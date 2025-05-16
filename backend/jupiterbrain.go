package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	gocontext "context"

	"github.com/cenk/backoff"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ssh"
)

const (
	defaultJupiterBrainImageSelectorType = "env"
	defaultBootPollDialTimeout           = 3 * time.Second
	defaultBootPollWaitForError          = 2 * time.Second
	defaultJupiterBrainSSHDialTimeout    = 5 * time.Second
)

var (
	metricNameCleanRegexp = regexp.MustCompile(`[^A-Za-z0-9.:-_]+`)
	jupiterBrainHelp      = map[string]string{
		"ENDPOINT":                 "[REQUIRED] url to Jupiter Brain server, including auth",
		"SSH_KEY_PATH":             "[REQUIRED] path to SSH key used to access job VMs",
		"SSH_KEY_PASSPHRASE":       "[REQUIRED] passphrase for SSH key given as SSH_KEY_PATH",
		"KEYCHAIN_PASSWORD":        "[REQUIRED] password used ... somehow",
		"IMAGE_SELECTOR_TYPE":      fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultJupiterBrainImageSelectorType),
		"IMAGE_SELECTOR_URL":       "URL for image selector API, used only when image selector is \"api\"",
		"IMAGE_ALIASES":            "comma-delimited strings used as stable names for images (default: \"\")",
		"IMAGE_ALIAS_{ALIAS}":      "full name for a given alias given via IMAGE_ALIASES, where the alias form in the key is uppercased and normalized by replacing non-alphanumerics with _",
		"BOOT_POLL_SLEEP":          "sleep interval between polling server for instance status (default 3s)",
		"BOOT_POLL_DIAL_TIMEOUT":   "how long to wait for a TCP connection to be made when polling SSH port (default 3s)",
		"BOOT_POLL_WAIT_FOR_ERROR": "time to wait for an error message after cancelling the boot polling (default 2s)",
		"SSH_DIAL_TIMEOUT":         fmt.Sprintf("connection timeout for ssh connections (default %v)", defaultJupiterBrainSSHDialTimeout),
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
	sshDialer            ssh.Dialer
	sshDialTimeout       time.Duration
	keychainPassword     string
	bootPollSleep        time.Duration
	bootPollDialTimeout  time.Duration
	bootPollWaitForError time.Duration

	imageSelectorType   string
	imageSelector       image.Selector
	defaultInstanceCPUs int
	defaultInstanceRAM  int

	apiClient *jupiterBrainAPIClient
}

type jupiterBrainInstance struct {
	payload    *jupiterBrainInstancePayload
	provider   *jupiterBrainProvider
	progresser Progresser

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

	sshDialTimeout := defaultJupiterBrainSSHDialTimeout
	if cfg.IsSet("SSH_DIAL_TIMEOUT") {
		sshDialTimeout, err = time.ParseDuration(cfg.Get("SSH_DIAL_TIMEOUT"))
		if err != nil {
			return nil, err
		}
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

	sshDialer, err := ssh.NewDialer(sshKeyPath, sshKeyPassphrase)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't set up SSH dialer")
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

	bootPollDialTimeout := defaultBootPollDialTimeout
	if cfg.IsSet("BOOT_POLL_DIAL_TIMEOUT") {
		si, err := time.ParseDuration(cfg.Get("BOOT_POLL_DIAL_TIMEOUT"))
		if err != nil {
			return nil, errors.Wrap(err, "error parsing boot pool dial timeout duration")
		}
		bootPollDialTimeout = si
	}

	bootPollWaitForError := defaultBootPollWaitForError
	if cfg.IsSet("BOOT_POLL_WAIT_FOR_ERROR") {
		si, err := time.ParseDuration(cfg.Get("BOOT_POLL_WAIT_FOR_ERROR"))
		if err != nil {
			return nil, errors.Wrap(err, "error parsing boot pool wait for error duration")
		}
		bootPollWaitForError = si
	}

	imageSelectorType := defaultJupiterBrainImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	imageSelector, err := buildJupiterBrainImageSelector(imageSelectorType, cfg)
	if err != nil {
		return nil, err
	}

	var defaultInstanceCPUs int
	if cfg.IsSet("INSTANCE_CPUS") {
		defaultInstanceCPUs, err = strconv.Atoi(cfg.Get("INSTANCE_CPUS"))
		if err != nil {
			return nil, errors.Wrap(err, "error parsing image CPU count")
		}
	}

	var defaultInstanceRAM int
	if cfg.IsSet("INSTANCE_RAM") {
		defaultInstanceRAM, err = strconv.Atoi(cfg.Get("INSTANCE_RAM"))
		if err != nil {
			return nil, errors.Wrap(err, "error parsing image RAM amount")
		}
	}

	return &jupiterBrainProvider{
		sshDialer:            sshDialer,
		sshDialTimeout:       sshDialTimeout,
		keychainPassword:     keychainPassword,
		bootPollSleep:        bootPollSleep,
		bootPollDialTimeout:  bootPollDialTimeout,
		bootPollWaitForError: bootPollWaitForError,

		imageSelectorType:   imageSelectorType,
		imageSelector:       imageSelector,
		defaultInstanceCPUs: defaultInstanceCPUs,
		defaultInstanceRAM:  defaultInstanceRAM,

		apiClient: &jupiterBrainAPIClient{
			client:  http.DefaultClient,
			baseURL: baseURL,
		},
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

func (p *jupiterBrainProvider) SupportsProgress() bool {
	return true
}

func (p *jupiterBrainProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	return p.StartWithProgress(ctx, startAttributes, NewTextProgresser(nil))
}

func (p *jupiterBrainProvider) StartWithProgress(ctx gocontext.Context, startAttributes *StartAttributes, progresser Progresser) (Instance, error) {
	var (
		imageName string
		err       error
	)

	if startAttributes.ImageName != "" {
		imageName = startAttributes.ImageName
	} else {
		imageName, err = p.getImageName(ctx, startAttributes)
		if err != nil {
			progresser.Progress(&ProgressEntry{
				Message: "could not select image",
				State:   ProgressFailure,
			})
			return nil, errors.Wrap(err, "couldn't get image name")
		}
	}

	progresser.Progress(&ProgressEntry{
		Message: fmt.Sprintf("selected image %q", imageName),
		State:   ProgressSuccess,
	})

	logger := context.LoggerFromContext(ctx).WithField("self", "backend/jupiterbrain_provider")

	logger.WithFields(logrus.Fields{
		"image_name": imageName,
		"osx_image":  startAttributes.OsxImage,
		"language":   startAttributes.Language,
		"dist":       startAttributes.Dist,
		"group":      startAttributes.Group,
		"os":         startAttributes.OS,
	}).Info("selected image name")

	startBooting := time.Now()

	// Start the instance
	instancePayload, err := p.apiClient.Start(ctx, imageName, p.defaultInstanceCPUs, p.defaultInstanceRAM)
	if err != nil {
		progresser.Progress(&ProgressEntry{
			Message: "could not create instance",
			State:   ProgressFailure,
		})
		return nil, errors.Wrap(err, "error creating instance in Jupiter Brain")
	}

	progresser.Progress(&ProgressEntry{
		Message: "started instance",
		State:   ProgressSuccess,
	})

	progresser.Progress(&ProgressEntry{
		Message: fmt.Sprintf("sleeping %s before checking instance start", p.bootPollSleep),
		State:   ProgressNeutral,
	})
	// Sleep to allow the new instance to be fully visible
	time.Sleep(p.bootPollSleep)

	progresser.Progress(&ProgressEntry{
		Message:   "polling for instance start completion...",
		State:     ProgressNeutral,
		Continues: true,
	})

	// Wait for instance to get IP address
	ip, payload, err := p.waitForIP(ctx, instancePayload.ID, progresser)
	if err != nil {
		if ctx.Err() == gocontext.DeadlineExceeded {
			progresser.Progress(&ProgressEntry{
				Message:    "timeout waiting for instance to be ready",
				State:      ProgressFailure,
				Interrupts: true,
			})
			metrics.Mark("worker.vm.provider.jupiterbrain.boot.timeout")
		}

		instance := &jupiterBrainInstance{
			payload:    instancePayload,
			provider:   p,
			progresser: progresser,
		}
		_ = instance.Stop(ctx)

		return nil, err
	}

	progresser.Progress(&ProgressEntry{
		Message:    "waiting for ssh connectivity...",
		State:      ProgressNeutral,
		Interrupts: true,
		Continues:  true,
	})

	// Wait for SSH to be ready
	err = p.waitForSSH(ctx, ip, progresser)
	if err != nil {
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.jupiterbrain.boot.timeout")
		}

		instance := &jupiterBrainInstance{
			payload:    instancePayload,
			provider:   p,
			progresser: progresser,
		}
		_ = instance.Stop(ctx)

		return nil, err
	}
	timeToSsh := time.Now().UTC().Sub(startBooting).Truncate(time.Millisecond)

	progresser.Progress(&ProgressEntry{
		Message:    fmt.Sprintf("instance is ready (%s)", timeToSsh),
		State:      ProgressSuccess,
		Interrupts: true,
	})

	metrics.TimeSince("worker.vm.provider.jupiterbrain.boot", startBooting)
	normalizedImageName := string(metricNameCleanRegexp.ReplaceAll([]byte(imageName), []byte("-")))
	metrics.TimeSince(fmt.Sprintf("worker.vm.provider.jupiterbrain.boot.image.%s", normalizedImageName), startBooting)
	logger.WithField("instance_uuid", payload.ID).Info("booted instance")

	if payload.BaseImage == "" {
		payload.BaseImage = imageName
	}

	return &jupiterBrainInstance{
		payload:    payload,
		provider:   p,
		progresser: progresser,

		startupDuration: time.Now().UTC().Sub(startBooting),
	}, nil
}

func (p *jupiterBrainProvider) Setup(ctx gocontext.Context) error {
	return nil
}

func (i *jupiterBrainInstance) Warmed() bool {
	return false
}

func (i *jupiterBrainInstance) SupportsProgress() bool {
	return true
}

func (i *jupiterBrainInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	waitStart := time.Now().UTC()
	i.progresser.Progress(&ProgressEntry{
		Message:   "waiting for ssh connectivity...",
		State:     ProgressNeutral,
		Continues: true,
	})

	conn, err := i.sshConnection()
	if err != nil {
		return errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	timeToSsh := time.Now().UTC().Sub(waitStart).Truncate(time.Millisecond)
	i.progresser.Progress(&ProgressEntry{
		Message:    fmt.Sprintf("ssh connectivity established (%s)", timeToSsh),
		State:      ProgressSuccess,
		Interrupts: true,
	})

	existed, err := conn.UploadFile("build.sh", script)
	if existed {
		i.progresser.Progress(&ProgressEntry{
			Message:    "existing script detected",
			State:      ProgressFailure,
			Interrupts: true,
		})
		return ErrStaleVM
	}
	if err != nil {
		return errors.Wrap(err, "couldn't upload build script")
	}

	i.progresser.Progress(&ProgressEntry{
		Message: "uploaded script",
		State:   ProgressSuccess,
	})

	_, err = conn.UploadFile("wrapper.sh", []byte(wrapperSh))
	if err != nil {
		return errors.Wrap(err, "couldn't upload wrapper.sh script")
	}

	i.progresser.Progress(&ProgressEntry{
		Message: "uploaded wrapper.sh script",
		State:   ProgressSuccess,
	})

	return nil
}

func (i *jupiterBrainInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	conn, err := i.sshConnection()
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	resultChan := make(chan struct {
		exitStatus int32
		err        error
	}, 1)

	go func() {
		exitStatus, err := conn.RunCommand("bash ~/wrapper.sh", output)
		resultChan <- struct {
			exitStatus int32
			err        error
		}{
			exitStatus,
			err,
		}
	}()

	select {
	case <-ctx.Done():
		return &RunResult{Completed: false}, ctx.Err()
	case result := <-resultChan:
		return &RunResult{Completed: result.err != nil, ExitCode: result.exitStatus}, errors.Wrap(err, "error running script")
	}
}

func (i *jupiterBrainInstance) DownloadTrace(ctx gocontext.Context) ([]byte, error) {
	conn, err := i.sshConnection()
	if err != nil {
		return nil, errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	buf, err := conn.DownloadFile("/tmp/build.trace")
	if err != nil {
		return nil, errors.Wrap(err, "couldn't download trace")
	}

	return buf, nil
}

func (i *jupiterBrainInstance) StopOnly(ctx gocontext.Context) error {
	return nil
}

func (i *jupiterBrainInstance) CreateImage(ctx gocontext.Context, createCustomImageName string, logWriterFunc func(string)) (int64, string, string, error) {
	return 0, "", "", nil
}

func (i *jupiterBrainInstance) Stop(ctx gocontext.Context) error {
	err := i.provider.apiClient.Stop(ctx, i.payload.ID)
	return errors.Wrap(err, "error sending Stop request to Jupiter Brain")
}

func (i *jupiterBrainInstance) ID() string {
	if i.payload == nil {
		return "{unidentified}"
	}
	return i.payload.ID
}

func (i *jupiterBrainInstance) ImageName() string {
	if i.payload == nil {
		return "{unidentified}"
	}
	return i.payload.BaseImage
}

func (i *jupiterBrainInstance) StartupDuration() time.Duration {
	return i.startupDuration
}

func (i *jupiterBrainInstance) sshConnection() (ssh.Connection, error) {
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

	return i.provider.sshDialer.Dial(fmt.Sprintf("%s:22", ip.String()), "travis", i.provider.sshDialTimeout)
}

func (p *jupiterBrainProvider) getImageName(ctx gocontext.Context, startAttributes *StartAttributes) (string, error) {
	defer context.TimeSince(ctx, "image_select", time.Now())

	jobID, _ := context.JobIDFromContext(ctx)
	repo, _ := context.RepositoryFromContext(ctx)

	return p.imageSelector.Select(ctx, &image.Params{
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

func (p *jupiterBrainProvider) waitForIP(ctx gocontext.Context, id string, progresser Progresser) (net.IP, *jupiterBrainInstancePayload, error) {
	defer context.TimeSince(ctx, "boot_poll_ip", time.Now())

	for {
		if ctx.Err() != nil {
			progresser.Progress(&ProgressEntry{
				Message:    "cancelling waiting for instance start",
				State:      ProgressFailure,
				Interrupts: true,
			})

			return nil, nil, errors.Errorf("cancelling waiting for instance to boot, was waiting for IP")
		}

		payload, err := p.apiClient.Get(ctx, id)
		if err != nil {
			progresser.Progress(&ProgressEntry{
				Message:    "could not check for instance start",
				State:      ProgressFailure,
				Interrupts: true,
			})
			return nil, nil, errors.Wrap(err, "error trying to refresh instance waiting for IP address")
		}

		for _, ipString := range payload.IPAddresses {
			curIP := net.ParseIP(ipString)
			if curIP.To4() != nil {
				return curIP, payload, nil
			}
		}

		progresser.Progress(&ProgressEntry{Message: ".", Raw: true})
		select {
		case <-time.After(p.bootPollSleep):
		case <-ctx.Done():
		}
	}
}

func (p *jupiterBrainProvider) waitForSSH(ctx gocontext.Context, ip net.IP, progresser Progresser) error {
	defer context.TimeSince(ctx, "boot_poll_ssh", time.Now())

	for {
		if ctx.Err() != nil {
			return errors.Errorf("cancelling waiting for instance to boot, was waiting for SSH to come up")
		}

		dialer := &net.Dialer{
			Timeout: p.bootPollDialTimeout,
		}

		conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:22", ip.String()))
		if conn != nil {
			conn.Close()
		}

		if err == nil {
			return nil
		}

		progresser.Progress(&ProgressEntry{Message: ".", Raw: true})
		select {
		case <-time.After(p.bootPollSleep):
		case <-ctx.Done():
		}
	}
}

type jupiterBrainAPIClient struct {
	client  *http.Client
	baseURL *url.URL
}

func (ac *jupiterBrainAPIClient) Start(ctx gocontext.Context, baseImage string, cpus int, ram int) (*jupiterBrainInstancePayload, error) {
	bodyPayload := map[string]map[string]interface{}{
		"data": {
			"type":       "instances",
			"base-image": baseImage,
		},
	}

	if cpus != 0 {
		bodyPayload["data"]["cpus"] = cpus
	}
	if ram != 0 {
		bodyPayload["data"]["ram"] = ram
	}

	jsonBody, err := json.Marshal(bodyPayload)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't marshal instance create request to JSON")
	}

	u, err := ac.baseURL.Parse("instances")
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create create-instance URL")
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create instance create request")
	}

	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := ac.httpDo(req)
	if err != nil {
		return nil, errors.Wrap(err, "error sending create instance request")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("expected 2xx from Jupiter Brain API, got %d (error: %s)", resp.StatusCode, body)
	}

	dataPayload := &jupiterBrainDataResponse{}
	err = json.NewDecoder(resp.Body).Decode(dataPayload)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't decode payload from Jupiter Brain")
	}

	if len(dataPayload.Data) != 1 {
		return nil, errors.Wrapf(err, "expected 1 instance to be returned, but got %d", len(dataPayload.Data))
	}

	return dataPayload.Data[0], nil
}

func (ac *jupiterBrainAPIClient) Get(ctx gocontext.Context, id string) (*jupiterBrainInstancePayload, error) {
	u, err := ac.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(id)))
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create fetch-instance URL")
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create instance fetch request")
	}

	resp, err := ac.httpDo(req)
	if err != nil {
		return nil, errors.Wrap(err, "error sending fetch instance request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("unknown status code: %d, expected 200 (body: %q)", resp.StatusCode, string(body))
	}

	dataPayload := &jupiterBrainDataResponse{}
	err = json.NewDecoder(resp.Body).Decode(dataPayload)
	if err != nil {
		_ = resp.Body.Close()
		return nil, errors.Wrap(err, "couldn't decode payload from Jupiter Brain")
	}

	if len(dataPayload.Data) != 1 {
		return nil, errors.Wrapf(err, "expected 1 instance to be returned, but got %d", len(dataPayload.Data))
	}

	return dataPayload.Data[0], nil
}

func (ac *jupiterBrainAPIClient) StopOnly(ctx gocontext.Context, id string) error {
	return nil
}

func (ac *jupiterBrainAPIClient) Stop(ctx gocontext.Context, id string) error {
	u, err := ac.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(id)))
	if err != nil {
		return errors.Wrap(err, "error creating instance stop URL")
	}

	req, err := http.NewRequest("DELETE", u.String(), nil)
	if err != nil {
		return errors.Wrap(err, "error creating instance stop request")
	}

	resp, err := ac.httpDo(req)
	if err != nil {
		return errors.Wrap(err, "error sending instance stop request")
	}
	resp.Body.Close()

	return nil
}

func (ac *jupiterBrainAPIClient) httpDo(req *http.Request) (*http.Response, error) {
	if req.URL.User != nil {
		token := req.URL.User.Username()
		req.URL.User = nil
		req.Header.Set("Authorization", "token "+token)
	}

	var resp *http.Response

	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	err := backoff.Retry(func() (err error) {
		resp, err = ac.client.Do(req)
		return
	}, b)

	return resp, err
}
