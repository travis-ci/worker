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
	"os"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	workerctx "github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/context"
)

const (
	sauceLabsHelp = `
`
)

func init() {
	// XXX: should this provider be removed?
	// config.SetProviderHelp("Sauce Labs", sauceLabsHelp)
}

type SauceLabsProvider struct {
	client           *http.Client
	baseURL          *url.URL
	imageAliases     map[string]string
	sshKeyPath       string
	sshKeyPassphrase string
}

type SauceLabsInstance struct {
	payload  sauceLabsInstancePayload
	provider *SauceLabsProvider
}

type sauceLabsInstancePayload struct {
	ID          string                 `json:"instance_id"`
	ImageID     string                 `json:"image_id"`
	RealImageID string                 `json:"real_image_id"`
	State       string                 `json:"state"`
	PrivateIP   string                 `json:"private_ip"`
	ExtraInfo   map[string]interface{} `json:"extra_info"`
}

func NewSauceLabsProvider(cfg *config.ProviderConfig) (*SauceLabsProvider, error) {
	if !cfg.IsSet("ENDPOINT") {
		return nil, ErrMissingEndpointConfig
	}

	if !cfg.IsSet("IMAGE_ALIASES") {
		return nil, fmt.Errorf("expected IMAGE_ALIASES config key")
	}

	baseURL, err := url.Parse(cfg.Get("ENDPOINT"))
	if err != nil {
		return nil, err
	}

	aliasNames := cfg.Get("IMAGE_ALIASES")
	aliasNamesSlice := strings.Split(aliasNames, ",")

	imageAliases := make(map[string]string, len(aliasNamesSlice))

	for _, aliasName := range aliasNamesSlice {
		key := strings.ToUpper(fmt.Sprintf("IMAGE_ALIAS_%s", aliasName))

		if !cfg.IsSet(key) {
			return nil, fmt.Errorf("expected image alias %q", aliasName)
		}

		imageAliases[aliasName] = cfg.Get(key)
	}

	if !cfg.IsSet("SSH_KEY_PATH") {
		return nil, fmt.Errorf("expected SSH_KEY_PATH config key")
	}

	sshKeyPath := cfg.Get("SSH_KEY_PATH")

	if !cfg.IsSet("SSH_KEY_PASSPHRASE") {
		return nil, fmt.Errorf("expected SSH_KEY_PASSPHRASE config key")
	}

	sshKeyPassphrase := cfg.Get("SSH_KEY_PASSPHRASE")

	return &SauceLabsProvider{
		client:           http.DefaultClient,
		baseURL:          baseURL,
		imageAliases:     imageAliases,
		sshKeyPath:       sshKeyPath,
		sshKeyPassphrase: sshKeyPassphrase,
	}, nil
}

func (p *SauceLabsProvider) Start(ctx context.Context, startAttributes *StartAttributes) (Instance, error) {
	startupInfo, err := json.Marshal(map[string]interface{}{"worker_pid": os.Getpid(), "source": "worker"})
	if err != nil {
		return nil, err
	}

	imageName, ok := p.imageAliases[startAttributes.OsxImage]
	if !ok {
		imageName, _ = p.imageAliases["default"]
	}

	if imageName == "" {
		return nil, fmt.Errorf("no image alias for %s", startAttributes.OsxImage)
	}

	u, err := p.baseURL.Parse(fmt.Sprintf("instances?image=%s", url.QueryEscape(imageName)))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(startupInfo))
	if err != nil {
		return nil, err
	}

	startBooting := time.Now()

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()

	if c := resp.StatusCode; c < 200 || c >= 300 {
		return nil, fmt.Errorf("expected 2xx from Sauce Labs API, got %d", c)
	}

	var payload sauceLabsInstancePayload
	err = json.NewDecoder(resp.Body).Decode(&payload)

	instanceReady := make(chan sauceLabsInstancePayload, 1)
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
			defer io.Copy(ioutil.Discard, resp.Body)
			defer resp.Body.Close()

			var payload sauceLabsInstancePayload
			err = json.NewDecoder(resp.Body).Decode(&payload)
			if err != nil {
				errChan <- err
				return
			}

			conn, err := net.Dial("tcp", fmt.Sprintf("%s:3422", payload.PrivateIP))
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
		metrics.TimeSince("worker.vm.provider.sauce_labs.boot", startBooting)
		workerctx.LoggerFromContext(ctx).WithField("instance_id", payload.ID).Info("booted instance")
		return &SauceLabsInstance{
			payload:  payload,
			provider: p,
		}, nil
	case err := <-errChan:
		instance := &SauceLabsInstance{
			payload:  payload,
			provider: p,
		}
		instance.Stop(ctx)

		return nil, err
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.sauce_labs.boot.timeout")
		}

		instance := &SauceLabsInstance{
			payload:  payload,
			provider: p,
		}
		instance.Stop(ctx)

		return nil, ctx.Err()
	}
}

func (i *SauceLabsInstance) UploadScript(ctx context.Context, script []byte) error {
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

	_, err = sftp.Lstat("build.sh")
	if err == nil {
		return ErrStaleVM
	}

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

func (i *SauceLabsInstance) RunScript(ctx context.Context, output io.WriteCloser) (*RunResult, error) {
	client, err := i.sshClient()
	if err != nil {
		return &RunResult{Completed: false}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return &RunResult{Completed: false}, err
	}
	defer session.Close()

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Run("bash ~/wrapper.sh")
	defer output.Close()
	if err == nil {
		return &RunResult{Completed: true, ExitCode: 0}, nil
	}

	switch err := err.(type) {
	case *ssh.ExitError:
		return &RunResult{Completed: true, ExitCode: uint8(err.ExitStatus())}, nil
	default:
		return &RunResult{Completed: false}, err
	}
}

func (i *SauceLabsInstance) Stop(ctx context.Context) error {
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

func (i *SauceLabsInstance) sshClient() (*ssh.Client, error) {
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

	return ssh.Dial("tcp", fmt.Sprintf("%s:3422", i.payload.PrivateIP), &ssh.ClientConfig{
		User: "travis",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
	})
}

func (i *SauceLabsInstance) ID() string {
	return fmt.Sprintf("%s:%s", i.payload.ID, i.payload.ImageID)
}
