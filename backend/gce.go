package backend

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/cenkalti/backoff"
	"github.com/mitchellh/multistep"
	"github.com/pborman/uuid"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ratelimit"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

const (
	defaultGCEZone               = "us-central1-a"
	defaultGCEMachineType        = "n1-standard-2"
	defaultGCEPremiumMachineType = "n1-standard-4"
	defaultGCENetwork            = "default"
	defaultGCEDiskSize           = int64(20)
	defaultGCELanguage           = "minimal"
	defaultGCEBootPollSleep      = 3 * time.Second
	defaultGCEBootPrePollSleep   = 15 * time.Second
	defaultGCEStopPollSleep      = 3 * time.Second
	defaultGCEStopPrePollSleep   = 15 * time.Second
	defaultGCEUploadRetries      = uint64(120)
	defaultGCEUploadRetrySleep   = 1 * time.Second
	defaultGCEImageSelectorType  = "env"
	defaultGCEImage              = "travis-ci.+"
	defaultGCERateLimitMaxCalls  = uint64(10)
	defaultGCERateLimitDuration  = time.Second
)

var (
	gceHelp = map[string]string{
		"ACCOUNT_JSON":          "[REQUIRED] account JSON config",
		"AUTO_IMPLODE":          "schedule a poweroff at HARD_TIMEOUT_MINUTES in the future (default true)",
		"BOOT_POLL_SLEEP":       fmt.Sprintf("sleep interval between polling server for instance ready status (default %v)", defaultGCEBootPollSleep),
		"BOOT_PRE_POLL_SLEEP":   fmt.Sprintf("time to sleep prior to polling server for instance ready status (default %v)", defaultGCEBootPrePollSleep),
		"DEFAULT_LANGUAGE":      fmt.Sprintf("default language to use when looking up image (default %q)", defaultGCELanguage),
		"DISK_SIZE":             fmt.Sprintf("disk size in GB (default %v)", defaultGCEDiskSize),
		"IMAGE_ALIASES":         "comma-delimited strings used as stable names for images, used only when image selector type is \"env\"",
		"IMAGE_DEFAULT":         fmt.Sprintf("default image name to use when none found (default %q)", defaultGCEImage),
		"IMAGE_SELECTOR_TYPE":   fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultGCEImageSelectorType),
		"IMAGE_SELECTOR_URL":    "URL for image selector API, used only when image selector is \"api\"",
		"IMAGE_[ALIAS_]{ALIAS}": "full name for a given alias given via IMAGE_ALIASES, where the alias form in the key is uppercased and normalized by replacing non-alphanumerics with _",
		"MACHINE_TYPE":          fmt.Sprintf("machine name (default %q)", defaultGCEMachineType),
		"NETWORK":               fmt.Sprintf("network name (default %q)", defaultGCENetwork),
		"PREEMPTIBLE":           "boot job instances with preemptible flag enabled (default true)",
		"PREMIUM_MACHINE_TYPE":  fmt.Sprintf("premium machine type (default %q)", defaultGCEPremiumMachineType),
		"PROJECT_ID":            "[REQUIRED] GCE project id",
		"IMAGE_PROJECT_ID":      "GCE project id to use for images, will use PROJECT_ID if not specified",
		"RATE_LIMIT_PREFIX":     "prefix for the rate limit key in Redis",
		"RATE_LIMIT_REDIS_URL":  "URL to Redis instance to use for rate limiting",
		"RATE_LIMIT_MAX_CALLS":  fmt.Sprintf("number of calls per duration to let through to the GCE API (default %d)", defaultGCERateLimitMaxCalls),
		"RATE_LIMIT_DURATION":   fmt.Sprintf("interval in which to let max-calls through to the GCE API (default %v)", defaultGCERateLimitDuration),
		"SKIP_STOP_POLL":        "immediately return after issuing first instance deletion request (default false)",
		"STOP_POLL_SLEEP":       fmt.Sprintf("sleep interval between polling server for instance stop status (default %v)", defaultGCEStopPollSleep),
		"STOP_PRE_POLL_SLEEP":   fmt.Sprintf("time to sleep prior to polling server for instance stop status (default %v)", defaultGCEStopPrePollSleep),
		"UPLOAD_RETRIES":        fmt.Sprintf("number of times to attempt to upload script before erroring (default %d)", defaultGCEUploadRetries),
		"UPLOAD_RETRY_SLEEP":    fmt.Sprintf("sleep interval between script upload attempts (default %v)", defaultGCEUploadRetrySleep),
		"ZONE":                  fmt.Sprintf("zone name (default %q)", defaultGCEZone),
	}

	errGCEMissingIPAddressError   = fmt.Errorf("no IP address found")
	errGCEInstanceDeletionNotDone = fmt.Errorf("instance deletion not done")

	gceStartupScript = template.Must(template.New("gce-startup").Parse(`#!/usr/bin/env bash
{{ if .AutoImplode }}echo poweroff | at now + {{ .HardTimeoutMinutes }} minutes{{ end }}
cat > ~travis/.ssh/authorized_keys <<EOF
{{ .SSHPubKey }}
EOF
`))

	// FIXME: get rid of the need for this global goop
	gceCustomHTTPTransport     http.RoundTripper = nil
	gceCustomHTTPTransportLock sync.Mutex
)

type gceStartupScriptData struct {
	AutoImplode        bool
	HardTimeoutMinutes int64
	SSHPubKey          string
}

func init() {
	Register("gce", "Google Compute Engine", gceHelp, newGCEProvider)
}

type gceOpError struct {
	Err *compute.OperationError
}

func (oe *gceOpError) Error() string {
	errStrs := []string{}
	for _, err := range oe.Err.Errors {
		errStrs = append(errStrs, fmt.Sprintf("code=%s location=%s message=%s",
			err.Code, err.Location, err.Message))
	}

	return strings.Join(errStrs, ", ")
}

type gceAccountJSON struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

type gceProvider struct {
	client         *compute.Service
	projectID      string
	imageProjectID string
	ic             *gceInstanceConfig
	cfg            *config.ProviderConfig

	imageSelectorType string
	imageSelector     image.Selector
	bootPollSleep     time.Duration
	bootPrePollSleep  time.Duration
	defaultLanguage   string
	defaultImage      string
	uploadRetries     uint64
	uploadRetrySleep  time.Duration

	rateLimiter         ratelimit.RateLimiter
	rateLimitMaxCalls   uint64
	rateLimitDuration   time.Duration
	rateLimitQueueDepth uint64
}

type gceInstanceConfig struct {
	MachineType        *compute.MachineType
	PremiumMachineType *compute.MachineType
	Zone               *compute.Zone
	Network            *compute.Network
	Subnetwork         *compute.Subnetwork
	DiskType           string
	DiskSize           int64
	SSHKeySigner       ssh.Signer
	SSHPubKey          string
	AutoImplode        bool
	HardTimeoutMinutes int64
	StopPollSleep      time.Duration
	StopPrePollSleep   time.Duration
	SkipStopPoll       bool
	Preemptible        bool
	PublicIP           bool
}

type gceStartMultistepWrapper struct {
	f func(*gceStartContext) multistep.StepAction
	c *gceStartContext
}

func (gsmw *gceStartMultistepWrapper) Run(multistep.StateBag) multistep.StepAction {
	return gsmw.f(gsmw.c)
}

func (gsmw *gceStartMultistepWrapper) Cleanup(multistep.StateBag) { return }

type gceStartContext struct {
	startAttributes  *StartAttributes
	ctx              gocontext.Context
	instChan         chan Instance
	errChan          chan error
	image            *compute.Image
	script           string
	bootStart        time.Time
	instance         *compute.Instance
	instanceInsertOp *compute.Operation
}

type gceInstance struct {
	client   *compute.Service
	provider *gceProvider
	instance *compute.Instance
	ic       *gceInstanceConfig

	authUser     string
	cachedIPAddr string

	projectID string
	imageName string

	startupDuration time.Duration
}

type gceInstanceStopContext struct {
	ctx              gocontext.Context
	errChan          chan error
	instanceDeleteOp *compute.Operation
}

type gceInstanceStopMultistepWrapper struct {
	f func(*gceInstanceStopContext) multistep.StepAction
	c *gceInstanceStopContext
}

func (gismw *gceInstanceStopMultistepWrapper) Run(multistep.StateBag) multistep.StepAction {
	return gismw.f(gismw.c)
}

func (gismw *gceInstanceStopMultistepWrapper) Cleanup(multistep.StateBag) { return }

func newGCEProvider(cfg *config.ProviderConfig) (Provider, error) {
	var (
		imageSelector image.Selector
		err           error
	)

	client, err := buildGoogleComputeService(cfg)
	if err != nil {
		return nil, err
	}

	if !cfg.IsSet("PROJECT_ID") {
		return nil, fmt.Errorf("missing PROJECT_ID")
	}

	projectID := cfg.Get("PROJECT_ID")
	imageProjectID := cfg.Get("PROJECT_ID")

	if cfg.IsSet("IMAGE_PROJECT_ID") {
		imageProjectID = cfg.Get("IMAGE_PROJECT_ID")
	}

	zoneName := defaultGCEZone
	if cfg.IsSet("ZONE") {
		zoneName = cfg.Get("ZONE")
	}

	cfg.Set("ZONE", zoneName)

	mtName := defaultGCEMachineType
	if cfg.IsSet("MACHINE_TYPE") {
		mtName = cfg.Get("MACHINE_TYPE")
	}

	cfg.Set("MACHINE_TYPE", mtName)

	premiumMTName := defaultGCEPremiumMachineType
	if cfg.IsSet("PREMIUM_MACHINE_TYPE") {
		premiumMTName = cfg.Get("PREMIUM_MACHINE_TYPE")
	}

	cfg.Set("PREMIUM_MACHINE_TYPE", premiumMTName)

	nwName := defaultGCENetwork
	if cfg.IsSet("NETWORK") {
		nwName = cfg.Get("NETWORK")
	}

	cfg.Set("NETWORK", nwName)

	diskSize := defaultGCEDiskSize
	if cfg.IsSet("DISK_SIZE") {
		ds, err := strconv.ParseInt(cfg.Get("DISK_SIZE"), 10, 64)
		if err == nil {
			diskSize = ds
		}
	}

	bootPollSleep := defaultGCEBootPollSleep
	if cfg.IsSet("BOOT_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("BOOT_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
		bootPollSleep = si
	}

	bootPrePollSleep := defaultGCEBootPrePollSleep
	if cfg.IsSet("BOOT_PRE_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("BOOT_PRE_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
		bootPrePollSleep = si
	}

	stopPollSleep := defaultGCEStopPollSleep
	if cfg.IsSet("STOP_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("STOP_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
		stopPollSleep = si
	}

	stopPrePollSleep := defaultGCEStopPrePollSleep
	if cfg.IsSet("STOP_PRE_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("STOP_PRE_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
		stopPrePollSleep = si
	}

	skipStopPoll := false
	if cfg.IsSet("SKIP_STOP_POLL") {
		ssp, err := strconv.ParseBool(cfg.Get("SKIP_STOP_POLL"))
		if err != nil {
			return nil, err
		}
		skipStopPoll = ssp
	}

	uploadRetries := defaultGCEUploadRetries
	if cfg.IsSet("UPLOAD_RETRIES") {
		ur, err := strconv.ParseUint(cfg.Get("UPLOAD_RETRIES"), 10, 64)
		if err != nil {
			return nil, err
		}
		uploadRetries = ur
	}

	uploadRetrySleep := defaultGCEUploadRetrySleep
	if cfg.IsSet("UPLOAD_RETRY_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("UPLOAD_RETRY_SLEEP"))
		if err != nil {
			return nil, err
		}
		uploadRetrySleep = si
	}

	defaultLanguage := defaultGCELanguage
	if cfg.IsSet("DEFAULT_LANGUAGE") {
		defaultLanguage = cfg.Get("DEFAULT_LANGUAGE")
	}

	defaultImage := defaultGCEImage
	if cfg.IsSet("IMAGE_DEFAULT") {
		defaultImage = cfg.Get("IMAGE_DEFAULT")
	}

	autoImplode := true
	if cfg.IsSet("AUTO_IMPLODE") {
		ai, err := strconv.ParseBool(cfg.Get("AUTO_IMPLODE"))
		if err != nil {
			return nil, err
		}
		autoImplode = ai
	}

	imageSelectorType := defaultGCEImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	if imageSelectorType != "env" && imageSelectorType != "api" {
		return nil, fmt.Errorf("invalid image selector type %q", imageSelectorType)
	}

	if imageSelectorType == "env" || imageSelectorType == "api" {
		imageSelector, err = buildGCEImageSelector(imageSelectorType, cfg)
		if err != nil {
			return nil, err
		}
	}

	var rateLimiter ratelimit.RateLimiter
	if cfg.IsSet("RATE_LIMIT_REDIS_URL") {
		rateLimiter = ratelimit.NewRateLimiter(cfg.Get("RATE_LIMIT_REDIS_URL"), cfg.Get("RATE_LIMIT_PREFIX"))
	} else {
		rateLimiter = ratelimit.NewNullRateLimiter()
	}

	rateLimitMaxCalls := defaultGCERateLimitMaxCalls
	if cfg.IsSet("RATE_LIMIT_MAX_CALLS") {
		mc, err := strconv.ParseUint(cfg.Get("RATE_LIMIT_MAX_CALLS"), 10, 64)
		if err != nil {
			return nil, err
		}
		rateLimitMaxCalls = mc
	}

	rateLimitDuration := defaultGCERateLimitDuration
	if cfg.IsSet("RATE_LIMIT_DURATION") {
		rld, err := time.ParseDuration(cfg.Get("RATE_LIMIT_DURATION"))
		if err != nil {
			return nil, err
		}
		rateLimitDuration = rld
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

	preemptible := true
	if cfg.IsSet("PREEMPTIBLE") {
		preemptible = asBool(cfg.Get("PREEMPTIBLE"))
	}

	publicIP := true
	if cfg.IsSet("PUBLIC_IP") {
		publicIP = asBool(cfg.Get("PUBLIC_IP"))
	}

	return &gceProvider{
		client:         client,
		projectID:      projectID,
		imageProjectID: imageProjectID,
		cfg:            cfg,

		ic: &gceInstanceConfig{
			Preemptible:      preemptible,
			PublicIP:         publicIP,
			DiskSize:         diskSize,
			SSHKeySigner:     sshKeySigner,
			SSHPubKey:        string(ssh.MarshalAuthorizedKey(pubKey)),
			AutoImplode:      autoImplode,
			StopPollSleep:    stopPollSleep,
			StopPrePollSleep: stopPrePollSleep,
			SkipStopPoll:     skipStopPoll,
		},

		imageSelector:     imageSelector,
		imageSelectorType: imageSelectorType,
		bootPollSleep:     bootPollSleep,
		bootPrePollSleep:  bootPrePollSleep,
		defaultLanguage:   defaultLanguage,
		defaultImage:      defaultImage,
		uploadRetries:     uploadRetries,
		uploadRetrySleep:  uploadRetrySleep,

		rateLimiter:       rateLimiter,
		rateLimitMaxCalls: rateLimitMaxCalls,
		rateLimitDuration: rateLimitDuration,
	}, nil
}

func (p *gceProvider) apiRateLimit(ctx gocontext.Context) error {
	metrics.Gauge("travis.worker.vm.provider.gce.rate-limit.queue", int64(p.rateLimitQueueDepth))
	startWait := time.Now()
	defer metrics.TimeSince("travis.worker.vm.provider.gce.rate-limit", startWait)

	atomic.AddUint64(&p.rateLimitQueueDepth, 1)
	// This decrements the counter, see the docs for atomic.AddUint64
	defer atomic.AddUint64(&p.rateLimitQueueDepth, ^uint64(0))

	errCount := 0

	for {
		ok, err := p.rateLimiter.RateLimit("gce-api", p.rateLimitMaxCalls, p.rateLimitDuration)
		if err != nil {
			errCount++
			if errCount >= 5 {
				context.CaptureError(ctx, err)
				context.LoggerFromContext(ctx).WithField("err", err).Info("rate limiter errored 5 times")
				return err
			}
		} else {
			errCount = 0
		}
		if ok {
			return nil
		}

		// Sleep for up to 1 second
		time.Sleep(time.Millisecond * time.Duration(mathrand.Intn(1000)))
	}
}

func (p *gceProvider) Setup(ctx gocontext.Context) error {
	var err error

	p.apiRateLimit(ctx)
	p.ic.Zone, err = p.client.Zones.Get(p.projectID, p.cfg.Get("ZONE")).Do()
	if err != nil {
		return err
	}

	p.ic.DiskType = fmt.Sprintf("zones/%s/diskTypes/pd-ssd", p.ic.Zone.Name)

	p.apiRateLimit(ctx)
	p.ic.MachineType, err = p.client.MachineTypes.Get(p.projectID, p.ic.Zone.Name, p.cfg.Get("MACHINE_TYPE")).Do()
	if err != nil {
		return err
	}

	p.apiRateLimit(ctx)
	p.ic.PremiumMachineType, err = p.client.MachineTypes.Get(p.projectID, p.ic.Zone.Name, p.cfg.Get("PREMIUM_MACHINE_TYPE")).Do()
	if err != nil {
		return err
	}

	p.apiRateLimit(ctx)
	p.ic.Network, err = p.client.Networks.Get(p.projectID, p.cfg.Get("NETWORK")).Do()
	if err != nil {
		return err
	}

	if p.cfg.IsSet("REGION") && p.cfg.IsSet("SUBNETWORK") {
		p.ic.Subnetwork, err = p.client.Subnetworks.Get(p.projectID, p.cfg.Get("REGION"), p.cfg.Get("SUBNETWORK")).Do()
		if err != nil {
			return err
		}
	}

	return nil
}

func buildGoogleComputeService(cfg *config.ProviderConfig) (*compute.Service, error) {
	if !cfg.IsSet("ACCOUNT_JSON") {
		return nil, fmt.Errorf("missing ACCOUNT_JSON")
	}

	a, err := loadGoogleAccountJSON(cfg.Get("ACCOUNT_JSON"))
	if err != nil {
		return nil, err
	}

	config := jwt.Config{
		Email:      a.ClientEmail,
		PrivateKey: []byte(a.PrivateKey),
		Scopes: []string{
			compute.DevstorageFullControlScope,
			compute.ComputeScope,
		},
		TokenURL: "https://accounts.google.com/o/oauth2/token",
	}

	client := config.Client(oauth2.NoContext)

	if gceCustomHTTPTransport != nil {
		client.Transport = gceCustomHTTPTransport
	}

	return compute.New(client)
}

func loadGoogleAccountJSON(filenameOrJSON string) (*gceAccountJSON, error) {
	var (
		bytes []byte
		err   error
	)

	if strings.HasPrefix(strings.TrimSpace(filenameOrJSON), "{") {
		bytes = []byte(filenameOrJSON)
	} else {
		bytes, err = ioutil.ReadFile(filenameOrJSON)
		if err != nil {
			return nil, err
		}
	}

	a := &gceAccountJSON{}
	err = json.Unmarshal(bytes, a)
	return a, err
}

func (p *gceProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	logger := context.LoggerFromContext(ctx)

	state := &multistep.BasicStateBag{}

	c := &gceStartContext{
		startAttributes: startAttributes,
		ctx:             ctx,
		instChan:        make(chan Instance),
		errChan:         make(chan error),
	}

	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&gceStartMultistepWrapper{c: c, f: p.stepGetImage},
			&gceStartMultistepWrapper{c: c, f: p.stepRenderScript},
			&gceStartMultistepWrapper{c: c, f: p.stepInsertInstance},
			&gceStartMultistepWrapper{c: c, f: p.stepWaitForInstanceIP},
		},
	}

	abandonedStart := false

	defer func(c *gceStartContext) {
		if c.instance != nil && abandonedStart {
			p.apiRateLimit(c.ctx)
			_, _ = p.client.Instances.Delete(p.projectID, p.ic.Zone.Name, c.instance.Name).Do()
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
			metrics.Mark("worker.vm.provider.gce.boot.timeout")
		}
		abandonedStart = true
		return nil, ctx.Err()
	}
}

func (p *gceProvider) stepGetImage(c *gceStartContext) multistep.StepAction {
	image, err := p.imageSelect(c.ctx, c.startAttributes)
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.image = image
	return multistep.ActionContinue
}

func (p *gceProvider) stepRenderScript(c *gceStartContext) multistep.StepAction {
	scriptBuf := bytes.Buffer{}
	scriptData := gceStartupScriptData{
		AutoImplode:        p.ic.AutoImplode,
		HardTimeoutMinutes: int64(c.startAttributes.HardTimeout.Minutes()) + 10,
		SSHPubKey:          p.ic.SSHPubKey,
	}
	err := gceStartupScript.Execute(&scriptBuf, scriptData)
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.script = scriptBuf.String()
	return multistep.ActionContinue
}

func (p *gceProvider) stepInsertInstance(c *gceStartContext) multistep.StepAction {
	inst := p.buildInstance(c.startAttributes, c.image.SelfLink, c.script)

	context.LoggerFromContext(c.ctx).WithFields(logrus.Fields{
		"instance": inst,
	}).Debug("inserting instance")

	c.bootStart = time.Now().UTC()

	p.apiRateLimit(c.ctx)
	op, err := p.client.Instances.Insert(p.projectID, p.ic.Zone.Name, inst).Do()
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.instance = inst
	c.instanceInsertOp = op
	return multistep.ActionContinue
}

func (p *gceProvider) stepWaitForInstanceIP(c *gceStartContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx)

	logger.WithFields(logrus.Fields{
		"duration": p.bootPrePollSleep,
	}).Debug("sleeping before first checking instance insert operation")

	time.Sleep(p.bootPrePollSleep)

	zoneOpCall := p.client.ZoneOperations.Get(p.projectID, p.ic.Zone.Name, c.instanceInsertOp.Name)

	for {
		metrics.Mark("worker.vm.provider.gce.boot.poll")

		p.apiRateLimit(c.ctx)
		newOp, err := zoneOpCall.Do()
		if err != nil {
			c.errChan <- err
			return multistep.ActionHalt
		}

		if newOp.Status == "RUNNING" || newOp.Status == "DONE" {
			if newOp.Error != nil {
				c.errChan <- &gceOpError{Err: newOp.Error}
				return multistep.ActionHalt
			}

			logger.WithFields(logrus.Fields{
				"status": newOp.Status,
				"name":   c.instanceInsertOp.Name,
			}).Debug("instance is ready")

			c.instChan <- &gceInstance{
				client:   p.client,
				provider: p,
				instance: c.instance,
				ic:       p.ic,

				authUser: "travis",

				projectID: p.projectID,
				imageName: c.image.Name,

				startupDuration: time.Now().UTC().Sub(c.bootStart),
			}
			return multistep.ActionContinue
		}

		if newOp.Error != nil {
			logger.WithFields(logrus.Fields{
				"err":  newOp.Error,
				"name": c.instanceInsertOp.Name,
			}).Error("encountered an error while waiting for instance insert operation")

			c.errChan <- &gceOpError{Err: newOp.Error}
			return multistep.ActionHalt
		}

		logger.WithFields(logrus.Fields{
			"status":   newOp.Status,
			"name":     c.instanceInsertOp.Name,
			"duration": p.bootPollSleep,
		}).Debug("sleeping before checking instance insert operation")

		time.Sleep(p.bootPollSleep)
	}
}

func (p *gceProvider) imageByFilter(ctx gocontext.Context, filter string) (*compute.Image, error) {
	p.apiRateLimit(ctx)
	// TODO: add some TTL cache in here maybe?
	images, err := p.client.Images.List(p.imageProjectID).Filter(filter).Do()
	if err != nil {
		return nil, err
	}

	if len(images.Items) == 0 {
		return nil, fmt.Errorf("no image found with filter %s", filter)
	}

	imagesByName := map[string]*compute.Image{}
	imageNames := []string{}
	for _, image := range images.Items {
		imagesByName[image.Name] = image
		imageNames = append(imageNames, image.Name)
	}

	sort.Strings(imageNames)

	return imagesByName[imageNames[len(imageNames)-1]], nil
}

func (p *gceProvider) imageSelect(ctx gocontext.Context, startAttributes *StartAttributes) (*compute.Image, error) {
	jobID, _ := context.JobIDFromContext(ctx)
	repo, _ := context.RepositoryFromContext(ctx)

	imageName, err := p.imageSelector.Select(&image.Params{
		Infra:    "gce",
		Language: startAttributes.Language,
		OsxImage: startAttributes.OsxImage,
		Dist:     startAttributes.Dist,
		Group:    startAttributes.Group,
		OS:       startAttributes.OS,
		JobID:    jobID,
		Repo:     repo,
	})

	if err != nil {
		return nil, err
	}

	if imageName == "default" {
		imageName = p.defaultImage
	}

	return p.imageByFilter(ctx, fmt.Sprintf("name eq ^%s", imageName))
}

func buildGCEImageSelector(selectorType string, cfg *config.ProviderConfig) (image.Selector, error) {
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

func (p *gceProvider) buildInstance(startAttributes *StartAttributes, imageLink, startupScript string) *compute.Instance {
	var machineType *compute.MachineType
	switch startAttributes.VMType {
	case "premium":
		machineType = p.ic.PremiumMachineType
	default:
		machineType = p.ic.MachineType
	}

	var subnetwork string
	if p.ic.Subnetwork != nil {
		subnetwork = p.ic.Subnetwork.SelfLink
	}

	var networkInterface *compute.NetworkInterface
	if p.ic.PublicIP {
		networkInterface = &compute.NetworkInterface{
			AccessConfigs: []*compute.AccessConfig{
				&compute.AccessConfig{
					Name: "AccessConfig brought to you by travis-worker",
					Type: "ONE_TO_ONE_NAT",
				},
			},
			Network:    p.ic.Network.SelfLink,
			Subnetwork: subnetwork,
		}
	} else {
		networkInterface = &compute.NetworkInterface{
			Network:    p.ic.Network.SelfLink,
			Subnetwork: subnetwork,
		}
	}

	return &compute.Instance{
		Description: fmt.Sprintf("Travis CI %s test VM", startAttributes.Language),
		Disks: []*compute.AttachedDisk{
			&compute.AttachedDisk{
				Type:       "PERSISTENT",
				Mode:       "READ_WRITE",
				Boot:       true,
				AutoDelete: true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: imageLink,
					DiskType:    p.ic.DiskType,
					DiskSizeGb:  p.ic.DiskSize,
				},
			},
		},
		Scheduling: &compute.Scheduling{
			Preemptible: p.ic.Preemptible,
		},
		MachineType: machineType.SelfLink,
		Name:        fmt.Sprintf("testing-gce-%s", uuid.NewRandom()),
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				&compute.MetadataItems{
					Key:   "startup-script",
					Value: googleapi.String(startupScript),
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			networkInterface,
		},
		ServiceAccounts: []*compute.ServiceAccount{
			&compute.ServiceAccount{
				Email: "default",
				Scopes: []string{
					"https://www.googleapis.com/auth/userinfo.email",
					compute.DevstorageFullControlScope,
					compute.ComputeScope,
				},
			},
		},
		Tags: &compute.Tags{
			Items: []string{
				"testing",
			},
		},
	}
}

func (i *gceInstance) sshClient(ctx gocontext.Context) (*ssh.Client, error) {
	if i.cachedIPAddr == "" {
		err := i.refreshInstance(ctx)
		if err != nil {
			return nil, err
		}

		ipAddr := i.getIP()
		if ipAddr == "" {
			return nil, errGCEMissingIPAddressError
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

func (i *gceInstance) getIP() string {
	// if instance has no public IP, return first private one
	if !i.ic.PublicIP {
		for _, ni := range i.instance.NetworkInterfaces {
			return ni.NetworkIP
		}
	}

	for _, ni := range i.instance.NetworkInterfaces {
		if ni.AccessConfigs == nil {
			continue
		}

		for _, ac := range ni.AccessConfigs {
			if ac.NatIP != "" {
				return ac.NatIP
			}
		}
	}

	// TODO: return an error?
	return ""
}

func (i *gceInstance) refreshInstance(ctx gocontext.Context) error {
	i.provider.apiRateLimit(ctx)
	inst, err := i.client.Instances.Get(i.projectID, i.ic.Zone.Name, i.instance.Name).Do()
	if err != nil {
		return err
	}

	i.instance = inst
	return nil
}

func (i *gceInstance) UploadScript(ctx gocontext.Context, script []byte) error {
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

func (i *gceInstance) uploadScriptAttempt(ctx gocontext.Context, script []byte) error {
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

func (i *gceInstance) isPreempted(ctx gocontext.Context) (bool, error) {
	if !i.ic.Preemptible {
		return false, nil
	}

	listOpCall := i.provider.client.GlobalOperations.AggregatedList(i.provider.projectID).
		Filter(fmt.Sprintf("targetId eq %d", i.instance.Id))

	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 1 * time.Second
	b.MaxElapsedTime = 1 * time.Minute

	var preempted bool
	err := backoff.Retry(func() error {
		i.provider.apiRateLimit(ctx)
		list, err := listOpCall.Do()
		if err != nil {
			return err
		}

		for _, item := range list.Items {
			for _, op := range item.Operations {
				if op.Kind == "compute#operation" && op.OperationType == "compute.instances.preempted" {
					preempted = true
					return nil
				}
			}
		}

		return nil
	}, b)

	return preempted, err
}

func (i *gceInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
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

	preempted, googleErr := i.isPreempted(ctx)
	if googleErr != nil {
		context.LoggerFromContext(ctx).WithField("err", googleErr).Error("couldn't determine if instance was preempted")
		// could not get answer from google
		// requeue just in case
		return &RunResult{Completed: false}, googleErr
	}
	if preempted {
		metrics.Mark("travis.worker.gce.preempted-instances")
		return &RunResult{Completed: false}, nil
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

func (i *gceInstance) Stop(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx)
	state := &multistep.BasicStateBag{}

	c := &gceInstanceStopContext{
		ctx:     ctx,
		errChan: make(chan error),
	}

	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepDeleteInstance},
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepWaitForInstanceDeleted},
		},
	}

	logger.WithField("instance", i.instance.Name).Info("deleting instance")
	go runner.Run(state)

	logger.Debug("selecting over error and done channels")
	select {
	case err := <-c.errChan:
		return err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.gce.delete.timeout")
		}
		return ctx.Err()
	}
}

func (i *gceInstance) stepDeleteInstance(c *gceInstanceStopContext) multistep.StepAction {
	op, err := i.client.Instances.Delete(i.projectID, i.ic.Zone.Name, i.instance.Name).Do()
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.instanceDeleteOp = op
	return multistep.ActionContinue
}

func (i *gceInstance) stepWaitForInstanceDeleted(c *gceInstanceStopContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx)

	if i.ic.SkipStopPoll {
		logger.Debug("skipping instance deletion polling")
		c.errChan <- nil
		return multistep.ActionContinue
	}

	logger.WithFields(logrus.Fields{
		"duration": i.ic.StopPrePollSleep,
	}).Debug("sleeping before first checking instance delete operation")

	time.Sleep(i.ic.StopPrePollSleep)

	zoneOpCall := i.client.ZoneOperations.Get(i.projectID,
		i.ic.Zone.Name, c.instanceDeleteOp.Name)

	b := backoff.NewExponentialBackOff()
	b.InitialInterval = i.ic.StopPollSleep
	b.MaxInterval = 10 * i.ic.StopPollSleep
	b.MaxElapsedTime = 2 * time.Minute

	err := backoff.Retry(func() error {
		i.provider.apiRateLimit(c.ctx)
		newOp, err := zoneOpCall.Do()
		if err != nil {
			return err
		}

		if newOp.Status == "DONE" {
			if newOp.Error != nil {
				return &gceOpError{Err: newOp.Error}
			}

			return nil
		}

		return errGCEInstanceDeletionNotDone
	}, b)

	c.errChan <- err

	if err != nil {
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) ID() string {
	return fmt.Sprintf("%s:%s", i.instance.Name, i.imageName)
}

func (i *gceInstance) StartupDuration() time.Duration {
	return i.startupDuration
}
