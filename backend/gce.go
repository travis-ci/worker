package backend

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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

	gocontext "context"

	"cloud.google.com/go/compute/metadata"
	"github.com/cenk/backoff"
	"github.com/mitchellh/multistep"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ratelimit"
	"github.com/travis-ci/worker/remote"
	"github.com/travis-ci/worker/ssh"
	"github.com/travis-ci/worker/winrm"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	compute "google.golang.org/api/compute/v1"
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
	defaultGCESubnet             = "default"
	defaultGCERegion             = "us-central1"
	defaultGCEUploadRetries      = uint64(120)
	defaultGCEUploadRetrySleep   = 1 * time.Second
	defaultGCEImageSelectorType  = "env"
	defaultGCEImage              = "travis-ci.+"
	defaultGCEGpuCount           = int64(0)
	defaultGCEGpuType            = "nvidia-tesla-p100"

	defaultGCERateLimitMaxCalls         = uint64(10)
	defaultGCERateLimitDuration         = time.Second
	defaultGCERateLimitDynamicConfigTTL = time.Minute

	defaultGCESSHDialTimeout = 5 * time.Second
	defaultGCEWarmerTimeout  = 5 * time.Second
)

var (
	gceHelp = map[string]string{
		"ACCOUNT_JSON":           "[REQUIRED] account JSON config",
		"AUTO_IMPLODE":           "schedule a poweroff at HARD_TIMEOUT_MINUTES in the future (default true)",
		"BOOT_POLL_SLEEP":        fmt.Sprintf("sleep interval between polling server for instance ready status (default %v)", defaultGCEBootPollSleep),
		"BOOT_PRE_POLL_SLEEP":    fmt.Sprintf("time to sleep prior to polling server for instance ready status (default %v)", defaultGCEBootPrePollSleep),
		"DEFAULT_LANGUAGE":       fmt.Sprintf("default language to use when looking up image (default %q)", defaultGCELanguage),
		"DETERMINISTIC_HOSTNAME": "assign deterministic hostname based on repo slug and job id (default false)",
		"DISK_SIZE":              fmt.Sprintf("disk size in GB (default %v)", defaultGCEDiskSize),
		"GPU_COUNT":              fmt.Sprintf("number of GPUs to use (default %v)", defaultGCEGpuCount),
		"GPU_TYPE":               fmt.Sprintf("type of GPU to use (default %q)", defaultGCEGpuType),
		"IMAGE_ALIASES":          "comma-delimited strings used as stable names for images, used only when image selector type is \"env\"",
		"IMAGE_DEFAULT":          fmt.Sprintf("default image name to use when none found (default %q)", defaultGCEImage),
		"IMAGE_SELECTOR_TYPE":    fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultGCEImageSelectorType),
		"IMAGE_SELECTOR_URL":     "URL for image selector API, used only when image selector is \"api\"",
		"IMAGE_[ALIAS_]{ALIAS}":  "full name for a given alias given via IMAGE_ALIASES, where the alias form in the key is uppercased and normalized by replacing non-alphanumerics with _",
		"MACHINE_TYPE":           fmt.Sprintf("machine name (default %q)", defaultGCEMachineType),
		"NETWORK":                fmt.Sprintf("network name (default %q)", defaultGCENetwork),
		"PREEMPTIBLE":            "boot job instances with preemptible flag enabled (default false)",
		"PREMIUM_MACHINE_TYPE":   fmt.Sprintf("premium machine type (default %q)", defaultGCEPremiumMachineType),
		"PROJECT_ID":             "[REQUIRED] GCE project id",
		"PUBLIC_IP":              "boot job instances with a public ip, disable this for NAT (default true)",
		"PUBLIC_IP_CONNECT":      "connect to the public ip of the instance instead of the internal, only takes effect if PUBLIC_IP is true (default true)",
		"IMAGE_PROJECT_ID":       "GCE project id to use for images, will use PROJECT_ID if not specified",

		"RATE_LIMIT_PREFIX":             "prefix for the rate limit key in Redis",
		"RATE_LIMIT_REDIS_URL":          "URL to Redis instance to use for rate limiting",
		"RATE_LIMIT_MAX_CALLS":          fmt.Sprintf("number of calls per duration to let through to the GCE API (default %d)", defaultGCERateLimitMaxCalls),
		"RATE_LIMIT_DURATION":           fmt.Sprintf("interval in which to let max-calls through to the GCE API (default %v)", defaultGCERateLimitDuration),
		"RATE_LIMIT_DYNAMIC_CONFIG":     "get max-calls and duration dynamically through redis (default false)",
		"RATE_LIMIT_DYNAMIC_CONFIG_TTL": fmt.Sprintf("time to cache dynamic config for (default %v)", defaultGCERateLimitDynamicConfigTTL),

		"REGION":                fmt.Sprintf("only takes effect when SUBNETWORK is defined; region in which to deploy (default %v)", defaultGCERegion),
		"SKIP_STOP_POLL":        "immediately return after issuing first instance deletion request (default false)",
		"SSH_DIAL_TIMEOUT":      fmt.Sprintf("connection timeout for ssh connections (default %v)", defaultGCESSHDialTimeout),
		"STOP_POLL_SLEEP":       fmt.Sprintf("sleep interval between polling server for instance stop status (default %v)", defaultGCEStopPollSleep),
		"STOP_PRE_POLL_SLEEP":   fmt.Sprintf("time to sleep prior to polling server for instance stop status (default %v)", defaultGCEStopPrePollSleep),
		"SUBNETWORK":            fmt.Sprintf("the subnetwork in which to launch build instances (gce internal default \"%v\")", defaultGCESubnet),
		"UPLOAD_RETRIES":        fmt.Sprintf("number of times to attempt to upload script before erroring (default %d)", defaultGCEUploadRetries),
		"UPLOAD_RETRY_SLEEP":    fmt.Sprintf("sleep interval between script upload attempts (default %v)", defaultGCEUploadRetrySleep),
		"WARMER_URL":            "URL for warmer service",
		"WARMER_TIMEOUT":        fmt.Sprintf("timeout for requests to warmer service (default %v)", defaultGCEWarmerTimeout),
		"WARMER_SSH_PASSPHRASE": fmt.Sprintf("The passphrase used to decipher instace SSH keys"),
		"ZONE":                  fmt.Sprintf("zone name (default %q)", defaultGCEZone),
	}

	errGCEMissingIPAddressError   = fmt.Errorf("no IP address found")
	errGCEInstanceDeletionNotDone = fmt.Errorf("instance deletion not done")

	gceStartupScript = template.Must(template.New("gce-startup").Parse(`#!/usr/bin/env bash
{{ if .AutoImplode }}echo poweroff | at now + {{ .HardTimeoutMinutes }} minutes{{ end }}
cat > ~travis/.ssh/authorized_keys <<EOF
{{ .SSHPubKey }}
EOF
chown -R travis:travis ~travis/.ssh/
`))

	gceWindowsStartupScript = template.Must(template.New("gce-windows-startup").Parse(`
shutdown -s -t {{ .HardTimeoutSeconds }}
net localgroup administrators travis /add
$pw = '{{ .WindowsPassword }}' | ConvertTo-SecureString -AsPlainText -Force
Set-LocalUser -Name travis -Password $pw
`))

	// FIXME: get rid of the need for this global goop
	gceCustomHTTPTransport     http.RoundTripper
	gceCustomHTTPTransportLock sync.Mutex
)

type gceStartupScriptData struct {
	AutoImplode        bool
	HardTimeoutMinutes int64
	SSHPubKey          string
	HardTimeoutSeconds int64
	WindowsPassword    string
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

	deterministicHostname bool
	imageSelectorType     string
	imageSelector         image.Selector
	imageCache            *sync.Map
	bootPollSleep         time.Duration
	bootPrePollSleep      time.Duration
	defaultLanguage       string
	defaultImage          string
	defaultGpuCount       int64
	defaultGpuType        string
	uploadRetries         uint64
	uploadRetrySleep      time.Duration
	sshDialer             ssh.Dialer
	sshDialTimeout        time.Duration

	rateLimiter         ratelimit.RateLimiter
	rateLimitMaxCalls   uint64
	rateLimitDuration   time.Duration
	rateLimitQueueDepth uint64

	warmerUrl           *url.URL
	warmerTimeout       time.Duration
	warmerSSHPassphrase string
}

type gceInstanceConfig struct {
	MachineType        *compute.MachineType
	PremiumMachineType *compute.MachineType
	Zone               *compute.Zone
	Network            *compute.Network
	Subnetwork         *compute.Subnetwork
	AcceleratorConfig  *compute.AcceleratorConfig
	DiskType           string
	DiskSize           int64
	SSHPubKey          string
	AutoImplode        bool
	HardTimeoutMinutes int64
	StopPollSleep      time.Duration
	StopPrePollSleep   time.Duration
	SkipStopPoll       bool
	Preemptible        bool
	PublicIP           bool
	PublicIPConnect    bool
	Site               string
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
	startAttributes      *StartAttributes
	progresser           Progresser
	ctx                  gocontext.Context
	sshDialer            ssh.Dialer
	instChan             chan Instance
	errChan              chan error
	image                *compute.Image
	script               string
	bootStart            time.Time
	instance             *compute.Instance
	instanceInsertOpName string
	instanceWarmedIP     string
	windowsPassword      string
	zoneName             string
}

type gceInstance struct {
	zoneName string
	client   *compute.Service
	provider *gceProvider
	instance *compute.Instance
	ic       *gceInstanceConfig

	progresser Progresser
	sshDialer  ssh.Dialer

	authUser     string
	cachedIPAddr string

	projectID string
	imageName string

	startupDuration time.Duration
	os              string
	windowsPassword string

	warmed bool
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
		err error
	)

	client, err := buildGoogleComputeService(cfg)
	if err != nil {
		return nil, err
	}

	projectID := cfg.Get("PROJECT_ID")
	if metadata.OnGCE() {
		projectID, err = metadata.ProjectID()
		if err != nil {
			return nil, errors.Wrap(err, "could not get project id from metadata api")
		}
	}
	if projectID == "" {
		return nil, fmt.Errorf("missing PROJECT_ID")
	}

	imageProjectID := projectID
	if cfg.IsSet("IMAGE_PROJECT_ID") {
		imageProjectID = cfg.Get("IMAGE_PROJECT_ID")
	}

	zoneName := defaultGCEZone
	if metadata.OnGCE() {
		zoneName, err = metadata.Zone()
		if err != nil {
			return nil, errors.Wrap(err, "could not get zone from metadata api")
		}
	}
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

	site := ""
	if cfg.IsSet("TRAVIS_SITE") {
		site = cfg.Get("TRAVIS_SITE")
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

	defaultAcceleratorConfig := &compute.AcceleratorConfig{}

	defaultAcceleratorConfig.AcceleratorType = defaultGCEGpuType
	if cfg.IsSet("GPU_TYPE") {
		defaultAcceleratorConfig.AcceleratorType = cfg.Get("GPU_TYPE")
	}

	defaultAcceleratorConfig.AcceleratorCount = defaultGCEGpuCount
	if cfg.IsSet("GPU_COUNT") {
		dgc, err := strconv.ParseInt(cfg.Get("GPU_COUNT"), 0, 64)
		if err != nil {
			return nil, err
		}
		defaultAcceleratorConfig.AcceleratorCount = dgc
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

	imageSelector, err := buildGCEImageSelector(imageSelectorType, cfg)
	if err != nil {
		return nil, err
	}

	rateLimitDynamicConfigTTL := defaultGCERateLimitDynamicConfigTTL
	if cfg.IsSet("RATE_LIMIT_DYNAMIC_CONFIG_TTL") {
		rldcttl, err := time.ParseDuration(cfg.Get("RATE_LIMIT_DYNAMIC_CONFIG_TTL"))
		if err != nil {
			return nil, err
		}
		rateLimitDynamicConfigTTL = rldcttl
	}

	var rateLimiter ratelimit.RateLimiter
	if cfg.IsSet("RATE_LIMIT_REDIS_URL") {
		rateLimiter = ratelimit.NewRateLimiter(
			cfg.Get("RATE_LIMIT_REDIS_URL"),
			cfg.Get("RATE_LIMIT_PREFIX"),
			asBool(cfg.Get("RATE_LIMIT_DYNAMIC_CONFIG")),
			rateLimitDynamicConfigTTL,
		)
	} else {
		rateLimiter = ratelimit.NewNullRateLimiter()
	}

	var warmerUrl *url.URL
	if cfg.IsSet("WARMER_URL") {
		warmerUrl, err = url.Parse(cfg.Get("WARMER_URL"))
		if err != nil {
			return nil, errors.Wrap(err, "could not parse WARMER_URL")
		}
	}

	warmerTimeout := defaultGCEWarmerTimeout
	if cfg.IsSet("WARMER_TIMEOUT") {
		warmerTimeout, err = time.ParseDuration(cfg.Get("WARMER_TIMEOUT"))
		if err != nil {
			return nil, errors.Wrap(err, "could not parse WARMER_TIMEOUT")
		}
	}

	var warmerSSHPassphrase string
	if cfg.IsSet("WARMER_SSH_PASSPHRASE") {
		warmerSSHPassphrase = cfg.Get("WARMER_SSH_PASSPHRASE")
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

	sshDialTimeout := defaultGCESSHDialTimeout
	if cfg.IsSet("SSH_DIAL_TIMEOUT") {
		sshDialTimeout, err = time.ParseDuration(cfg.Get("SSH_DIAL_TIMEOUT"))
		if err != nil {
			return nil, err
		}
	}

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	pubKey, err := ssh.FormatPublicKey(&privKey.PublicKey)
	if err != nil {
		return nil, err
	}

	sshDialer, err := ssh.NewDialerWithKey(privKey)
	if err != nil {
		return nil, err
	}

	preemptible := false
	if cfg.IsSet("PREEMPTIBLE") {
		preemptible = asBool(cfg.Get("PREEMPTIBLE"))
	}

	publicIP := true
	if cfg.IsSet("PUBLIC_IP") {
		publicIP = asBool(cfg.Get("PUBLIC_IP"))
	}

	publicIPConnect := true
	if cfg.IsSet("PUBLIC_IP_CONNECT") {
		publicIPConnect = asBool(cfg.Get("PUBLIC_IP_CONNECT"))
	}

	deterministicHostname := false
	if cfg.IsSet("DETERMINISTIC_HOSTNAME") {
		deterministicHostname = asBool(cfg.Get("DETERMINISTIC_HOSTNAME"))
	}

	return &gceProvider{
		client:         client,
		projectID:      projectID,
		imageProjectID: imageProjectID,
		cfg:            cfg,
		sshDialer:      sshDialer,
		sshDialTimeout: sshDialTimeout,

		ic: &gceInstanceConfig{
			Preemptible:       preemptible,
			PublicIP:          publicIP,
			PublicIPConnect:   publicIPConnect,
			DiskSize:          diskSize,
			SSHPubKey:         string(pubKey),
			AutoImplode:       autoImplode,
			StopPollSleep:     stopPollSleep,
			StopPrePollSleep:  stopPrePollSleep,
			SkipStopPoll:      skipStopPoll,
			Site:              site,
			AcceleratorConfig: defaultAcceleratorConfig,
		},

		deterministicHostname: deterministicHostname,
		imageSelector:         imageSelector,
		imageSelectorType:     imageSelectorType,
		imageCache:            &sync.Map{},
		bootPollSleep:         bootPollSleep,
		bootPrePollSleep:      bootPrePollSleep,
		defaultLanguage:       defaultLanguage,
		defaultImage:          defaultImage,
		uploadRetries:         uploadRetries,
		uploadRetrySleep:      uploadRetrySleep,

		rateLimiter:       rateLimiter,
		rateLimitMaxCalls: rateLimitMaxCalls,
		rateLimitDuration: rateLimitDuration,

		warmerUrl:           warmerUrl,
		warmerTimeout:       warmerTimeout,
		warmerSSHPassphrase: warmerSSHPassphrase,
	}, nil
}

func (p *gceProvider) apiRateLimit(ctx gocontext.Context) error {
	if trace.FromContext(ctx) != nil {
		var span *trace.Span
		ctx, span = trace.StartSpan(ctx, "apiRateLimit")
		defer span.End()
	}

	metrics.Mark("travis.worker.vm.provider.gce.rate-limit.start")

	metrics.Gauge("travis.worker.vm.provider.gce.rate-limit.queue", int64(p.rateLimitQueueDepth))
	startWait := time.Now()
	defer metrics.TimeSince("travis.worker.vm.provider.gce.rate-limit", startWait)

	defer context.TimeSince(ctx, "gce_api_rate_limit", time.Now())

	atomic.AddUint64(&p.rateLimitQueueDepth, 1)
	// This decrements the counter, see the docs for atomic.AddUint64
	defer atomic.AddUint64(&p.rateLimitQueueDepth, ^uint64(0))

	errCount := 0

	for {
		ok, err := p.rateLimiter.RateLimit(ctx, "gce-api", p.rateLimitMaxCalls, p.rateLimitDuration)
		if err != nil {
			errCount++
			if errCount >= 5 {
				context.CaptureError(ctx, err)
				context.LoggerFromContext(ctx).WithFields(logrus.Fields{
					"err":  err,
					"self": "backend/gce_provider",
				}).Info("rate limiter errored 5 times")
				return err
			}
		} else {
			errCount = 0
		}
		if ok {
			return nil
		}

		// Sleep for up to 1 second
		var span *trace.Span
		if trace.FromContext(ctx) != nil {
			_, span = trace.StartSpan(ctx, "GCE.timeSleep.apiRateLimit")
		}

		time.Sleep(time.Millisecond * time.Duration(mathrand.Intn(1000)))

		if trace.FromContext(ctx) != nil {
			span.End()
		}
	}
}

func (p *gceProvider) Setup(ctx gocontext.Context) error {
	var err error

	p.apiRateLimit(ctx)
	p.ic.Zone, err = p.client.Zones.Get(p.projectID, p.cfg.Get("ZONE")).Context(ctx).Do()
	if err != nil {
		return err
	}

	p.ic.DiskType = fmt.Sprintf("zones/%s/diskTypes/pd-ssd", p.ic.Zone.Name)

	p.apiRateLimit(ctx)
	p.ic.MachineType, err = p.client.MachineTypes.Get(p.projectID, p.ic.Zone.Name, p.cfg.Get("MACHINE_TYPE")).Context(ctx).Do()
	if err != nil {
		return err
	}

	p.apiRateLimit(ctx)
	p.ic.PremiumMachineType, err = p.client.MachineTypes.Get(p.projectID, p.ic.Zone.Name, p.cfg.Get("PREMIUM_MACHINE_TYPE")).Context(ctx).Do()
	if err != nil {
		return err
	}

	p.apiRateLimit(ctx)
	p.ic.Network, err = p.client.Networks.Get(p.projectID, p.cfg.Get("NETWORK")).Context(ctx).Do()
	if err != nil {
		return err
	}

	region := defaultGCERegion
	if metadata.OnGCE() {
		region = p.ic.Zone.Region
	}
	if p.cfg.IsSet("REGION") {
		region = p.cfg.Get("REGION")
	}

	if p.cfg.IsSet("SUBNETWORK") {
		p.apiRateLimit(ctx)
		p.ic.Subnetwork, err = p.client.Subnetworks.Get(p.projectID, region, p.cfg.Get("SUBNETWORK")).Context(ctx).Do()
		if err != nil {
			return err
		}
	}

	return nil
}

type MetricsTransport struct {
	Name      string
	Transport http.RoundTripper
}

func (m *MetricsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	metrics.Mark(m.Name)
	return m.Transport.RoundTrip(req)
}

func buildGoogleComputeService(cfg *config.ProviderConfig) (*compute.Service, error) {
	ctx := gocontext.WithValue(gocontext.Background(), oauth2.HTTPClient, &http.Client{
		Transport: &MetricsTransport{
			Name:      "worker.google.compute.api.client",
			Transport: &ochttp.Transport{},
		},
	})

	if !cfg.IsSet("ACCOUNT_JSON") {
		client, err := google.DefaultClient(ctx, compute.DevstorageFullControlScope, compute.ComputeScope)
		if err != nil {
			return nil, errors.Wrap(err, "could not build default client")
		}
		return compute.New(client)
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

	client := config.Client(ctx)

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

func (p *gceProvider) SupportsProgress() bool {
	return true
}

func (p *gceProvider) StartWithProgress(ctx gocontext.Context, startAttributes *StartAttributes, progresser Progresser) (Instance, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_provider")

	var (
		zone *compute.Zone
		err  error
	)
	zone = p.ic.Zone
	if startAttributes.VMConfig.Zone != "" {
		logger.WithField("zone", startAttributes.VMConfig.Zone).Debug("setting zone from vm config")

		p.apiRateLimit(ctx)
		zone, err = p.client.Zones.Get(p.projectID, startAttributes.VMConfig.Zone).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
	}

	state := &multistep.BasicStateBag{}

	wp, err := makeWindowsPassword()
	if err != nil {
		return nil, err
	}
	c := &gceStartContext{
		startAttributes: startAttributes,
		zoneName:        zone.Name,
		progresser:      progresser,
		sshDialer:       p.sshDialer,
		ctx:             ctx,
		instChan:        make(chan Instance),
		errChan:         make(chan error),
		windowsPassword: wp,
	}

	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&gceStartMultistepWrapper{c: c, f: p.stepGetImage},
			&gceStartMultistepWrapper{c: c, f: p.stepRenderScript},
			&gceStartMultistepWrapper{c: c, f: p.stepInsertInstance},
			&gceStartMultistepWrapper{c: c, f: p.stepWaitForInstanceIP},
		},
	}

	go runner.Run(state)

	logger.Debug("selecting over instance, error, and done channels")
	select {
	case inst := <-c.instChan:
		return inst, nil
	case err := <-c.errChan:
		return nil, err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.gce.boot.timeout")
		}
		c.progresser.Progress(&ProgressEntry{
			Message:    "timeout waiting for instance to be ready",
			State:      ProgressFailure,
			Interrupts: true,
		})
		return nil, ctx.Err()
	}
}

func (p *gceProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	return p.StartWithProgress(ctx, startAttributes, NewTextProgresser(ioutil.Discard))
}

func (p *gceProvider) stepGetImage(c *gceStartContext) multistep.StepAction {
	ctx := c.ctx

	ctx, span := trace.StartSpan(ctx, "GCE.GetImage")
	defer span.End()

	image, err := p.imageSelect(c.ctx, c.startAttributes)
	if err != nil {
		c.progresser.Progress(&ProgressEntry{
			Message: "could not select image",
			State:   ProgressFailure,
		})
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.image = image
	c.progresser.Progress(&ProgressEntry{
		Message: fmt.Sprintf("selected image %q", image.Name),
		State:   ProgressSuccess,
	})
	return multistep.ActionContinue
}

func makeWindowsPassword() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s?!?!?_:-[", base64.StdEncoding.EncodeToString(b)), nil
}

func (p *gceProvider) stepRenderScript(c *gceStartContext) multistep.StepAction {
	ctx := c.ctx

	ctx, span := trace.StartSpan(ctx, "GCE.RenderScript")
	defer span.End()

	scriptBuf := bytes.Buffer{}
	scriptData := gceStartupScriptData{
		AutoImplode:        p.ic.AutoImplode,
		HardTimeoutMinutes: int64(c.startAttributes.HardTimeout.Minutes()) + 10,
		SSHPubKey:          p.ic.SSHPubKey,
		HardTimeoutSeconds: int64(c.startAttributes.HardTimeout.Seconds()) + 600,
	}

	var err error
	if c.startAttributes.OS == "windows" {
		scriptData.WindowsPassword = c.windowsPassword
		context.LoggerFromContext(c.ctx).WithFields(logrus.Fields{
			"self":             "backend/gce_provider",
			"windows_password": c.windowsPassword,
		}).Debug("rendering startup script with password")
		err = gceWindowsStartupScript.Execute(&scriptBuf, scriptData)
	} else {
		err = gceStartupScript.Execute(&scriptBuf, scriptData)
	}
	if err != nil {
		c.progresser.Progress(&ProgressEntry{
			Message: "could not render startup script",
			State:   ProgressFailure,
		})
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.script = scriptBuf.String()
	c.progresser.Progress(&ProgressEntry{
		Message: "rendered startup script",
		State:   ProgressSuccess,
	})
	return multistep.ActionContinue
}

func (p *gceProvider) stepInsertInstance(c *gceStartContext) multistep.StepAction {
	ctx := c.ctx

	ctx, span := trace.StartSpan(ctx, "GCE.InsertInstance")
	defer span.End()

	logger := context.LoggerFromContext(c.ctx).WithField("self", "backend/gce_provider")

	inst, err := p.buildInstance(c.ctx, c.startAttributes, c.image.SelfLink, c.script)
	if err != nil {
		c.progresser.Progress(&ProgressEntry{
			Message: "could not build instance",
			State:   ProgressFailure,
		})
		c.errChan <- err
		return multistep.ActionHalt
	}

	context.LoggerFromContext(c.ctx).WithFields(logrus.Fields{
		"self":     "backend/gce_provider",
		"instance": inst,
	}).Debug("inserting instance")

	c.bootStart = time.Now().UTC()

	if c.startAttributes.Warmer && p.warmerUrl != nil {
		warmerResponse, err := p.warmerRequestInstance(c.ctx, c.zoneName, inst)
		if err != nil {
			logger.WithError(err).Warn("could not obtain instance from warmer")
		} else {
			// success case
			logger.WithFields(logrus.Fields{
				"ip":   warmerResponse.IP,
				"name": warmerResponse.Name,
				"zone": warmerResponse.Zone,
			}).Info("got instance from warmer")

			inst.Name = warmerResponse.Name
			inst.Zone = warmerResponse.Zone
			c.instance = inst
			c.instanceWarmedIP = warmerResponse.IP
			if p.ic.PublicIPConnect && warmerResponse.PublicIP != "" {
				c.instanceWarmedIP = warmerResponse.PublicIP
			}
			c.progresser.Progress(&ProgressEntry{
				Message: "obtained instance",
				State:   ProgressSuccess,
			})

			// we need to decrypt the instance SSH key in order to create the dialer
			block, _ := pem.Decode([]byte(warmerResponse.SSHPrivateKey))
			if block == nil {
				c.errChan <- errors.Wrap(err, "ssh key does not contain a valid PEM block")
				return multistep.ActionHalt
			}
			der, err := x509.DecryptPEMBlock(block, []byte(p.warmerSSHPassphrase))
			if err != nil {
				c.errChan <- errors.Wrap(err, "couldn't decrypt SSH key")
				return multistep.ActionHalt
			}

			decryptedKey, err := x509.ParsePKCS1PrivateKey(der)
			if err != nil {
				c.errChan <- errors.Wrap(err, "couldn't parse SSH key")
				return multistep.ActionHalt
			}

			sshDialer, err := ssh.NewDialerWithKey(decryptedKey)
			if err != nil {
				c.progresser.Progress(&ProgressEntry{
					Message: "could not create ssh dialer for instance",
					State:   ProgressFailure,
				})
				c.errChan <- err
				return multistep.ActionHalt
			}
			c.sshDialer = sshDialer

			return multistep.ActionContinue
		}
	}

	p.apiRateLimit(c.ctx)
	op, err := p.client.Instances.Insert(p.projectID, c.zoneName, inst).Context(c.ctx).Do()
	if err != nil {
		c.progresser.Progress(&ProgressEntry{
			Message: "could not insert instance",
			State:   ProgressFailure,
		})
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.instance = inst
	c.instanceInsertOpName = op.Name
	c.progresser.Progress(&ProgressEntry{
		Message: "inserted instance",
		State:   ProgressSuccess,
	})
	return multistep.ActionContinue
}

func (p *gceProvider) stepWaitForInstanceIP(c *gceStartContext) multistep.StepAction {
	ctx := c.ctx

	ctx, span := trace.StartSpan(ctx, "GCE.WaitForInstanceIP")
	defer span.End()

	defer context.TimeSince(c.ctx, "boot_poll_ip", time.Now())

	logger := context.LoggerFromContext(c.ctx).WithField("self", "backend/gce_provider")

	gceInst := &gceInstance{
		zoneName:   c.zoneName,
		client:     p.client,
		provider:   p,
		instance:   c.instance,
		ic:         p.ic,
		progresser: c.progresser,
		sshDialer:  c.sshDialer,

		authUser: "travis",

		projectID: p.projectID,
		imageName: c.image.Name,

		os:              c.startAttributes.OS,
		windowsPassword: c.windowsPassword,
	}

	if c.instanceWarmedIP != "" {
		logger.Debug("pre-warmed instance present, skipping boot poll")

		startupDuration := time.Now().UTC().Sub(c.bootStart)
		c.progresser.Progress(&ProgressEntry{
			Message:    fmt.Sprintf("instance is ready (%s)", startupDuration.Truncate(time.Millisecond)),
			State:      ProgressSuccess,
			Interrupts: true,
		})

		gceInst.startupDuration = startupDuration
		gceInst.cachedIPAddr = c.instanceWarmedIP
		gceInst.warmed = true
		c.instChan <- gceInst

		return multistep.ActionContinue
	}

	logger.WithField("duration", p.bootPrePollSleep).Debug("sleeping before first checking instance insert operation")
	c.progresser.Progress(&ProgressEntry{
		Message: fmt.Sprintf("sleeping %s before checking instance insert", p.bootPrePollSleep),
		State:   ProgressNeutral,
	})

	_, span = trace.StartSpan(ctx, "GCE.timeSleep.WaitForInstanceIP")
	time.Sleep(p.bootPrePollSleep)
	span.End()

	p.apiRateLimit(ctx)
	zoneOpCall := p.client.ZoneOperations.Get(p.projectID, c.zoneName, c.instanceInsertOpName).Context(c.ctx)

	c.progresser.Progress(&ProgressEntry{
		Message:   "polling for instance insert completion...",
		State:     ProgressNeutral,
		Continues: true,
	})

	for {
		metrics.Mark("worker.vm.provider.gce.boot.poll")

		p.apiRateLimit(c.ctx)
		newOp, err := zoneOpCall.Do()
		if err != nil {
			c.progresser.Progress(&ProgressEntry{
				Message:    "could not check for instance insert",
				State:      ProgressFailure,
				Interrupts: true,
			})
			c.errChan <- err
			return multistep.ActionHalt
		}

		if newOp.Status == "RUNNING" || newOp.Status == "DONE" {
			if newOp.Error != nil {
				c.progresser.Progress(&ProgressEntry{
					Message:    "instance could not be inserted",
					State:      ProgressFailure,
					Interrupts: true,
				})
				c.errChan <- &gceOpError{Err: newOp.Error}
				return multistep.ActionHalt
			}

			logger.WithFields(logrus.Fields{
				"status": newOp.Status,
				"name":   c.instanceInsertOpName,
			}).Debug("instance is ready")

			startupDuration := time.Now().UTC().Sub(c.bootStart)
			c.progresser.Progress(&ProgressEntry{
				Message:    fmt.Sprintf("instance is ready (%s)", startupDuration.Truncate(time.Millisecond)),
				State:      ProgressSuccess,
				Interrupts: true,
			})

			gceInst.startupDuration = startupDuration
			c.instChan <- gceInst

			return multistep.ActionContinue
		}

		if newOp.Error != nil {
			logger.WithFields(logrus.Fields{
				"err":  newOp.Error,
				"name": c.instanceInsertOpName,
			}).Error("encountered an error while waiting for instance insert operation")

			c.progresser.Progress(&ProgressEntry{
				Message:    "error while waiting for instance insert",
				State:      ProgressFailure,
				Interrupts: true,
			})
			c.errChan <- &gceOpError{Err: newOp.Error}
			return multistep.ActionHalt
		}

		logger.WithFields(logrus.Fields{
			"status":   newOp.Status,
			"name":     c.instanceInsertOpName,
			"duration": p.bootPollSleep,
		}).Debug("sleeping before checking instance insert operation")

		c.progresser.Progress(&ProgressEntry{Message: ".", Raw: true})

		var span *trace.Span
		_, span = trace.StartSpan(ctx, "GCE.timeSleep.afterInstanceInsertCompletion")
		time.Sleep(p.bootPollSleep)
		span.End()
	}
}

func (p *gceProvider) imageByFilter(ctx gocontext.Context, filter string) (*compute.Image, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.imageByFilter")
	defer span.End()

	p.apiRateLimit(ctx)
	images, err := p.client.Images.List(p.imageProjectID).Filter(filter).Context(ctx).Do()
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
	ctx, span := trace.StartSpan(ctx, "GCE.imageSelect")
	defer span.End()

	defer context.TimeSince(ctx, "image_select", time.Now())

	var (
		imageName string
		err       error
	)

	jobID, _ := context.JobIDFromContext(ctx)
	repo, _ := context.RepositoryFromContext(ctx)

	if startAttributes.ImageName != "" {
		imageName = startAttributes.ImageName
	} else {
		imageName, err = p.imageSelector.Select(ctx, &image.Params{
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
	}

	if imageName == "default" {
		imageName = p.defaultImage
	}

	if image, ok := p.imageCache.Load(imageName); ok {
		return image.(*compute.Image), nil
	}

	image, err := p.imageByFilter(ctx, fmt.Sprintf("name eq ^%s", imageName))
	if err != nil {
		return nil, err
	}

	p.imageCache.Store(imageName, image)

	return image, nil
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

func (p *gceProvider) buildInstance(ctx gocontext.Context, startAttributes *StartAttributes, imageLink, startupScript string) (*compute.Instance, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.buildinstance")
	defer span.End()
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")

	var err error

	zone := p.ic.Zone
	diskType := p.ic.DiskType

	if startAttributes.VMConfig.Zone != "" {
		p.apiRateLimit(ctx)
		zone, err = p.client.Zones.Get(p.projectID, startAttributes.VMConfig.Zone).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		logger.WithFields(logrus.Fields{
			"disk_type": fmt.Sprintf("zones/%s/diskTypes/pd-ssd", zone.Name),
		}).Info("setting disk type based on zone in vm config")
		diskType = fmt.Sprintf("zones/%s/diskTypes/pd-ssd", zone.Name)

	}

	var machineType *compute.MachineType
	if startAttributes.VMType == "premium" {
		machineType = p.ic.PremiumMachineType
	} else {
		machineType = p.ic.MachineType
	}

	// Set accelerator config based on number and type of requested GPUs (empty if none)
	acceleratorConfig := &compute.AcceleratorConfig{}
	if startAttributes.VMConfig.GpuCount > 0 {
		acceleratorConfig.AcceleratorCount = startAttributes.VMConfig.GpuCount
		acceleratorConfig.AcceleratorType = fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/acceleratorTypes/%s",
			p.projectID,
			startAttributes.VMConfig.Zone,
			startAttributes.VMConfig.GpuType)
	}

	var subnetwork string
	if p.ic.Subnetwork != nil {
		subnetwork = p.ic.Subnetwork.SelfLink
	}

	tags := []string{"testing"}
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
		tags = append(tags, "no-ip")
		networkInterface = &compute.NetworkInterface{
			Network:    p.ic.Network.SelfLink,
			Subnetwork: subnetwork,
		}
	}

	startupKey := "startup-script"
	if startAttributes.OS == "windows" {
		startupKey = "windows-startup-script-ps1"
	}

	if p.ic.Site != "" {
		tags = append(tags, p.ic.Site)
	}

	hostname := ""
	if p.deterministicHostname {
		hostname = hostnameFromContext(ctx)
	} else {
		hostname = fmt.Sprintf("travis-job-%s", uuid.NewRandom())
	}

	var onHostMaintenance string
	onHostMaintenance = "MIGRATE"

	acceleratorConfigs := []*compute.AcceleratorConfig{}
	if acceleratorConfig.AcceleratorCount > 0 {
		logger.Debug("GPU requested, setting acceleratorConfig")
		acceleratorConfigs = append(acceleratorConfigs, acceleratorConfig)
		onHostMaintenance = "TERMINATE"
	}

	if p.ic.Preemptible == true && onHostMaintenance == "MIGRATE" {
		// googleapi: Scheduling must have preemptible be false when OnHostMaintenance isn't TERMINATE.
		// In other words: if preemptible is true, OnHostMaintenance must be TERMINATE.
		logger.Warn("PREEMPTIBLE is set to true; forcing onHostMaintenance to TERMINATE")
		onHostMaintenance = "TERMINATE"
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
					DiskType:    diskType,
					DiskSizeGb:  p.ic.DiskSize,
				},
			},
		},
		GuestAccelerators: acceleratorConfigs,
		Scheduling: &compute.Scheduling{
			Preemptible:       p.ic.Preemptible,
			AutomaticRestart:  googleapi.Bool(false),
			OnHostMaintenance: onHostMaintenance,
		},
		MachineType: machineType.SelfLink,
		Name:        hostname,
		Zone:        zone.Name,
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				&compute.MetadataItems{
					Key:   startupKey,
					Value: googleapi.String(startupScript),
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			networkInterface,
		},
		Tags: &compute.Tags{
			Items: tags,
		},
	}, nil
}

func (p *gceProvider) warmerRequestInstance(ctx gocontext.Context, zone string, inst *compute.Instance) (*warmerResponse, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.warmerRequestInstance")
	defer span.End()

	defer context.TimeSince(ctx, "warmer_request_instance", time.Now())

	if len(inst.Disks) == 0 {
		return nil, errors.New("missing disk in instance description")
	}

	warmerReq := &warmerRequest{
		Site:        p.ic.Site,
		Zone:        zone,
		ImageName:   inst.Disks[0].InitializeParams.SourceImage,
		MachineType: inst.MachineType,
		PublicIP:    p.ic.PublicIP,
	}

	reqBody, err := json.Marshal(warmerReq)
	if err != nil {
		return nil, errors.Wrap(err, "could not encode request body")
	}

	b := bytes.NewBuffer(reqBody)
	req, err := http.NewRequest(
		"POST",
		p.warmerUrl.String()+"/request-instance",
		b,
	)
	if err != nil {
		return nil, errors.Wrap(err, "could not create http request")
	}

	req.Header.Add("Content-Type", "application/json")
	if p.warmerUrl.User != nil {
		if pw, ok := p.warmerUrl.User.Password(); ok {
			req.SetBasicAuth(p.warmerUrl.User.Username(), pw)
		}
	}
	req = req.WithContext(ctx)

	client := &http.Client{
		Timeout: p.warmerTimeout,
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "could not perform http request")
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return nil, errors.Errorf("expected 200 response code from warmer, got %v", res.StatusCode)
	}

	warmerRes := &warmerResponse{}

	err = json.NewDecoder(res.Body).Decode(warmerRes)
	if err != nil {
		return nil, errors.Wrap(err, "could not decode response body")
	}

	return warmerRes, nil
}

type warmerRequest struct {
	Site        string `json:"site"`
	Zone        string `json:"zone"`
	ImageName   string `json:"image_name"`
	MachineType string `json:"machine_type"`
	PublicIP    bool   `json:"public_ip"`
}

type warmerResponse struct {
	Name          string `json:"name"`
	Zone          string `json:"zone"`
	IP            string `json:"ip"`
	PublicIP      string `json:"public_ip"`
	SSHPrivateKey string `json:"ssh_private_key"`
}

func (i *gceInstance) sshConnection(ctx gocontext.Context) (remote.Remoter, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.sshConnection")
	defer span.End()

	ip, err := i.getCachedIP(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := i.sshDialer.Dial(fmt.Sprintf("%s:22", ip), i.authUser, i.provider.sshDialTimeout)
	if err != nil {
		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnavailable,
			Message: err.Error(),
		})
	}

	return conn, err
}

func (i *gceInstance) winrmRemoter(ctx gocontext.Context) (remote.Remoter, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.winrmRemoter")
	defer span.End()

	ip, err := i.getCachedIP(ctx)
	if err != nil {
		return nil, err
	}
	return winrm.New(ip, 5986, "travis", i.windowsPassword)
}

func (i *gceInstance) getCachedIP(ctx gocontext.Context) (string, error) {
	if i.cachedIPAddr != "" {
		return i.cachedIPAddr, nil
	}

	err := i.refreshInstance(ctx)
	if err != nil {
		return "", err
	}

	ipAddr := i.getIP()
	if ipAddr == "" {
		return "", errGCEMissingIPAddressError
	}

	i.cachedIPAddr = ipAddr
	return i.cachedIPAddr, nil
}

func (i *gceInstance) getIP() string {
	if i.ic.PublicIP && i.ic.PublicIPConnect {
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
	}

	// if instance has no public IP, return first private one
	for _, ni := range i.instance.NetworkInterfaces {
		return ni.NetworkIP
	}

	// TODO: return an error?
	return ""
}

// normalizes the zone name to ensure it is not the full URL, so
// https://www.googleapis.com/compute/v1/projects/travis-staging-1/zones/us-central1-a
// gets shortened to:
// us-central1-a
func (i *gceInstance) getZoneName() string {
	parts := strings.Split(i.instance.Zone, "/")
	return parts[len(parts)-1]
}

func (i *gceInstance) refreshInstance(ctx gocontext.Context) error {
	ctx, span := trace.StartSpan(ctx, "GCE.refreshInstance")
	defer span.End()

	i.provider.apiRateLimit(ctx)
	inst, err := i.client.Instances.Get(i.projectID, i.getZoneName(), i.instance.Name).Context(ctx).Do()
	if err != nil {
		return err
	}

	i.instance = inst
	return nil
}

func (i *gceInstance) Warmed() bool {
	return i.warmed
}

func (i *gceInstance) SupportsProgress() bool {
	return true
}

func (i *gceInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	defer context.TimeSince(ctx, "boot_poll_ssh", time.Now())

	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")

	uploadedChan := make(chan error)
	var lastErr error

	connType := "ssh"
	if i.os == "windows" {
		connType = "winrm"
	}

	waitStart := time.Now().UTC()
	i.progresser.Progress(&ProgressEntry{
		Message:   fmt.Sprintf("waiting for %s connectivity...", connType),
		State:     ProgressNeutral,
		Continues: true,
	})

	go func() {
		var errCount uint64
		for {
			if ctx.Err() != nil {
				return
			}

			err := i.uploadScriptAttempt(ctx, script)
			if err != nil {
				logger.WithError(err).Debug("upload script attempt errored")
			} else {
				timeToConn := time.Now().UTC().Sub(waitStart).Truncate(time.Millisecond)
				i.progresser.Progress(&ProgressEntry{
					Message:    fmt.Sprintf("%s connectivity established (%s)", connType, timeToConn),
					State:      ProgressSuccess,
					Interrupts: true,
				})
				i.progresser.Progress(&ProgressEntry{
					Message: "uploaded script",
					State:   ProgressSuccess,
				})
				uploadedChan <- nil
				return
			}

			lastErr = err

			errCount++
			if errCount > i.provider.uploadRetries {
				uploadedChan <- err
				return
			}

			i.progresser.Progress(&ProgressEntry{Message: ".", Raw: true})
			var span *trace.Span
			_, span = trace.StartSpan(ctx, "GCE.timeSleep.uploadRetry")
			time.Sleep(i.provider.uploadRetrySleep)
			span.End()

		}
	}()

	select {
	case err := <-uploadedChan:
		return err
	case <-ctx.Done():
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"err":  lastErr,
			"self": "backend/gce_instance",
		}).Info("stopping upload retries, error from last attempt")
		return ctx.Err()
	}
}

func (i *gceInstance) uploadScriptAttempt(ctx gocontext.Context, script []byte) error {
	var conn remote.Remoter
	var err error

	if i.os == "windows" {
		conn, err = i.winrmRemoter(ctx)
	} else {
		conn, err = i.sshConnection(ctx)
	}
	if err != nil {
		return errors.Wrap(err, "couldn't connect to remote server for script upload")
	}
	defer conn.Close()

	uploadDest := "build.sh"
	if i.os == "windows" {
		uploadDest = "c:/users/travis/build.sh"
	}

	context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"dest":       uploadDest,
		"script_len": len(script),
		"self":       "backend/gce_instance",
	}).Debug("uploading script")

	existed, err := conn.UploadFile(uploadDest, script)
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

	return nil
}

func (i *gceInstance) isPreempted(ctx gocontext.Context) (bool, error) {
	if !i.ic.Preemptible {
		return false, nil
	}

	listOpCall := i.provider.client.GlobalOperations.AggregatedList(i.provider.projectID).
		Filter(fmt.Sprintf("targetId eq %d", i.instance.Id)).Context(ctx)

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
	var conn remote.Remoter
	var err error

	if i.os == "windows" {
		conn, err = i.winrmRemoter(ctx)
	} else {
		conn, err = i.sshConnection(ctx)
	}
	if err != nil {
		return &RunResult{
			Completed: false,
		}, errors.Wrap(err, "couldn't connect to remote server for script run")
	}
	defer conn.Close()

	bashCommand := "bash ~/build.sh"
	if i.os == "windows" {
		bashCommand = `powershell -Command "& 'c:/program files/git/usr/bin/bash' -c 'export PATH=/bin:/usr/bin:$PATH; bash /c/users/travis/build.sh'"`
	}
	exitStatus, err := conn.RunCommand(bashCommand, output)

	preempted, googleErr := i.isPreempted(ctx)
	if googleErr != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"err":  googleErr,
			"self": "backend/gce_instance",
		}).Error("couldn't determine if instance was preempted")
		// could not get answer from google
		// requeue just in case
		return &RunResult{Completed: false}, googleErr
	}
	if preempted {
		metrics.Mark("travis.worker.gce.preempted-instances")
		return &RunResult{Completed: false}, nil
	}

	return &RunResult{Completed: err != nil, ExitCode: exitStatus}, errors.Wrap(err, "error running script")
}

func (i *gceInstance) DownloadTrace(ctx gocontext.Context) ([]byte, error) {
	var conn remote.Remoter
	var err error

	if i.os == "windows" {
		conn, err = i.winrmRemoter(ctx)
	} else {
		conn, err = i.sshConnection(ctx)
	}

	if err != nil {
		return nil, errors.Wrap(err, "couldn't connect to remote server to download trace")
	}
	defer conn.Close()

	buf, err := conn.DownloadFile("/tmp/build.trace")
	if err != nil {
		return nil, errors.Wrap(err, "couldn't download trace")
	}

	return buf, nil
}

func (i *gceInstance) Stop(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")
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
	op, err := i.client.Instances.Delete(i.projectID, i.getZoneName(), i.instance.Name).Context(c.ctx).Do()
	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.instanceDeleteOp = op
	return multistep.ActionContinue
}

func (i *gceInstance) stepWaitForInstanceDeleted(c *gceInstanceStopContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx).WithField("self", "backend/gce_instance")

	if i.ic.SkipStopPoll {
		logger.Debug("skipping instance deletion polling")
		c.errChan <- nil
		return multistep.ActionContinue
	}

	logger.WithFields(logrus.Fields{
		"duration": i.ic.StopPrePollSleep,
	}).Debug("sleeping before first checking instance delete operation")

	var span *trace.Span
	ctx := c.ctx
	ctx, span = trace.StartSpan(ctx, "GCE.timeSleep.WaitForInstanceDeleted")
	time.Sleep(i.ic.StopPrePollSleep)
	span.End()

	zoneOpCall := i.client.ZoneOperations.Get(i.projectID,
		i.getZoneName(), c.instanceDeleteOp.Name)

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
	return i.instance.Name
}

func (i *gceInstance) ImageName() string {
	return i.imageName
}

func (i *gceInstance) StartupDuration() time.Duration {
	return i.startupDuration
}
