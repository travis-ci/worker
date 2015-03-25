package backend

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/sftp"
	workerctx "github.com/travis-ci/worker/lib/context"
	"github.com/travis-ci/worker/lib/metrics"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/context"
)

var nonAlphaNumRegexp = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

type JupiterBrainProvider struct {
	client           *http.Client
	baseURL          *url.URL
	imageAliases     map[string]string
	sshKeyPath       string
	sshKeyPassphrase string
	keychainPassword string
}

type JupiterBrainInstance struct {
	payload  jupiterBrainInstancePayload
	provider *JupiterBrainProvider
}

type jupiterBrainInstancePayload struct {
	ID          string   `json:"id"`
	IpAddresses []string `json:"ip-addresses"`
	State       string   `json:"state"`
	BaseImage   string   `json:"base-image,omitempty"`
	Type        string   `json:"type,omitempty"`
}

type jupiterBrainDataResponse struct {
	Data []jupiterBrainInstancePayload `json:"data"`
}

func NewJupiterBrainProvider(config map[string]string) (*JupiterBrainProvider, error) {
	endpoint, ok := config["endpoint"]
	if !ok {
		return nil, fmt.Errorf("expected endpoint config key")
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	aliasNames, ok := config["image_aliases"]
	if !ok {
		return nil, fmt.Errorf("expected image_aliases config key")
	}

	imageAliases := make(map[string]string, len(aliasNames))

	for _, aliasName := range strings.Split(aliasNames, ",") {
		normalizedAliasName := string(nonAlphaNumRegexp.ReplaceAll([]byte(aliasName), []byte("_")))

		imageName, ok := config[fmt.Sprintf("image_alias_%s", normalizedAliasName)]
		if !ok {
			return nil, fmt.Errorf("expected image alias %q", aliasName)
		}

		imageAliases[aliasName] = imageName
	}

	sshKeyPath, ok := config["ssh_key_path"]
	if !ok {
		return nil, fmt.Errorf("expected ssh_key_path config key")
	}

	sshKeyPassphrase, ok := config["ssh_key_passphrase"]
	if !ok {
		return nil, fmt.Errorf("expected ssh_key_passphrase config key")
	}

	keychainPassword, ok := config["keychain_password"]
	if !ok {
		return nil, fmt.Errorf("expected keychain_password config key")
	}

	return &JupiterBrainProvider{
		client:           http.DefaultClient,
		baseURL:          baseURL,
		imageAliases:     imageAliases,
		sshKeyPath:       sshKeyPath,
		sshKeyPassphrase: sshKeyPassphrase,
		keychainPassword: keychainPassword,
	}, nil
}

func (p *JupiterBrainProvider) Start(ctx context.Context, startAttributes StartAttributes) (Instance, error) {
	u, err := p.baseURL.Parse("instances")
	if err != nil {
		return nil, err
	}

	normalizedAliasName := string(nonAlphaNumRegexp.ReplaceAll([]byte(startAttributes.OsxImage), []byte("_")))
	imageName, ok := p.imageAliases[normalizedAliasName]
	if !ok {
		imageName, _ = p.imageAliases["default"]
	}

	if imageName == "" {
		return nil, fmt.Errorf("no image alias for %s", startAttributes.OsxImage)
	}

	startBooting := time.Now()

	bodyPayload := map[string]map[string]string{
		"data": {
			"type":       "instances",
			"base-image": imageName,
		},
	}

	jsonBody, err := json.Marshal(bodyPayload)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Post(u.String(), "application/vnd.api+json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	defer io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()

	if c := resp.StatusCode; c < 200 || c >= 300 {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("expected 2xx from Jupiter Brain API, got %d (error: %s)", c, body)
	}

	var dataPayload jupiterBrainDataResponse
	err = json.NewDecoder(resp.Body).Decode(&dataPayload)
	if err != nil {
		return nil, fmt.Errorf("couldn't decode created payload: %s", err)
	}

	payload := dataPayload.Data[0]

	instanceReady := make(chan jupiterBrainInstancePayload, 1)
	errChan := make(chan error, 1)
	go func(id string) {
		u, err := p.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(id)))
		if err != nil {
			errChan <- err
			return
		}

		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			errChan <- err
			return
		}

		for true {
			resp, err := p.client.Do(req)
			if err != nil {
				errChan <- err
				return
			}

			var dataPayload jupiterBrainDataResponse
			err = json.NewDecoder(resp.Body).Decode(&dataPayload)
			if err != nil {
				errChan <- fmt.Errorf("couldn't decode refresh payload: %s", err)
				return
			}
			payload := dataPayload.Data[0]

			_, _ = io.Copy(ioutil.Discard, resp.Body)
			_ = resp.Body.Close()

			var ip net.IP
			for _, ipString := range payload.IpAddresses {
				curIp := net.ParseIP(ipString)
				if curIp.To4() != nil {
					ip = curIp
					break
				}

			}

			if ip == nil {
				continue
			}

			conn, err := net.Dial("tcp", fmt.Sprintf("%s:22", ip.String()))
			if conn != nil {
				conn.Close()
			}

			if err == nil {
				instanceReady <- payload
				return
			}
		}
	}(payload.ID)

	select {
	case payload := <-instanceReady:
		metrics.TimeSince("worker.vm.provider.jupiterbrain.boot", startBooting)
		workerctx.LoggerFromContext(ctx).WithField("instance_uuid", payload.ID).Info("booted instance")
		return &JupiterBrainInstance{
			payload:  payload,
			provider: p,
		}, nil
	case err := <-errChan:
		instance := &JupiterBrainInstance{
			payload:  payload,
			provider: p,
		}
		instance.Stop(ctx)

		return nil, err
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.jupiterbrain.boot.timeout")
		}

		instance := &JupiterBrainInstance{
			payload:  payload,
			provider: p,
		}
		instance.Stop(ctx)

		return nil, ctx.Err()
	}
}

func (i *JupiterBrainInstance) UploadScript(ctx context.Context, script []byte) error {
	client, err := i.sshClient()
	if err != nil {
		return err
	}
	defer client.Close()

	sftp, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftp.Close()

	f, err := sftp.Create("build.sh")
	if err != nil {
		return err
	}

	_, err = f.Write(script)
	if err != nil {
		return err
	}

	f, err = sftp.Create("wrapper.sh")
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(f, `#!/bin/bash

[[ -f ~/build.sh.exit ]] && rm ~/build.sh.exit

until nc 127.0.0.1 15782; do sleep 1; done

until [[ -f ~/build.sh.exit ]]; do sleep 1; done
exit $(cat ~/build.sh.exit)
`)

	return err
}

func (i *JupiterBrainInstance) RunScript(ctx context.Context, output io.WriteCloser) (RunResult, error) {
	client, err := i.sshClient()
	if err != nil {
		return RunResult{Completed: false}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return RunResult{Completed: false}, err
	}
	defer session.Close()

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
	if err != nil {
		return RunResult{Completed: false}, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Run("bash ~/wrapper.sh")
	defer output.Close()
	if err == nil {
		return RunResult{Completed: true, ExitCode: 0}, nil
	}

	switch err := err.(type) {
	case *ssh.ExitError:
		return RunResult{Completed: true, ExitCode: uint8(err.ExitStatus())}, nil
	default:
		return RunResult{Completed: false}, err
	}
}

func (i *JupiterBrainInstance) Stop(ctx context.Context) error {
	u, err := i.provider.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(i.payload.ID)))
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := i.provider.client.Do(req)
	io.Copy(ioutil.Discard, resp.Body)
	resp.Body.Close()
	return err
}

func (i *JupiterBrainInstance) sshClient() (*ssh.Client, error) {
	file, err := ioutil.ReadFile(i.provider.sshKeyPath)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(file)
	if block == nil {
		return nil, fmt.Errorf("ssh key does not contain a valid PEM block")
	}

	der, err := x509.DecryptPEMBlock(block, []byte(i.provider.sshKeyPassphrase))
	if err != nil {
		return nil, err
	}

	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, err
	}

	var ip net.IP
	for _, ipString := range i.payload.IpAddresses {
		curIp := net.ParseIP(ipString)
		if curIp.To4() != nil {
			ip = curIp
			break
		}

	}

	if ip == nil {
		return nil, fmt.Errorf("no valid IPv4 address")
	}

	return ssh.Dial("tcp", fmt.Sprintf("%s:22", ip.String()), &ssh.ClientConfig{
		User: "travis",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
	})
}
