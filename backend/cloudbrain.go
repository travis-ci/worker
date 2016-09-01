package backend

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ratelimit"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
)

const (
	defaultCloudBrainBootPollSleep     = 3 * time.Second
	defaultCloudBrainBootPrePollSleep  = 15 * time.Second
	defaultCloudBrainUploadRetries     = uint64(120)
	defaultCloudBrainUploadRetrySleep  = 1 * time.Second
	defaultCloudBrainImageSelectorType = "env"
)

var (
	cbHelp = map[string]string{
		"ENDPOINT":              "cloud-brain HTTP endpoint, including token",
		"PROVIDER":              "cloud-brain provider name, e.g. gce-staging",
		"BOOT_POLL_SLEEP":       fmt.Sprintf("sleep interval between polling server for instance ready status (default %v)", defaultCloudBrainBootPollSleep),
		"BOOT_PRE_POLL_SLEEP":   fmt.Sprintf("time to sleep prior to polling server for instance ready status (default %v)", defaultCloudBrainBootPrePollSleep),
		"IMAGE_SELECTOR_TYPE":   fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultCloudBrainImageSelectorType),
		"IMAGE_SELECTOR_URL":    "URL for image selector API, used only when image selector is \"api\"",
		"IMAGE_[ALIAS_]{ALIAS}": "full name for a given alias given via IMAGE_ALIASES, where the alias form in the key is uppercased and normalized by replacing non-alphanumerics with _",
		"UPLOAD_RETRIES":        fmt.Sprintf("number of times to attempt to upload script before erroring (default %d)", defaultCloudBrainUploadRetries),
		"UPLOAD_RETRY_SLEEP":    fmt.Sprintf("sleep interval between script upload attempts (default %v)", defaultCloudBrainUploadRetrySleep),
	}

	errCloudBrainMissingIPAddressError   = fmt.Errorf("no IP address found")
	errCloudBrainInstanceDeletionNotDone = fmt.Errorf("instance deletion not done")
)

func init() {
	Register("cloudbrain", "CloudBrain", cbHelp, newCloudBrainProvider)
}

type cbProvider struct {
	client *cbClient
	ic     *cbInstanceConfig
	cfg    *config.ProviderConfig

	provider string

	imageSelectorType string
	imageSelector     image.Selector
	bootPollSleep     time.Duration
	bootPrePollSleep  time.Duration
	uploadRetries     uint64
	uploadRetrySleep  time.Duration

	rateLimiter         ratelimit.RateLimiter
	rateLimitMaxCalls   uint64
	rateLimitDuration   time.Duration
	rateLimitQueueDepth uint64
}

type cbInstanceConfig struct {
	SSHKeySigner ssh.Signer
	SSHPubKey    string
	PublicIP     bool
}

type cbStartMultistepWrapper struct {
	f func(*cbStartContext) multistep.StepAction
	c *cbStartContext
}

func (gsmw *cbStartMultistepWrapper) Run(multistep.StateBag) multistep.StepAction {
	return gsmw.f(gsmw.c)
}

func (gsmw *cbStartMultistepWrapper) Cleanup(multistep.StateBag) { return }

type cbStartContext struct {
	startAttributes *StartAttributes
	ctx             gocontext.Context
	instChan        chan Instance
	errChan         chan error
	image           string
	script          string
	bootStart       time.Time
	instance        *cbInstanceData
}

type cbInstance struct {
	client   *cbClient
	provider *cbProvider
	instance *cbInstanceData
	ic       *cbInstanceConfig

	authUser     string
	cachedIPAddr string

	imageName string

	startupDuration time.Duration
}

type cbInstanceStopContext struct {
	ctx     gocontext.Context
	errChan chan error
}

type cbInstanceStopMultistepWrapper struct {
	f func(*cbInstanceStopContext) multistep.StepAction
	c *cbInstanceStopContext
}

func (gismw *cbInstanceStopMultistepWrapper) Run(multistep.StateBag) multistep.StepAction {
	return gismw.f(gismw.c)
}

func (gismw *cbInstanceStopMultistepWrapper) Cleanup(multistep.StateBag) { return }

func buildCloudBrainClient(baseURL *url.URL, provider string) (*cbClient, error) {
	client := &cbClient{
		baseURL:    baseURL,
		provider:   provider,
		httpClient: http.DefaultClient,
	}
	return client, nil
}

func newCloudBrainProvider(cfg *config.ProviderConfig) (Provider, error) {
	var (
		imageSelector image.Selector
		err           error
	)

	if !cfg.IsSet("ENDPOINT") {
		return nil, ErrMissingEndpointConfig
	}

	baseURL, err := url.Parse(cfg.Get("ENDPOINT"))
	if err != nil {
		return nil, errors.Wrap(err, "error parsing Jupiter Brain endpoint URL")
	}

	if !cfg.IsSet("PROVIDER") {
		return nil, fmt.Errorf("missing PROVIDER")
	}

	provider := cfg.Get("PROVIDER")

	client, err := buildCloudBrainClient(baseURL, provider)
	if err != nil {
		return nil, err
	}

	bootPollSleep := defaultCloudBrainBootPollSleep
	if cfg.IsSet("BOOT_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("BOOT_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
		bootPollSleep = si
	}

	bootPrePollSleep := defaultCloudBrainBootPrePollSleep
	if cfg.IsSet("BOOT_PRE_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("BOOT_PRE_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
		bootPrePollSleep = si
	}

	uploadRetries := defaultCloudBrainUploadRetries
	if cfg.IsSet("UPLOAD_RETRIES") {
		ur, err := strconv.ParseUint(cfg.Get("UPLOAD_RETRIES"), 10, 64)
		if err != nil {
			return nil, err
		}
		uploadRetries = ur
	}

	uploadRetrySleep := defaultCloudBrainUploadRetrySleep
	if cfg.IsSet("UPLOAD_RETRY_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("UPLOAD_RETRY_SLEEP"))
		if err != nil {
			return nil, err
		}
		uploadRetrySleep = si
	}

	imageSelectorType := defaultCloudBrainImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	if imageSelectorType != "env" && imageSelectorType != "api" {
		return nil, fmt.Errorf("invalid image selector type %q", imageSelectorType)
	}

	if imageSelectorType == "env" || imageSelectorType == "api" {
		imageSelector, err = buildCloudBrainImageSelector(imageSelectorType, cfg)
		if err != nil {
			return nil, err
		}
	}

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	pubKey, err := ssh.NewPublicKey(&privKey.PublicKey)
	if err != nil {
		return nil, err
	}

	sshKeySigner, err := ssh.NewSignerFromKey(privKey)

	if err != nil {
		return nil, err
	}

	publicIP := true
	if cfg.IsSet("PUBLIC_IP") {
		publicIP = asBool(cfg.Get("PUBLIC_IP"))
	}

	return &cbProvider{
		client: client,
		cfg:    cfg,

		ic: &cbInstanceConfig{
			PublicIP:     publicIP,
			SSHKeySigner: sshKeySigner,
			SSHPubKey:    string(ssh.MarshalAuthorizedKey(pubKey)),
		},

		provider:          provider,
		imageSelector:     imageSelector,
		imageSelectorType: imageSelectorType,
		bootPollSleep:     bootPollSleep,
		bootPrePollSleep:  bootPrePollSleep,
		uploadRetries:     uploadRetries,
		uploadRetrySleep:  uploadRetrySleep,
	}, nil
}

func (p *cbProvider) Setup(ctx gocontext.Context) error {
	return nil
}

func (p *cbProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	logger := context.LoggerFromContext(ctx)

	state := &multistep.BasicStateBag{}

	c := &cbStartContext{
		startAttributes: startAttributes,
		ctx:             ctx,
		instChan:        make(chan Instance),
		errChan:         make(chan error),
	}

	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&cbStartMultistepWrapper{c: c, f: p.stepGetImage},
			&cbStartMultistepWrapper{c: c, f: p.stepInsertInstance},
			&cbStartMultistepWrapper{c: c, f: p.stepWaitForInstanceIP},
		},
	}

	abandonedStart := false

	defer func(c *cbStartContext) {
		if c.instance != nil && abandonedStart {
			_, _ = p.client.Delete(c.instance.ID)
		}
	}(c)

	logger.Info("starting instance")
	go runner.Run(state)

	logger.Debug("selecting over instance, error, and done channels")
	select {
	case inst := <-c.instChan:
		return inst, nil
	case err := <-c.errChan:
		abandonedStart = true
		return nil, err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.cloudbrain.boot.timeout")
		}
		abandonedStart = true
		return nil, ctx.Err()
	}
}

func (p *cbProvider) stepGetImage(c *cbStartContext) multistep.StepAction {
	image, err := p.imageSelect(c.ctx, c.startAttributes)
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.image = image
	return multistep.ActionContinue
}

func (p *cbProvider) stepInsertInstance(c *cbStartContext) multistep.StepAction {
	instRequest := &cbInstanceRequest{
		Provider:     p.provider,
		Image:        c.image,
		InstanceType: c.startAttributes.VMType,
	}

	context.LoggerFromContext(c.ctx).WithFields(logrus.Fields{
		"request": instRequest,
	}).Debug("creating instance")

	c.bootStart = time.Now().UTC()

	inst, err := p.client.Create(instRequest)
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.instance = inst
	return multistep.ActionContinue
}

func (p *cbProvider) stepWaitForInstanceIP(c *cbStartContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx)

	logger.WithFields(logrus.Fields{
		"duration": p.bootPrePollSleep,
	}).Debug("sleeping before first checking instance insert operation")

	time.Sleep(p.bootPrePollSleep)

	for {
		metrics.Mark("worker.vm.provider.cloudbrain.boot.poll")

		instance, err := p.client.Get(c.instance.ID)
		if err != nil {
			c.errChan <- err
			return multistep.ActionHalt
		}

		if instance.State == "running" {
			logger.WithFields(logrus.Fields{
				"state": instance.State,
				"id":    instance.ID,
			}).Debug("instance is ready")

			c.instChan <- &cbInstance{
				client:   p.client,
				provider: p,
				instance: c.instance,
				ic:       p.ic,

				authUser: "travis",

				imageName: c.image,

				startupDuration: time.Now().UTC().Sub(c.bootStart),
			}
			return multistep.ActionContinue
		}

		if instance.State == "errored" {
			logger.WithFields(logrus.Fields{
				"id":           c.instance.ID,
				"error_reason": c.instance.ErrorReason,
			}).Errorf("encountered an error while waiting for instance insert operation: %v", c.instance.ErrorReason)

			c.errChan <- fmt.Errorf("encountered an error while waiting for instance insert operation: %v", c.instance.ErrorReason)
			return multistep.ActionHalt
		}

		logger.WithFields(logrus.Fields{
			"status":   instance.State,
			"id":       c.instance.ID,
			"duration": p.bootPollSleep,
		}).Debug("sleeping before checking instance insert operation")

		time.Sleep(p.bootPollSleep)
	}
}

func (p *cbProvider) imageSelect(ctx gocontext.Context, startAttributes *StartAttributes) (string, error) {
	jobID, _ := context.JobIDFromContext(ctx)
	repo, _ := context.RepositoryFromContext(ctx)

	imageName, err := p.imageSelector.Select(&image.Params{
		Infra:    "cb",
		Language: startAttributes.Language,
		OsxImage: startAttributes.OsxImage,
		Dist:     startAttributes.Dist,
		Group:    startAttributes.Group,
		OS:       startAttributes.OS,
		JobID:    jobID,
		Repo:     repo,
	})

	if err != nil {
		return "", err
	}

	return imageName, nil
}

func buildCloudBrainImageSelector(selectorType string, cfg *config.ProviderConfig) (image.Selector, error) {
	switch selectorType {
	case "env":
		return image.NewEnvSelector(cfg)
	case "api":
		baseURL, err := url.Parse(cfg.Get("IMAGE_SELECTOR_URL"))
		if err != nil {
			return nil, err
		}
		return image.NewAPISelector(baseURL), nil
	default:
		return nil, fmt.Errorf("invalid image selector type %q", selectorType)
	}
}

func (i *cbInstance) sshClient(ctx gocontext.Context) (*ssh.Client, error) {
	if i.cachedIPAddr == "" {
		err := i.refreshInstance(ctx)
		if err != nil {
			return nil, err
		}

		ipAddr := i.getIP()
		if ipAddr == "" {
			return nil, errCloudBrainMissingIPAddressError
		}

		i.cachedIPAddr = ipAddr
	}

	return ssh.Dial("tcp", fmt.Sprintf("%s:22", i.cachedIPAddr), &ssh.ClientConfig{
		User: i.authUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(i.ic.SSHKeySigner),
		},
	})
}

func (i *cbInstance) getIP() string {
	return i.instance.IPAddress
}

func (i *cbInstance) refreshInstance(ctx gocontext.Context) error {
	inst, err := i.client.Get(i.instance.ID)
	if err != nil {
		return err
	}

	i.instance = inst
	return nil
}

func (i *cbInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	uploadedChan := make(chan error)
	var lastErr error

	go func() {
		var errCount uint64
		for {
			if ctx.Err() != nil {
				return
			}

			err := i.uploadScriptAttempt(ctx, script)
			if err == nil {
				uploadedChan <- nil
				return
			}

			lastErr = err

			errCount++
			if errCount > i.provider.uploadRetries {
				uploadedChan <- err
				return
			}

			time.Sleep(i.provider.uploadRetrySleep)
		}
	}()

	select {
	case err := <-uploadedChan:
		return err
	case <-ctx.Done():
		context.LoggerFromContext(ctx).WithField("err", lastErr).Info("stopping upload retries, error from last attempt")
		return ctx.Err()
	}
}

func (i *cbInstance) uploadScriptAttempt(ctx gocontext.Context, script []byte) error {
	client, err := i.sshClient(ctx)
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

	if _, err := f.Write(script); err != nil {
		return err
	}

	return nil
}

func (i *cbInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	client, err := i.sshClient(ctx)
	if err != nil {
		return &RunResult{Completed: false}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return &RunResult{Completed: false}, err
	}
	defer session.Close()

	err = session.RequestPty("xterm", 40, 80, ssh.TerminalModes{})
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Run("bash ~/build.sh")

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

func (i *cbInstance) Stop(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx)
	state := &multistep.BasicStateBag{}

	c := &cbInstanceStopContext{
		ctx:     ctx,
		errChan: make(chan error),
	}

	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&cbInstanceStopMultistepWrapper{c: c, f: i.stepDeleteInstance},
			&cbInstanceStopMultistepWrapper{c: c, f: i.stepWaitForInstanceDeleted},
		},
	}

	logger.WithField("instance", i.instance.ID).Info("deleting instance")
	go runner.Run(state)

	logger.Debug("selecting over error and done channels")
	select {
	case err := <-c.errChan:
		return err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.cloudbrain.delete.timeout")
		}
		return ctx.Err()
	}
}

func (i *cbInstance) stepDeleteInstance(c *cbInstanceStopContext) multistep.StepAction {
	_, err := i.client.Get(i.instance.ID)
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *cbInstance) stepWaitForInstanceDeleted(c *cbInstanceStopContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx)

	logger.Debug("skipping instance deletion polling")
	c.errChan <- nil
	return multistep.ActionContinue
}

func (i *cbInstance) ID() string {
	return fmt.Sprintf("%s:%s", i.instance.ID, i.imageName)
}

func (i *cbInstance) StartupDuration() time.Duration {
	return i.startupDuration
}
