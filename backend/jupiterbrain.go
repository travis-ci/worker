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
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/cenk/backoff"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/metrics"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
)

const (
	defaultJupiterBrainImageSelectorType = "env"
)

var (
	metricNameCleanRegexp = regexp.MustCompile(`[^A-Za-z0-9.:-_]+`)
	jupiterBrainHelp      = map[string]string{
		"ENDPOINT":            "[REQUIRED] url to Jupiter Brain server, including auth",
		"SSH_KEY_PATH":        "[REQUIRED] path to SSH key used to access job VMs",
		"SSH_KEY_PASSPHRASE":  "[REQUIRED] passphrase for SSH key given as SSH_KEY_PATH",
		"KEYCHAIN_PASSWORD":   "[REQUIRED] password used ... somehow",
		"IMAGE_SELECTOR_TYPE": fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultJupiterBrainImageSelectorType),
		"IMAGE_SELECTOR_URL":  "URL for image selector API, used only when image selector is \"api\"",
		"IMAGE_ALIASES":       "comma-delimited strings used as stable names for images (default: \"\")",
		"IMAGE_ALIAS_{ALIAS}": "full name for a given alias given via IMAGE_ALIASES, where the alias form in the key is uppercased and normalized by replacing non-alphanumerics with _",
		"BOOT_POLL_SLEEP":     "sleep interval between polling server for instance status (default 3s)",
	}
)

const (
	wrapperSh = `#!/bin/bash

[[ $(uname) = Linux ]] && exec bash ~/build.sh

[[ -f ~/build.sh.exit ]] && rm ~/build.sh.exit

until nc 127.0.0.1 15782; do sleep 1; done

until [[ -f ~/build.sh.exit ]]; do sleep 1; done
exit $(cat ~/build.sh.exit)
`
)

func init() {
	Register("jupiterbrain", "Jupiter Brain", jupiterBrainHelp, newJupiterBrainProvider)
}

type jupiterBrainProvider struct {
	client           *http.Client
	baseURL          *url.URL
	sshKeyPath       string
	sshKeyPassphrase string
	keychainPassword string
	bootPollSleep    time.Duration

	imageSelectorType string
	imageSelector     image.Selector
}

type jupiterBrainInstance struct {
	payload  *jupiterBrainInstancePayload
	provider *jupiterBrainProvider

	startupDuration time.Duration
}

type jupiterBrainInstancePayload struct {
	ID          string   `json:"id"`
	IPAddresses []string `json:"ip-addresses"`
	State       string   `json:"state"`
	BaseImage   string   `json:"base-image,omitempty"`
	Type        string   `json:"type,omitempty"`
}

type jupiterBrainDataResponse struct {
	Data []*jupiterBrainInstancePayload `json:"data"`
}

func newJupiterBrainProvider(cfg *config.ProviderConfig) (Provider, error) {
	if !cfg.IsSet("ENDPOINT") {
		return nil, ErrMissingEndpointConfig
	}

	baseURL, err := url.Parse(cfg.Get("ENDPOINT"))
	if err != nil {
		return nil, errors.Wrap(err, "error parsing Jupiter Brain endpoint URL")
	}

	if !cfg.IsSet("SSH_KEY_PATH") {
		return nil, errors.Errorf("expected SSH_KEY_PATH config key")
	}

	sshKeyPath := cfg.Get("SSH_KEY_PATH")

	if !cfg.IsSet("SSH_KEY_PASSPHRASE") {
		return nil, errors.Errorf("expected SSH_KEY_PASSPHRASE config key")
	}

	sshKeyPassphrase := cfg.Get("SSH_KEY_PASSPHRASE")

	if !cfg.IsSet("KEYCHAIN_PASSWORD") {
		return nil, errors.Errorf("expected KEYCHAIN_PASSWORD config key")
	}

	keychainPassword := cfg.Get("KEYCHAIN_PASSWORD")

	bootPollSleep := 3 * time.Second
	if cfg.IsSet("BOOT_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("BOOT_POLL_SLEEP"))
		if err != nil {
			return nil, errors.Wrap(err, "error parsing boot pool sleep duration")
		}
		bootPollSleep = si
	}

	imageSelectorType := defaultJupiterBrainImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	imageSelector, err := buildJupiterBrainImageSelector(imageSelectorType, cfg)
	if err != nil {
		return nil, err
	}

	return &jupiterBrainProvider{
		client:           http.DefaultClient,
		baseURL:          baseURL,
		sshKeyPath:       sshKeyPath,
		sshKeyPassphrase: sshKeyPassphrase,
		keychainPassword: keychainPassword,
		bootPollSleep:    bootPollSleep,

		imageSelectorType: imageSelectorType,
		imageSelector:     imageSelector,
	}, nil
}

func buildJupiterBrainImageSelector(selectorType string, cfg *config.ProviderConfig) (image.Selector, error) {
	switch selectorType {
	case "env":
		return image.NewEnvSelector(cfg)
	case "api":
		baseURL, err := url.Parse(cfg.Get("IMAGE_SELECTOR_URL"))
		if err != nil {
			return nil, errors.Wrap(err, "error parsing image selector URL")
		}
		return image.NewAPISelector(baseURL), nil
	default:
		return nil, fmt.Errorf("invalid image selector type %q", selectorType)
	}
}

func (p *jupiterBrainProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	u, err := p.baseURL.Parse("instances")
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create instances URL")
	}

	imageName, err := p.getImageName(ctx, startAttributes)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't get image name")
	}

	context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"image_name": imageName,
		"osx_image":  startAttributes.OsxImage,
		"language":   startAttributes.Language,
		"dist":       startAttributes.Dist,
		"group":      startAttributes.Group,
		"os":         startAttributes.OS,
	}).Info("selected image name")

	startBooting := time.Now()

	bodyPayload := map[string]map[string]string{
		"data": {
			"type":       "instances",
			"base-image": imageName,
		},
	}

	jsonBody, err := json.Marshal(bodyPayload)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't marshal instance create request to JSON")
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create instance create request")
	}
	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := p.httpDo(req)
	if err != nil {
		return nil, errors.Wrap(err, "error sending create instance request")
	}
	defer io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()

	if c := resp.StatusCode; c < 200 || c >= 300 {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, errors.Errorf("expected 2xx from Jupiter Brain API, got %d (error: %s)", c, body)
	}

	dataPayload := &jupiterBrainDataResponse{}
	err = json.NewDecoder(resp.Body).Decode(dataPayload)
	if err != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"err":     err,
			"payload": dataPayload,
			"body":    resp.Body,
		}).Error("couldn't decode created payload")
		return nil, errors.Wrap(err, "couldn't decode created payload")
	}

	payload := dataPayload.Data[0]

	instanceReady := make(chan *jupiterBrainInstancePayload, 1)
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

		var ip net.IP

		for {
			select {
			case <-ctx.Done():
				if ip == nil {
					errChan <- errors.Errorf("cancelling waiting for instance to boot, was waiting for IP")
				} else {
					errChan <- errors.Errorf("cancelling waiting for instance to boot, was waiting for SSH to come up")
				}

				return
			default:
			}

			resp, err := p.httpDo(req)
			if err != nil {
				errChan <- err
				return
			}

			if resp.StatusCode != 200 {
				body, _ := ioutil.ReadAll(resp.Body)
				_ = resp.Body.Close()
				errChan <- errors.Errorf("unknown status code: %d, expected 200 (body: %q)", resp.StatusCode, string(body))
				return
			}

			dataPayload := &jupiterBrainDataResponse{}
			err = json.NewDecoder(resp.Body).Decode(dataPayload)
			if err != nil {
				_ = resp.Body.Close()
				errChan <- errors.Wrap(err, "couldn't decode refresh payload")
				return
			}
			payload := dataPayload.Data[0]

			_, _ = io.Copy(ioutil.Discard, resp.Body)
			_ = resp.Body.Close()

			for _, ipString := range payload.IPAddresses {
				curIP := net.ParseIP(ipString)
				if curIP.To4() != nil {
					ip = curIP
					break
				}

			}

			if ip == nil {
				select {
				case <-time.After(p.bootPollSleep):
				case <-ctx.Done():
				}

				continue
			}

			dialer := &net.Dialer{
				Timeout: time.Second,
			}

			conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:22", ip.String()))
			if conn != nil {
				conn.Close()
			}

			if err == nil {
				instanceReady <- payload
				return
			}

			select {
			case <-time.After(p.bootPollSleep):
			case <-ctx.Done():
			}
		}
	}(payload.ID)

	select {
	case payload := <-instanceReady:
		metrics.TimeSince("worker.vm.provider.jupiterbrain.boot", startBooting)
		normalizedImageName := string(metricNameCleanRegexp.ReplaceAll([]byte(imageName), []byte("-")))
		metrics.TimeSince(fmt.Sprintf("worker.vm.provider.jupiterbrain.boot.image.%s", normalizedImageName), startBooting)
		context.LoggerFromContext(ctx).WithField("instance_uuid", payload.ID).Info("booted instance")

		if payload.BaseImage == "" {
			payload.BaseImage = imageName
		}

		return &jupiterBrainInstance{
			payload:         payload,
			provider:        p,
			startupDuration: time.Now().UTC().Sub(startBooting),
		}, nil
	case err := <-errChan:
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.jupiterbrain.boot.timeout")
		}

		instance := &jupiterBrainInstance{
			payload:  payload,
			provider: p,
		}
		instance.Stop(ctx)

		return nil, err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.jupiterbrain.boot.timeout")
		}

		instance := &jupiterBrainInstance{
			payload:  payload,
			provider: p,
		}
		instance.Stop(ctx)

		select {
		case err := <-errChan:
			return nil, err
		case <-time.After(2 * time.Second):
			return nil, ctx.Err()
		}
	}
}

func (p *jupiterBrainProvider) Setup(ctx gocontext.Context) error {
	return nil
}

func (p *jupiterBrainProvider) httpDo(req *http.Request) (*http.Response, error) {
	if req.URL.User != nil {
		token := req.URL.User.Username()
		req.URL.User = nil
		req.Header.Set("Authorization", "token "+token)
	}

	var resp *http.Response = nil

	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	err := backoff.Retry(func() (err error) {
		resp, err = p.client.Do(req)
		return
	}, b)

	return resp, err
}

func (i *jupiterBrainInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	client, err := i.sshClient()
	if err != nil {
		return errors.Wrap(err, "couldn't create SSH client")
	}
	defer client.Close()

	sftp, err := sftp.NewClient(client)
	if err != nil {
		return errors.Wrap(err, "couldn't create SFTP client")
	}
	defer sftp.Close()

	_, err = sftp.Lstat("build.sh")
	if err == nil {
		return ErrStaleVM
	}

	f, err := sftp.Create("build.sh")
	if err != nil {
		return errors.Wrap(err, "couldn't create build.sh file")
	}

	_, err = f.Write(script)
	if err != nil {
		return errors.Wrap(err, "couldn't write to build.sh file")
	}

	f, err = sftp.Create("wrapper.sh")
	if err != nil {
		return errors.Wrap(err, "couldn't create wrapper.sh file")
	}

	_, err = fmt.Fprintf(f, wrapperSh)
	if err != nil {
		return errors.Wrap(err, "couldn't write to wrapper.sh file")
	}

	return nil
}

func (i *jupiterBrainInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	client, err := i.sshClient()
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "error creating SSH client")
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "error creating SSH session")
	}
	defer session.Close()

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "error requesting PTY")
	}

	session.Stdout = output

	errChan := make(chan error, 1)

	go func() {
		errChan <- session.Run("bash ~/wrapper.sh")
	}()

	select {
	case <-ctx.Done():
		return &RunResult{Completed: false}, ctx.Err()
	case err = <-errChan:
	}

	if err == nil {
		return &RunResult{Completed: true, ExitCode: 0}, nil
	}

	switch err := err.(type) {
	case *ssh.ExitError:
		return &RunResult{Completed: true, ExitCode: uint8(err.ExitStatus())}, nil
	default:
		return &RunResult{Completed: false}, errors.Wrap(err, "error running script")
	}
}

func (i *jupiterBrainInstance) Stop(ctx gocontext.Context) error {
	u, err := i.provider.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(i.payload.ID)))
	if err != nil {
		return errors.Wrap(err, "error creating instance stop URL")
	}

	req, err := http.NewRequest("DELETE", u.String(), nil)
	if err != nil {
		return errors.Wrap(err, "error creating instance stop request")
	}

	resp, err := i.provider.httpDo(req)
	if err != nil {
		return errors.Wrap(err, "error sending instance stop request")
	}

	resp.Body.Close()
	return nil
}

func (i *jupiterBrainInstance) ID() string {
	if i.payload == nil {
		return "{unidentified}"
	}
	return fmt.Sprintf("%s:%s", i.payload.ID, i.payload.BaseImage)
}

func (i *jupiterBrainInstance) StartupDuration() time.Duration {
	return i.startupDuration
}

func (i *jupiterBrainInstance) sshClient() (*ssh.Client, error) {
	file, err := ioutil.ReadFile(i.provider.sshKeyPath)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't read SSH key")
	}

	block, _ := pem.Decode(file)
	if block == nil {
		return nil, errors.Errorf("ssh key does not contain a valid PEM block")
	}

	der, err := x509.DecryptPEMBlock(block, []byte(i.provider.sshKeyPassphrase))
	if err != nil {
		return nil, errors.Wrap(err, "couldn't decrypt SSH key")
	}

	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't parse SSH key")
	}

	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create signer from SSH key")
	}

	var ip net.IP
	for _, ipString := range i.payload.IPAddresses {
		curIP := net.ParseIP(ipString)
		if curIP.To4() != nil {
			ip = curIP
			break
		}

	}

	if ip == nil {
		return nil, errors.Errorf("no valid IPv4 address")
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", ip.String()), &ssh.ClientConfig{
		User: "travis",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
	})
	return client, errors.Wrap(err, "couldn't connect to SSH server")
}

func (p *jupiterBrainProvider) getImageName(ctx gocontext.Context, startAttributes *StartAttributes) (string, error) {
	jobID, _ := context.JobIDFromContext(ctx)
	repo, _ := context.RepositoryFromContext(ctx)

	return p.imageSelector.Select(&image.Params{
		Infra:    "jupiterbrain",
		Language: startAttributes.Language,
		OsxImage: startAttributes.OsxImage,
		Dist:     startAttributes.Dist,
		Group:    startAttributes.Group,
		OS:       startAttributes.OS,
		JobID:    jobID,
		Repo:     repo,
	})
}
