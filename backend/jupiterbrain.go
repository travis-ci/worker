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
	"github.com/cenkalti/backoff"
	"github.com/codegangsta/cli"
	"github.com/pkg/sftp"
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
	jupiterBrainFlags = []cli.Flag{
		backendStringFlag("jupiterbrain", "endpoint", "",
			"URL to Jupiter Brain server, including auth",
			[]string{"ENDPOINT"}),
		backendStringFlag("jupiterbrain", "ssh-key-path", "",
			"Path to SSH key used to access job VMs",
			[]string{"SSH_KEY_PATH"}),
		backendStringFlag("jupiterbrain", "ssh-key-passphrase", "",
			"Passphrase for SSH key given as SSH_KEY_PATH",
			[]string{"SSH_KEY_PASSPHRASE"}),
		backendStringFlag("jupiterbrain", "keychain-password", "",
			"password used ... somehow",
			[]string{"KEYCHAIN_PASSWORD"}),
		backendStringFlag("jupiterbrain", "image-selector-type", defaultGCEImageSelectorType,
			"Image selector type (\"env\" or \"api\")",
			[]string{"IMAGE_SELECTOR_TYPE"}),
		backendStringFlag("jupiterbrain", "image-selector-url", "",
			"URL for image selector API, used only when image selector is \"api\"",
			[]string{"IMAGE_SELECTOR_URL"}),
		&cli.StringSliceFlag{
			Name:   "images",
			Usage:  "Key=value pairs of image names",
			EnvVar: "JUPITERBRAIN_IMAGES,TRAVIS_WORKER_JUPITERBRAIN_IMAGES",
		},
		&cli.StringSliceFlag{
			Name:   "image-aliases",
			Usage:  "Key=value pairs of image aliases",
			EnvVar: "JUPITERBRAIN_IMAGE_ALIASES,TRAVIS_WORKER_JUPITERBRAIN_IMAGE_ALIASES",
		},
		&cli.DurationFlag{
			Name:   "boot-poll-sleep",
			Usage:  "Sleep interval between polling server for instance ready status",
			Value:  3 * time.Second,
			EnvVar: "JUPITERBRAIN_BOOT_POLL_SLEEP,TRAVIS_WORKER_JUPITERBRAIN_BOOT_POLL_SLEEP",
		},
	}

	metricNameCleanRegexp = regexp.MustCompile(`[^A-Za-z0-9.:-_]+`)
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
	Register("jupiterbrain", "Jupiter Brain", jupiterBrainFlags, newJupiterBrainProvider)
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

func newJupiterBrainProvider(c *cli.Context) (Provider, error) {
	if c.String("endpoint") == "" {
		return nil, ErrMissingEndpointConfig
	}

	baseURL, err := url.Parse(c.String("endpoint"))
	if err != nil {
		return nil, err
	}

	if c.String("ssh-key-path") == "" {
		return nil, fmt.Errorf("expected ssh-key-path config key")
	}

	sshKeyPath := c.String("ssh-key-path")

	if c.String("ssh-key-passphrase") == "" {
		return nil, fmt.Errorf("expected ssh-key-passphrase config key")
	}

	sshKeyPassphrase := c.String("ssh-key-passphrase")

	if c.String("keychain-password") == "" {
		return nil, fmt.Errorf("expected keychain-password config key")
	}

	keychainPassword := c.String("keychain-password")
	bootPollSleep := c.Duration("boot-poll-sleep")

	imageSelectorType := c.String("image-selector-type")

	imageSelector, err := buildJupiterBrainImageSelector(imageSelectorType, c)
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

func buildJupiterBrainImageSelector(selectorType string, c *cli.Context) (image.Selector, error) {
	switch selectorType {
	case "env":
		return image.NewEnvSelector(c)
	case "api":
		baseURL, err := url.Parse(c.String("image-selector-url"))
		if err != nil {
			return nil, err
		}
		return image.NewAPISelector(baseURL), nil
	default:
		return nil, fmt.Errorf("invalid image selector type %q", selectorType)
	}
}

func (p *jupiterBrainProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	u, err := p.baseURL.Parse("instances")
	if err != nil {
		return nil, err
	}

	imageName, err := p.getImageName(ctx, startAttributes)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := p.httpDo(req)
	if err != nil {
		return nil, err
	}
	defer io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()

	if c := resp.StatusCode; c < 200 || c >= 300 {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("expected 2xx from Jupiter Brain API, got %d (error: %s)", c, body)
	}

	dataPayload := &jupiterBrainDataResponse{}
	err = json.NewDecoder(resp.Body).Decode(dataPayload)
	if err != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"err":     err,
			"payload": dataPayload,
			"body":    resp.Body,
		}).Error("couldn't decode created payload")
		return nil, fmt.Errorf("couldn't decode created payload: %s", err)
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

		for {
			resp, err := p.httpDo(req)
			if err != nil {
				errChan <- err
				return
			}

			if resp.StatusCode != 200 {
				body, _ := ioutil.ReadAll(resp.Body)
				errChan <- fmt.Errorf("unknown status code: %d, expected 200 (body: %q)", resp.StatusCode, string(body))
				return
			}

			dataPayload := &jupiterBrainDataResponse{}
			err = json.NewDecoder(resp.Body).Decode(dataPayload)
			if err != nil {
				errChan <- fmt.Errorf("couldn't decode refresh payload: %s", err)
				return
			}
			payload := dataPayload.Data[0]

			_, _ = io.Copy(ioutil.Discard, resp.Body)
			_ = resp.Body.Close()

			var ip net.IP
			for _, ipString := range payload.IPAddresses {
				curIP := net.ParseIP(ipString)
				if curIP.To4() != nil {
					ip = curIP
					break
				}

			}

			if ip == nil {
				time.Sleep(p.bootPollSleep)
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

			time.Sleep(p.bootPollSleep)
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

		return nil, ctx.Err()
	}
}

func (p *jupiterBrainProvider) Setup() error {
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

	_, err = fmt.Fprintf(f, wrapperSh)

	return err
}

func (i *jupiterBrainInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
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
		return &RunResult{Completed: false}, err
	}
}

func (i *jupiterBrainInstance) Stop(ctx gocontext.Context) error {
	u, err := i.provider.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(i.payload.ID)))
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := i.provider.httpDo(req)
	if err != nil {
		return err
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
	for _, ipString := range i.payload.IPAddresses {
		curIP := net.ParseIP(ipString)
		if curIP.To4() != nil {
			ip = curIP
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
