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
	mathrand "math/rand"
	"net/http"
	"net/url"
	"os"
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
	"google.golang.org/api/option"
)

const (
	defaultGCEZone                     = "us-central1-a"
	defaultGCEMachineType              = "n1-standard-2"
	defaultGCEPremiumMachineType       = "n1-standard-4"
	defaultGCENetwork                  = "default"
	defaultGCEDiskSize                 = int64(20)
	defaultGCELanguage                 = "minimal"
	defaultGCEBootPollSleep            = 3 * time.Second
	defaultGCEBootPrePollSleep         = 15 * time.Second
	defaultGCEStopPollSleep            = 3 * time.Second
	defaultGCEStopPrePollSleep         = 15 * time.Second
	defaultGCESubnet                   = "default"
	defaultGCERegion                   = "us-central1"
	defaultGCEUploadRetries            = uint64(120)
	defaultGCEUploadRetrySleep         = 1 * time.Second
	defaultGCETestConnectionRetries    = uint64(120)
	defaultGCETestConnectionRetrySleep = 1 * time.Second
	defaultGCEImageSelectorType        = "env"
	defaultGCEImage                    = "travis-ci.+"
	defaultGCEGpuCount                 = int64(0)
	defaultGCEGpuType                  = "nvidia-tesla-p100"

	defaultGCERateLimitMaxCalls         = uint64(10)
	defaultGCERateLimitDuration         = time.Second
	defaultGCERateLimitDynamicConfigTTL = time.Minute

	defaultGCESSHDialTimeout = 5 * time.Second
	defaultGCEWarmerTimeout  = 5 * time.Second

	sshTestConnectionTimeout = 5 * time.Second
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
		"DISK_SIZE_WINDOWS":      "disk size in GB for windows OS (defaults to DISK_SIZE value)",
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

		"BACKOFF_RETRY_MAX":     "Maximum allowed duration of generic exponential backoff retries (default 1m)",
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
		"WARMER_SSH_PASSPHRASE": "The passphrase used to decipher instace SSH keys",
		"ZONE":                  fmt.Sprintf("[DEPRECATED] Use ZONES instead. Zone name (default %q)", defaultGCEZone),
		"ZONES":                 fmt.Sprintf("comma-delimited list of zone names (default %q)", defaultGCEZone),
	}

	errGCEMissingIPAddressError      = fmt.Errorf("no IP address found")
	errGCEInstanceDeletionNotDone    = fmt.Errorf("instance deletion not done")
	errGCEInstanceStopNotDone        = fmt.Errorf("instance stop not done")
	errGCEInstanceStartNotDone       = fmt.Errorf("instance start not done")
	errGCEInstanceImageCreateNotDone = fmt.Errorf("instance image create not done")

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

	gceSimpleStartupScript = template.Must(template.New("gce-startup").Parse(`#!/usr/bin/env bash
cat > ~travis/.ssh/authorized_keys <<EOF
{{ .SSHPubKey }}
EOF
chown -R travis:travis ~travis/.ssh/
`))

	gceSimpleWindowsStartupScript = template.Must(template.New("gce-windows-startup").Parse(`
net localgroup administrators travis /add
$pw = '{{ .WindowsPassword }}' | ConvertTo-SecureString -AsPlainText -Force
Set-LocalUser -Name travis -Password $pw
`))

	// FIXME: get rid of the need for this global goop
	gceCustomHTTPTransport     http.RoundTripper
	gceCustomHTTPTransportLock sync.Mutex

	gceVMSizeMapping = map[string]string{
		"medium":     "n2-standard-2",
		"large":      "n2-standard-4",
		"x-large":    "n2-standard-8",
		"2x-large":   "n2-standard-16",
		"gpu-medium": "n1-standard-8",
		"gpu-xlarge": "n1-standard-8",
	}
)

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

type gceStartupScriptData struct {
	AutoImplode        bool
	HardTimeoutMinutes int64
	SSHPubKey          string
	HardTimeoutSeconds int64
	WindowsPassword    string
}

type ImageSizeResult struct {
	size int64
	arch string
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

type singleGpuMapping struct {
	GpuCount int64
	GpuType  string
	DiskSize int64
}

var gpuMedium = singleGpuMapping{
	GpuCount: 1,
	GpuType:  "nvidia-tesla-t4",
	DiskSize: 300}
var gpuXLarge = singleGpuMapping{
	GpuCount: 1,
	GpuType:  "nvidia-tesla-v100",
	DiskSize: 300}

func GpuMapping(vmSize string) (value singleGpuMapping) {
	gpuMapping := map[string]singleGpuMapping{
		"gpu-medium": gpuMedium,
		"gpu-xlarge": gpuXLarge,
	}
	return gpuMapping[vmSize]
}

func GpuDefaultGpuCount(vmSize string) (gpuCountInt int64) {
	return GpuMapping(vmSize).GpuCount
}

func GpuDefaultGpuDiskSize(vmSize string) (gpuDiskSizeInt int64) {
	return GpuMapping(vmSize).DiskSize
}

func GpuDefaultGpuType(vmSize string) (gpuTypeString string) {
	return GpuMapping(vmSize).GpuType
}

func GPUType(varSize string) string {
	switch varSize {
	case "gpu-medium":
		return "gpu-medium"
	case "gpu-xlarge":
		return "gpu-xlarge"
	default:
		return ""
	}
}

type gceAccountJSON struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

type gceProvider struct {
	client               *compute.Service
	projectID            string
	imageProjectID       string
	ic                   *gceInstanceConfig
	cfg                  *config.ProviderConfig
	alternateZones       []string
	machineTypeSelfLinks map[string]string

	backoffRetryMax          time.Duration
	deterministicHostname    bool
	imageSelectorType        string
	imageSelector            image.Selector
	imageCache               *sync.Map
	bootPollSleep            time.Duration
	bootPrePollSleep         time.Duration
	defaultLanguage          string
	defaultImage             string
	uploadRetries            uint64
	uploadRetrySleep         time.Duration
	testConnectionRetries    uint64
	testConnectionRetrySleep time.Duration
	sshDialer                ssh.Dialer
	sshDialTimeout           time.Duration

	rateLimiter         ratelimit.RateLimiter
	rateLimitMaxCalls   uint64
	rateLimitDuration   time.Duration
	rateLimitQueueDepth uint64

	warmerUrl           *url.URL
	warmerTimeout       time.Duration
	warmerSSHPassphrase string

	artifactManager *image.ArtifactManager
}

type gceInstanceConfig struct {
	MachineType        string
	PremiumMachineType string
	Zones              []*compute.Zone
	Network            *compute.Network
	Subnetwork         *compute.Subnetwork
	AcceleratorConfig  *compute.AcceleratorConfig
	DiskSize           int64
	DiskSizeWindows    int64
	SSHPubKey          string
	AutoImplode        bool
	HardTimeoutMinutes int64
	StopPollSleep      time.Duration
	StopPrePollSleep   time.Duration
	StartPollSleep     time.Duration
	StartPrePollSleep  time.Duration
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

func (gsmw *gceStartMultistepWrapper) Cleanup(multistep.StateBag) {}

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
	zonePinned           bool
	machineType          string
	premiumMachineType   string
}

type gceInstance struct {
	zoneName string
	client   *compute.Service
	provider *gceProvider
	instance *compute.Instance
	//ic       *gceInstanceConfig

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

	createCustomImageName string
}

type gceInstanceStopContext struct {
	ctx                   gocontext.Context
	errChan               chan error
	instanceDeleteOp      *compute.Operation
	instanceStopOp        *compute.Operation
	instanceStartOp       *compute.Operation
	instanceCreateImageOp *compute.Operation
	imageName             string
	imageSize             int64
	imageArchitecture     string
	logWriterFunc         func(string)
}

type gceInstanceStopMultistepWrapper struct {
	f func(*gceInstanceStopContext) multistep.StepAction
	c *gceInstanceStopContext
}

func (gismw *gceInstanceStopMultistepWrapper) Run(multistep.StateBag) multistep.StepAction {
	return gismw.f(gismw.c)
}

func (gismw *gceInstanceStopMultistepWrapper) Cleanup(multistep.StateBag) {}

func newGCEProvider(cfg *config.ProviderConfig) (Provider, error) {
	var (
		err error
	)

	client, ctx, err := buildGoogleComputeService(cfg)
	if err != nil {
		return nil, err
	}

	projectID := cfg.Get("PROJECT_ID")
	if metadata.OnGCE() {
		projectID, err = metadata.ProjectIDWithContext(ctx)
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

	zoneNames := []string{defaultGCEZone}
	if metadata.OnGCE() {
		zoneName, err := metadata.ZoneWithContext(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "could not get zone from metadata api")
		}
		zoneNames = []string{zoneName}
	}

	// For compatibility, should be removed later
	if cfg.IsSet("ZONE") {
		zoneNames = strings.Split(cfg.Get("ZONE"), ",")
	}

	if cfg.IsSet("ZONES") {
		zoneNames = strings.Split(cfg.Get("ZONES"), ",")
	}

	cfg.Set("ZONES", strings.Join(zoneNames, ","))

	mtName := defaultGCEMachineType
	if cfg.IsSet("MACHINE_TYPE") {
		mtName = cfg.Get("MACHINE_TYPE")
	}

	premiumMTName := defaultGCEPremiumMachineType
	if cfg.IsSet("PREMIUM_MACHINE_TYPE") {
		premiumMTName = cfg.Get("PREMIUM_MACHINE_TYPE")
	}

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

	diskSizeWindows := diskSize
	if cfg.IsSet("DISK_SIZE_WINDOWS") {
		ds, err := strconv.ParseInt(cfg.Get("DISK_SIZE_WINDOWS"), 10, 64)
		if err == nil {
			diskSizeWindows = ds
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

	testConnectionRetries := defaultGCETestConnectionRetries
	if cfg.IsSet("TESTCONNECTION_RETRIES") {
		ur, err := strconv.ParseUint(cfg.Get("TESTCONNECTION_RETRIES"), 10, 64)
		if err != nil {
			return nil, err
		}
		uploadRetries = ur
	}

	testConnectionRetrySleep := defaultGCETestConnectionRetrySleep
	if cfg.IsSet("TESTCONNECTION_RETRY_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("TESTCONNECTION_RETRY_SLEEP"))
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

	backoffRetryMax := time.Minute
	if cfg.IsSet("BACKOFF_RETRY_MAX") {
		backoffRetryMax, err = time.ParseDuration(cfg.Get("BACKOFF_RETRY_MAX"))
		if err != nil {
			return nil, err
		}
	}

	return &gceProvider{
		client:               client,
		projectID:            projectID,
		imageProjectID:       imageProjectID,
		cfg:                  cfg,
		alternateZones:       []string{},
		machineTypeSelfLinks: map[string]string{},
		sshDialer:            sshDialer,
		sshDialTimeout:       sshDialTimeout,

		ic: &gceInstanceConfig{
			Preemptible:        preemptible,
			PublicIP:           publicIP,
			PublicIPConnect:    publicIPConnect,
			DiskSize:           diskSize,
			DiskSizeWindows:    diskSizeWindows,
			SSHPubKey:          string(pubKey),
			AutoImplode:        autoImplode,
			StopPollSleep:      stopPollSleep,
			StopPrePollSleep:   stopPrePollSleep,
			SkipStopPoll:       skipStopPoll,
			Site:               site,
			AcceleratorConfig:  defaultAcceleratorConfig,
			MachineType:        mtName,
			PremiumMachineType: premiumMTName,
			Zones:              []*compute.Zone{},
		},

		backoffRetryMax:          backoffRetryMax,
		deterministicHostname:    deterministicHostname,
		imageSelector:            imageSelector,
		imageSelectorType:        imageSelectorType,
		imageCache:               &sync.Map{},
		bootPollSleep:            bootPollSleep,
		bootPrePollSleep:         bootPrePollSleep,
		defaultLanguage:          defaultLanguage,
		defaultImage:             defaultImage,
		uploadRetries:            uploadRetries,
		uploadRetrySleep:         uploadRetrySleep,
		testConnectionRetries:    testConnectionRetries,
		testConnectionRetrySleep: testConnectionRetrySleep,

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
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_provider")

	logger.WithField("zones", p.cfg.Get("ZONES")).Debug("resolving configured zone")

	for _, zoneName := range strings.Split(p.cfg.Get("ZONES"), ",") {
		err := p.backoffRetry(ctx, func() error {
			_ = p.apiRateLimit(ctx)

			zone, zErr := p.client.Zones.
				Get(p.projectID, zoneName).
				Context(ctx).
				Do()
			if zErr == nil {
				p.ic.Zones = append(p.ic.Zones, zone)
			}
			return zErr
		})

		if err != nil {
			return errors.Wrap(err, "failed to resolve configured zone")
		}

	}

	logger.WithField("network", p.cfg.Get("NETWORK")).Debug("resolving configured network")

	err := p.backoffRetry(ctx, func() error {
		_ = p.apiRateLimit(ctx)
		nw, nwErr := p.client.Networks.
			Get(p.projectID, p.cfg.Get("NETWORK")).
			Context(ctx).
			Do()
		if nwErr == nil {
			p.ic.Network = nw
		}
		return nwErr
	})

	if err != nil {
		return errors.Wrap(err, "failed te resolve configured network")
	}

	region := defaultGCERegion
	if metadata.OnGCE() {
		logger.WithField("region", p.ic.Zones[0].Region).Debug("setting region from zone when on gce")
		region = p.ic.Zones[0].Region
	}
	if p.cfg.IsSet("REGION") {
		logger.WithField("region", p.ic.Zones[0].Region).Debug("setting region from config")
		region = p.cfg.Get("REGION")
	}

	if p.cfg.IsSet("SUBNETWORK") {
		logger.WithField("subnetwork", p.cfg.Get("SUBNETWORK")).Debug("resolving configured subnetwork")

		err = p.backoffRetry(ctx, func() error {
			_ = p.apiRateLimit(ctx)
			sn, snErr := p.client.Subnetworks.
				Get(p.projectID, region, p.cfg.Get("SUBNETWORK")).
				Context(ctx).
				Do()
			if snErr == nil {
				p.ic.Subnetwork = sn
			}
			return snErr
		})

		if err != nil {
			return errors.Wrap(err, "failed to resolve configured subnetwork")
		}
	}

	logger.Debug("finding alternate zones")
	err = p.backoffRetry(ctx, func() error {
		_ = p.apiRateLimit(ctx)
		zl, zlErr := p.client.Zones.List(p.projectID).
			Context(ctx).
			Filter("status eq UP").
			Filter(fmt.Sprintf("region eq %s", p.ic.Zones[0].Region)).Do()

		if zlErr != nil {
			return zlErr
		}

		p.alternateZones = []string{}
		for _, z := range zl.Items {
			p.alternateZones = append(p.alternateZones, z.Name)
		}

		return nil
	})

	if err != nil {
		return errors.Wrap(err, "failed to find alternate zones")
	}

	logger.Debug("building machine type self link map")

	zoneNames := make([]string, len(p.ic.Zones))
	for i, zone := range p.ic.Zones {
		zoneNames[i] = zone.Name
	}

	machineTypes := []string{p.ic.MachineType, p.ic.PremiumMachineType}
	for _, machineType := range gceVMSizeMapping {
		if !stringInSlice(machineType, machineTypes) {
			machineTypes = append(machineTypes, machineType)
		}
	}
	for _, zoneName := range append(zoneNames, p.alternateZones...) {
		for _, machineType := range machineTypes {
			if zoneName == "" || machineType == "" {
				continue
			}

			key := gceMtKey(zoneName, machineType)
			logger.WithFields(logrus.Fields{
				"zone":         zoneName,
				"machine_type": machineType,
				"key":          key,
			}).Debug("finding machine type self link")

			err = p.backoffRetry(ctx, func() error {
				_ = p.apiRateLimit(ctx)

				mt, mtErr := p.client.MachineTypes.
					Get(p.projectID, zoneName, machineType).
					Context(ctx).
					Do()
				if mtErr == nil {
					p.machineTypeSelfLinks[key] = mt.SelfLink
				}

				return mtErr
			})

			if err != nil {
				logger.WithFields(logrus.Fields{
					"err":          err,
					"zone":         zoneName,
					"machine_type": machineType,
					"key":          key,
				}).Warn("failed to find machine type self link")
			}
		}
	}

	if len(p.machineTypeSelfLinks) == 0 {
		return errors.New("failed to find any machine type self link")
	}

	logger.WithField("map", p.machineTypeSelfLinks).Debug("built machine type self link map")

	return err
}

func (p *gceProvider) backoffRetry(ctx gocontext.Context, fn func() error) error {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 1 * time.Second
	b.MaxElapsedTime = p.backoffRetryMax

	return backoff.Retry(fn, backoff.WithContext(b, ctx))
}

func (p *gceProvider) backoffLongerRetry(ctx gocontext.Context, fn func() error) error {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 1 * time.Second
	b.MaxElapsedTime = p.backoffRetryMax * 100

	return backoff.Retry(fn, backoff.WithContext(b, ctx))
}

type MetricsTransport struct {
	Name      string
	Transport http.RoundTripper
}

func (m *MetricsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	metrics.Mark(m.Name)
	return m.Transport.RoundTrip(req)
}

func buildGoogleComputeService(cfg *config.ProviderConfig) (*compute.Service, gocontext.Context, error) {
	ctx := gocontext.WithValue(gocontext.Background(), oauth2.HTTPClient, &http.Client{
		Transport: &MetricsTransport{
			Name:      "worker.google.compute.api.client",
			Transport: &ochttp.Transport{},
		},
	})

	if !cfg.IsSet("ACCOUNT_JSON") {
		client, err := google.DefaultClient(ctx, compute.DevstorageFullControlScope, compute.ComputeScope)
		if err != nil {
			return nil, ctx, errors.Wrap(err, "could not build default client")
		}
		svc, err := compute.NewService(ctx, option.WithHTTPClient(client))
		return svc, ctx, err //nolint:staticcheck
	}

	a, err := loadGoogleAccountJSON(cfg.Get("ACCOUNT_JSON"))
	if err != nil {
		return nil, ctx, err
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

	svc, err := compute.NewService(ctx, option.WithHTTPClient(client))
	return svc, ctx, err //nolint:staticcheck
}

func loadGoogleAccountJSON(filenameOrJSON string) (*gceAccountJSON, error) {
	var (
		bytes []byte
		err   error
	)

	if strings.HasPrefix(strings.TrimSpace(filenameOrJSON), "{") {
		bytes = []byte(filenameOrJSON)
	} else {
		bytes, err = os.ReadFile(filenameOrJSON)
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

	c := &gceStartContext{
		startAttributes:    startAttributes,
		zoneName:           p.pickRandomZone(),
		machineType:        p.ic.MachineType,
		premiumMachineType: p.ic.PremiumMachineType,
		progresser:         progresser,
		sshDialer:          p.sshDialer,
		ctx:                ctx,
		instChan:           make(chan Instance),
		errChan:            make(chan error),
	}

	p.artifactManager = startAttributes.ArtifactManager

	state := &multistep.BasicStateBag{}

	wp, err := makeWindowsPassword()
	if err != nil {
		return nil, err
	}

	c.windowsPassword = wp

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
	inst, err := p.StartWithProgress(ctx, startAttributes, NewTextProgresser(io.Discard))

	return inst, err
}

func (p *gceProvider) stepGetImage(c *gceStartContext) multistep.StepAction {
	_, span := trace.StartSpan(c.ctx, "GCE.GetImage")
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
	_, span := trace.StartSpan(c.ctx, "GCE.RenderScript")
	defer span.End()

	scriptBuf := bytes.Buffer{}
	scriptData := gceStartupScriptData{
		AutoImplode:        p.ic.AutoImplode,
		HardTimeoutMinutes: int64(c.startAttributes.HardTimeout.Minutes()) + 10,
		SSHPubKey:          p.ic.SSHPubKey,
		HardTimeoutSeconds: int64(c.startAttributes.HardTimeout.Seconds()) + 600,
	}

	var err error
	if c.startAttributes.CreatedCustomImageId == 0 {
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
	} else {
		if c.startAttributes.OS == "windows" {
			scriptData.WindowsPassword = c.windowsPassword
			context.LoggerFromContext(c.ctx).WithFields(logrus.Fields{
				"self":             "backend/gce_provider",
				"windows_password": c.windowsPassword,
			}).Debug("rendering startup script with password")
			err = gceSimpleWindowsStartupScript.Execute(&scriptBuf, scriptData)
		} else {
			err = gceSimpleStartupScript.Execute(&scriptBuf, scriptData)
		}
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

	if c.startAttributes.VMConfig.Zone != "" {
		err := p.backoffRetry(ctx, func() error {
			_ = p.apiRateLimit(ctx)
			zone, zErr := p.client.Zones.Get(p.projectID, c.startAttributes.VMConfig.Zone).Context(ctx).Do()
			if zErr != nil {
				return zErr
			}
			c.zoneName = zone.Name
			c.zonePinned = true
			return nil
		})

		if err != nil {
			return multistep.ActionHalt
		}
	}

	inst, err := p.buildInstance(ctx, c)
	if err != nil {
		c.progresser.Progress(&ProgressEntry{
			Message: "could not build instance",
			State:   ProgressFailure,
		})
		c.errChan <- err
		return multistep.ActionHalt
	}

	c.instance = inst

	context.LoggerFromContext(c.ctx).WithFields(logrus.Fields{
		"self":     "backend/gce_provider",
		"instance": c.instance,
	}).Debug("inserting instance")

	c.bootStart = time.Now().UTC()

	if c.startAttributes.Warmer && p.warmerUrl != nil {
		warmerResponse, err := p.warmerRequestInstance(c.ctx, c.zoneName, c.instance)
		if err != nil {
			logger.WithError(err).Warn("could not obtain instance from warmer")
		} else {
			// success case
			logger.WithFields(logrus.Fields{
				"ip":   warmerResponse.IP,
				"name": warmerResponse.Name,
				"zone": warmerResponse.Zone,
			}).Info("got instance from warmer")

			c.instance.Name = warmerResponse.Name
			c.instance.Zone = warmerResponse.Zone
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
			der, err := x509.DecryptPEMBlock(block, []byte(p.warmerSSHPassphrase)) //nolint:staticcheck
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

	err = p.backoffRetry(c.ctx, func() error {
		_ = p.apiRateLimit(c.ctx)

		op, insErr := p.client.Instances.Insert(p.projectID, c.zoneName, c.instance).Context(c.ctx).Do()
		if insErr != nil {
			if !c.zonePinned {
				altZone := p.pickAlternateZone(c.zoneName)
				logger.WithFields(logrus.Fields{
					"err":       insErr,
					"prev_zone": c.zoneName,
					"next_zone": altZone,
				}).Warn("switching zones due to error")
				p.setStartContextZone(c, altZone)
			}
			return insErr
		}

		c.instanceInsertOpName = op.Name

		return nil
	})

	if err != nil {
		c.progresser.Progress(&ProgressEntry{
			Message: "could not insert instance",
			State:   ProgressFailure,
		})
		c.errChan <- err
		return multistep.ActionHalt
	}

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
		zoneName: c.zoneName,

		client:     p.client,
		provider:   p,
		instance:   c.instance,
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

	c.progresser.Progress(&ProgressEntry{
		Message:   "polling for instance insert completion...",
		State:     ProgressNeutral,
		Continues: true,
	})

	for {
		metrics.Mark("worker.vm.provider.gce.boot.poll")

		zoneOp := &compute.Operation{}

		err := p.backoffRetry(ctx, func() error {
			_ = p.apiRateLimit(c.ctx)
			op, zoErr := p.client.ZoneOperations.
				Get(p.projectID, c.zoneName, c.instanceInsertOpName).
				Context(ctx).
				Do()

			if zoErr == nil {
				zoneOp.Status = op.Status
				zoneOp.Error = op.Error
			}
			return zoErr
		})

		if err != nil {
			c.progresser.Progress(&ProgressEntry{
				Message:    "could not check for instance insert",
				State:      ProgressFailure,
				Interrupts: true,
			})
			c.errChan <- err
			return multistep.ActionHalt
		}

		if zoneOp.Status == "RUNNING" || zoneOp.Status == "DONE" {
			if zoneOp.Error != nil {
				c.progresser.Progress(&ProgressEntry{
					Message:    "instance could not be inserted",
					State:      ProgressFailure,
					Interrupts: true,
				})
				c.errChan <- &gceOpError{Err: zoneOp.Error}
				return multistep.ActionHalt
			}

			logger.WithFields(logrus.Fields{
				"status": zoneOp.Status,
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

		if zoneOp.Error != nil {
			logger.WithFields(logrus.Fields{
				"err":  zoneOp.Error,
				"name": c.instanceInsertOpName,
			}).Error("encountered an error while waiting for instance insert operation")

			c.progresser.Progress(&ProgressEntry{
				Message:    "error while waiting for instance insert",
				State:      ProgressFailure,
				Interrupts: true,
			})
			c.errChan <- &gceOpError{Err: zoneOp.Error}
			return multistep.ActionHalt
		}

		logger.WithFields(logrus.Fields{
			"status":   zoneOp.Status,
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

func (p *gceProvider) imageByName(ctx gocontext.Context, name string) (*compute.Image, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.imageByFilter")
	defer span.End()

	return p.client.Images.Get(p.projectID, name).Context(ctx).Do()
}

func (p *gceProvider) imageByFilter(ctx gocontext.Context, filter string) (*compute.Image, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.imageByFilter")
	defer span.End()

	imagesByName := map[string]*compute.Image{}
	imageNames := []string{}

	err := p.backoffRetry(ctx, func() error {
		_ = p.apiRateLimit(ctx)
		images, ilErr := p.client.Images.List(p.imageProjectID).Filter(filter).Context(ctx).Do()
		if ilErr != nil {
			return ilErr
		}

		if len(images.Items) == 0 {
			return fmt.Errorf("no image found with filter %s", filter)
		}

		for _, image := range images.Items {
			imagesByName[image.Name] = image
			imageNames = append(imageNames, image.Name)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	sort.Strings(imageNames)

	return imagesByName[imageNames[len(imageNames)-1]], nil
}

func (p *gceProvider) imageSelect(ctx gocontext.Context, startAttributes *StartAttributes) (*compute.Image, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.imageSelect")
	defer span.End()

	defer context.TimeSince(ctx, "image_select", time.Now())

	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")
	var (
		imageName string
		err       error
	)

	if startAttributes.UsedCustomImageId != 0 {
		imageName = p.artifactManager.GenerateCustomImageName(startAttributes.OwnerId, startAttributes.OwnerType, startAttributes.UsedCustomImageId)
		logger.Info(fmt.Sprintf("using custom image %s, ownerId: %d, ownerType: %s, usedCustomImageId: %d", imageName, startAttributes.OwnerId, startAttributes.OwnerType, startAttributes.UsedCustomImageId))
	} else {
		jobID, _ := context.JobIDFromContext(ctx)
		repo, _ := context.RepositoryFromContext(ctx)
		var gpuVMType = GPUType(startAttributes.VMSize)

		if startAttributes.ImageName != "" {
			imageName = startAttributes.ImageName
		} else {
			imageName, err = p.imageSelector.Select(ctx, &image.Params{
				Infra:     "gce",
				Language:  startAttributes.Language,
				OsxImage:  startAttributes.OsxImage,
				Dist:      startAttributes.Dist,
				Group:     startAttributes.Group,
				OS:        startAttributes.OS,
				JobID:     jobID,
				Repo:      repo,
				GpuVMType: gpuVMType,
			})

			if err != nil {
				return nil, err
			}
		}

		if imageName == "default" {
			imageName = p.defaultImage
		}
	}

	if image, ok := p.imageCache.Load(imageName); ok {
		return image.(*compute.Image), nil
	}

	var image *compute.Image
	if startAttributes.UsedCustomImageId != 0 {
		image, err = p.imageByName(ctx, imageName)
		if err != nil {
			return nil, err
		}
	} else {
		image, err = p.imageByFilter(ctx, fmt.Sprintf("name eq ^%s", imageName))
		if err != nil {
			return nil, err
		}
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

func (p *gceProvider) buildInstance(ctx gocontext.Context, c *gceStartContext) (*compute.Instance, error) {
	ctx, span := trace.StartSpan(ctx, "GCE.buildinstance")
	defer span.End()
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")

	inst := &compute.Instance{
		Description: fmt.Sprintf("Travis CI %s test VM", c.startAttributes.Language),
		Name:        fmt.Sprintf("travis-job-%s", uuid.NewRandom()),
		Zone:        c.zoneName,
	}

	var gpuVMType = GPUType(c.startAttributes.VMSize)

	machineType := p.ic.MachineType
	if c.startAttributes.VMType == "premium" {
		c.startAttributes.VMSize = "premium"
		machineType = p.ic.PremiumMachineType
	} else if c.startAttributes.VMSize != "" {
		if mtype, ok := gceVMSizeMapping[c.startAttributes.VMSize]; ok {
			machineType = mtype
			// storing converted machine type for instance size identification
			if gpuVMType == "" {
				c.startAttributes.VMSize = machineType
			}
		}
	}

	diskSize := p.ic.DiskSize
	if c.startAttributes.OS == "windows" {
		diskSize = p.ic.DiskSizeWindows
	}

	if gpuVMType != "" {
		diskSize = GpuDefaultGpuDiskSize(gpuVMType)
	}

	diskInitParams := &compute.AttachedDiskInitializeParams{
		SourceImage: c.image.SelfLink,
		DiskType:    gcePdSSDForZone(c.zoneName),
		DiskSizeGb:  diskSize,
	}

	inst.Disks = []*compute.AttachedDisk{
		{
			Type:             "PERSISTENT",
			Mode:             "READ_WRITE",
			Boot:             true,
			AutoDelete:       true,
			InitializeParams: diskInitParams,
		},
	}

	var ok bool
	inst.MachineType, ok = p.machineTypeSelfLinks[gceMtKey(c.zoneName, machineType)]
	if !ok {
		return nil, fmt.Errorf("no machine type %s for zone %s", machineType, c.zoneName)
	}

	// Set accelerator config based on number and type of requested GPUs (empty if none)
	acceleratorConfig := &compute.AcceleratorConfig{}
	if c.startAttributes.VMConfig.GpuCount > 0 {
		acceleratorConfig.AcceleratorCount = c.startAttributes.VMConfig.GpuCount
		acceleratorConfig.AcceleratorType = fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/acceleratorTypes/%s",
			p.projectID,
			c.startAttributes.VMConfig.Zone,
			c.startAttributes.VMConfig.GpuType)
	} else if gpuVMType != "" {
		logger.WithField("acceleratorConfig.AcceleratorType", acceleratorConfig.AcceleratorType).Debug("Setting AcceleratorConfig")
		if !strings.HasPrefix(acceleratorConfig.AcceleratorType, "https") {
			notUrlAcceleratorType := GpuDefaultGpuType(gpuVMType)
			logger.WithField("notUrlAcceleratorType", notUrlAcceleratorType).Debug("Retrieving AcceleratorType from defaults")
			logger.WithField("AcceleratorCount", p.ic.AcceleratorConfig.AcceleratorCount).Debug("Retrieving AcceleratorCount from defaults")
			acceleratorConfig.AcceleratorCount = GpuDefaultGpuCount(gpuVMType)
			acceleratorConfig.AcceleratorType = fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/acceleratorTypes/%s",
				p.projectID,
				c.zoneName,
				notUrlAcceleratorType)
			logger.WithField("acceleratorConfig.AcceleratorType", acceleratorConfig.AcceleratorType).Debug("Url for Accelerator Type is:")
		}
	}

	var subnetwork string
	if p.ic.Subnetwork != nil {
		subnetwork = p.ic.Subnetwork.SelfLink
	}

	inst.Tags = &compute.Tags{
		Items: []string{"testing"},
	}

	if p.ic.PublicIP {
		inst.NetworkInterfaces = []*compute.NetworkInterface{
			{
				AccessConfigs: []*compute.AccessConfig{
					{
						Name: "AccessConfig brought to you by travis-worker",
						Type: "ONE_TO_ONE_NAT",
					},
				},
				Network:    p.ic.Network.SelfLink,
				Subnetwork: subnetwork,
			},
		}
	} else {
		inst.Tags.Items = append(inst.Tags.Items, "no-ip")
		inst.NetworkInterfaces = []*compute.NetworkInterface{
			{
				Network:    p.ic.Network.SelfLink,
				Subnetwork: subnetwork,
			},
		}
	}

	startupKey := "startup-script"
	if c.startAttributes.OS == "windows" {
		startupKey = "windows-startup-script-ps1"
	}

	inst.Metadata = &compute.Metadata{
		Items: []*compute.MetadataItems{
			{
				Key:   startupKey,
				Value: googleapi.String(c.script),
			},
		},
	}

	if p.ic.Site != "" {
		inst.Tags.Items = append(inst.Tags.Items, p.ic.Site)
	}

	if p.deterministicHostname {
		inst.Name = hostnameFromContext(ctx)
	}

	inst.Scheduling = &compute.Scheduling{
		Preemptible:       p.ic.Preemptible,
		AutomaticRestart:  googleapi.Bool(false),
		OnHostMaintenance: "MIGRATE",
	}

	inst.GuestAccelerators = []*compute.AcceleratorConfig{}

	if acceleratorConfig.AcceleratorCount > 0 {
		logger.Debug("GPU requested, setting acceleratorConfig")
		inst.GuestAccelerators = append(inst.GuestAccelerators, acceleratorConfig)
		inst.Scheduling.OnHostMaintenance = "TERMINATE"
	}

	if p.ic.Preemptible && inst.Scheduling.OnHostMaintenance == "MIGRATE" {
		// googleapi: Scheduling must have preemptible be false when OnHostMaintenance isn't TERMINATE.
		// In other words: if preemptible is true, OnHostMaintenance must be TERMINATE.
		logger.Warn("PREEMPTIBLE is set to true; forcing onHostMaintenance to TERMINATE")
		inst.Scheduling.OnHostMaintenance = "TERMINATE"
	}

	return inst, nil
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

func (p *gceProvider) pickRandomZone() string {
	return p.ic.Zones[mathrand.Intn(len(p.ic.Zones))].Name
}

func (p *gceProvider) pickAlternateZone(zoneName string) string {
	if len(p.alternateZones) == 0 {
		return zoneName
	}

	for {
		altZone := p.alternateZones[mathrand.Intn(len(p.alternateZones))]
		if altZone != zoneName {
			return altZone
		}
		if len(p.alternateZones) == 1 {
			return zoneName
		}
	}
}

func (p *gceProvider) setStartContextZone(c *gceStartContext, zoneName string) {
	c.zoneName = zoneName

	if c.instance == nil {
		return
	}

	c.instance.Zone = zoneName
	c.instance.MachineType = p.machineTypeSelfLinks[gceMtKey(zoneName, c.instance.MachineType)]

	for _, disk := range c.instance.Disks {
		if disk.InitializeParams == nil {
			continue
		}

		disk.InitializeParams.DiskType = gcePdSSDForZone(zoneName)
	}
}

func gceMtKey(zoneName, machineType string) string {
	return zoneName + "-" + machineType
}

func gcePdSSDForZone(zoneName string) string {
	return fmt.Sprintf("zones/%s/diskTypes/pd-ssd", zoneName)
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
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self":   "backend/gce_instance",
		"method": "getCachedIP",
	})

	if i.cachedIPAddr != "" {
		logger.WithField("cached_ip", i.cachedIPAddr).Debug("returning cached ip address")
		return i.cachedIPAddr, nil
	}

	err := i.refreshInstance(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to refresh instance")
	}

	logger.Debug("getting ip")
	ipAddr := i.getIP()
	if ipAddr == "" {
		return "", errGCEMissingIPAddressError
	}

	i.cachedIPAddr = ipAddr
	return i.cachedIPAddr, nil
}

func (i *gceInstance) getIP() string {
	if i.provider.ic.PublicIP && i.provider.ic.PublicIPConnect {
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
	parts := strings.Split(strings.TrimRight(i.instance.Zone, "/"), "/")
	return parts[len(parts)-1]
}

func (i *gceInstance) refreshInstance(ctx gocontext.Context) error {
	ctx, span := trace.StartSpan(ctx, "GCE.refreshInstance")
	defer span.End()
	zone := i.getZoneName()

	context.LoggerFromContext(ctx).
		WithFields(logrus.Fields{
			"self": "backend/gce_instance",
			"zone": zone,
			"name": i.instance.Name,
		}).Debug("refreshing instance")

	return i.provider.backoffRetry(ctx, func() error {
		_ = i.provider.apiRateLimit(ctx)
		inst, err := i.client.Instances.
			Get(i.projectID, zone, i.instance.Name).
			Context(ctx).
			Do()

		if err != nil {
			return err
		}

		i.instance = inst
		return nil
	})
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
	if !i.provider.ic.Preemptible {
		return false, nil
	}

	preempted := &(struct{ state bool }{state: false})

	err := i.provider.backoffRetry(ctx, func() error {
		_ = i.provider.apiRateLimit(ctx)
		list, err := i.provider.client.GlobalOperations.
			AggregatedList(i.provider.projectID).
			Filter(fmt.Sprintf("targetId eq %d", i.instance.Id)).
			Context(ctx).
			Do()

		if err != nil {
			return err
		}

		for _, item := range list.Items {
			for _, op := range item.Operations {
				if op.Kind == "compute#operation" && op.OperationType == "compute.instances.preempted" {
					preempted.state = true
					return nil
				}
			}
		}

		return nil
	})

	return preempted.state, err
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

	bashCommand := "rm -f /tmp/build.trace; bash ~/build.sh; res=$?; rm -f ~/build.sh; exit $res"
	if i.os == "windows" {
		bashCommand = `powershell -Command "& 'c:/program files/git/usr/bin/bash' -c 'export PATH=/bin:/usr/bin:$PATH; bash /c/users/travis/build.sh'"`
	}

	exitStatus, err := conn.RunCommand(bashCommand, output)

	preempted, googleErr := i.isPreempted(ctx)
	if googleErr != nil {
		return &RunResult{Completed: false}, errors.Wrap(googleErr, "couldn't determine if instance was preempted")
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

func (i *gceInstance) CreateImage(ctx gocontext.Context, createCustomImageName string, logWriterFunc func(string)) (int64, string, string, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")
	state := &multistep.BasicStateBag{}

	c := &gceInstanceStopContext{
		ctx:           ctx,
		errChan:       make(chan error),
		logWriterFunc: logWriterFunc,
	}

	dotMakerDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-dotMakerDone:
				return
			case <-ticker.C:
				logWriterFunc(".")
			}
		}
	}()

	i.createCustomImageName = createCustomImageName

	logger.Info(fmt.Sprintf("creating custom image %s", createCustomImageName))

	c.imageName = createCustomImageName
	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepCreateImageFromInstance},
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepWaitForImageCreated},
		},
	}

	logger.WithField("instance", i.instance.Name).Info("stopping instance")
	go runner.Run(state)

	logger.Debug("selecting over error and done channels")
	select {
	case err := <-c.errChan:
		image, err := i.client.Images.Get(i.projectID, createCustomImageName).Do()
		if err != nil {
			logger.Error(fmt.Sprintf("get custom image size failed %s", err.Error()))
			return 0, "", "", err
		}
		close(dotMakerDone)
		return image.ArchiveSizeBytes, image.Architecture, i.os, err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.gce.stop.timeout")
		}
		close(dotMakerDone)
		return c.imageSize, c.imageArchitecture, i.os, ctx.Err()
	}
}

func (i *gceInstance) Restart(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")
	state := &multistep.BasicStateBag{}

	c := &gceInstanceStopContext{
		ctx:     ctx,
		errChan: make(chan error),
	}

	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepStopInstance},
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepWaitForInstanceStopped},
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepStartInstance},
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepWaitForInstanceStarted},
		},
	}

	logger.WithField("instance", i.instance.Name).Info("stopping instance")
	go runner.Run(state)

	logger.Debug("selecting over error and done channels")
	select {
	case err := <-c.errChan:
		return err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.gce.stop.timeout")
		}
		return ctx.Err()
	}
}

func (i *gceInstance) StopOnly(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/gce_instance")
	state := &multistep.BasicStateBag{}

	c := &gceInstanceStopContext{
		ctx:     ctx,
		errChan: make(chan error),
	}

	runner := &multistep.BasicRunner{
		Steps: []multistep.Step{
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepStopInstance},
			&gceInstanceStopMultistepWrapper{c: c, f: i.stepWaitForInstanceStopped},
		},
	}

	logger.WithField("instance", i.instance.Name).Info("stopping instance")
	go runner.Run(state)

	logger.Debug("selecting over error and done channels")
	select {
	case err := <-c.errChan:
		return err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.gce.stop.timeout")
		}
		return ctx.Err()
	}
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

func (i *gceInstance) stepStopInstance(c *gceInstanceStopContext) multistep.StepAction {
	err := i.provider.backoffRetry(c.ctx, func() error {
		op, err := i.client.Instances.
			Stop(i.projectID, i.getZoneName(), i.instance.Name).
			Context(c.ctx).
			Do()

		if err != nil {
			return err
		}
		c.instanceStopOp = op
		return nil
	})

	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) stepStartInstance(c *gceInstanceStopContext) multistep.StepAction {
	err := i.provider.backoffRetry(c.ctx, func() error {
		op, err := i.client.Instances.
			Start(i.projectID, i.getZoneName(), i.instance.Name).
			Context(c.ctx).
			Do()

		if err != nil {
			return err
		}
		c.instanceStartOp = op
		return nil
	})

	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) stepCreateImageFromInstance(c *gceInstanceStopContext) multistep.StepAction {
	err := i.provider.backoffRetry(c.ctx, func() error {
		ci := &compute.Image{
			Name:        i.createCustomImageName,
			SourceDisk:  i.instance.Disks[0].Source,
			Description: i.instance.Description,
			Labels:      i.instance.Labels,
		}
		op, err := i.client.Images.Insert(i.projectID, ci).Context(c.ctx).Do()
		if err != nil {
			return err
		}
		c.instanceCreateImageOp = op
		return nil
	})

	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) stepDeleteInstance(c *gceInstanceStopContext) multistep.StepAction {
	err := i.provider.backoffRetry(c.ctx, func() error {
		op, err := i.client.Instances.
			Delete(i.projectID, i.getZoneName(), i.instance.Name).
			Context(c.ctx).
			Do()

		if err != nil {
			return err
		}
		c.instanceDeleteOp = op
		return nil
	})

	if err != nil {
		c.errChan <- err
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) stepWaitForImageCreated(c *gceInstanceStopContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx).WithField("self", "backend/gce_instance")

	logger.WithFields(logrus.Fields{
		"duration": i.provider.ic.StopPrePollSleep,
	}).Debug("sleeping before first checking instance image creation operation")

	var span *trace.Span
	ctx := c.ctx
	ctx, span = trace.StartSpan(ctx, "GCE.timeSleep.WaitForInstanceImageCreate")

	time.Sleep(i.provider.ic.StopPrePollSleep)
	span.End()

	err := i.provider.backoffLongerRetry(ctx, func() error {
		_ = i.provider.apiRateLimit(c.ctx)
		globalOp, err := i.client.GlobalOperations.
			Get(i.projectID, c.instanceCreateImageOp.Name).
			Do()
		if err != nil {
			return err
		}

		if globalOp.Status == "DONE" {
			if globalOp.Error != nil {
				return &gceOpError{Err: globalOp.Error}
			}

			return nil
		}

		return errGCEInstanceImageCreateNotDone
	})

	c.errChan <- err

	if err != nil {
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) stepWaitForInstanceStarted(c *gceInstanceStopContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx).WithField("self", "backend/gce_instance")

	logger.WithFields(logrus.Fields{
		"duration": i.provider.ic.StartPrePollSleep,
	}).Debug("sleeping before first checking instance start operation")

	var span *trace.Span
	ctx := c.ctx
	ctx, span = trace.StartSpan(ctx, "GCE.timeSleep.WaitForInstanceStarted")
	time.Sleep(i.provider.ic.StartPrePollSleep)
	span.End()

	err := i.provider.backoffRetry(ctx, func() error {
		_ = i.provider.apiRateLimit(c.ctx)
		zoneOp, err := i.client.ZoneOperations.
			Get(i.projectID, i.getZoneName(), c.instanceStartOp.Name).
			Do()

		if err != nil {
			return err
		}

		if zoneOp.Status == "DONE" {
			if zoneOp.Error != nil {
				return &gceOpError{Err: zoneOp.Error}
			}

			return nil
		}

		return errGCEInstanceStartNotDone
	})

	c.errChan <- err

	if err != nil {
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) stepWaitForInstanceStopped(c *gceInstanceStopContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx).WithField("self", "backend/gce_instance")

	logger.WithFields(logrus.Fields{
		"duration": i.provider.ic.StopPrePollSleep,
	}).Debug("sleeping before first checking instance stop operation")

	var span *trace.Span
	ctx := c.ctx
	ctx, span = trace.StartSpan(ctx, "GCE.timeSleep.WaitForInstanceStopped")
	time.Sleep(i.provider.ic.StopPrePollSleep)
	span.End()

	err := i.provider.backoffRetry(ctx, func() error {
		_ = i.provider.apiRateLimit(c.ctx)
		zoneOp, err := i.client.ZoneOperations.
			Get(i.projectID, i.getZoneName(), c.instanceStopOp.Name).
			Do()

		if err != nil {
			return err
		}

		if zoneOp.Status == "DONE" {
			if zoneOp.Error != nil {
				return &gceOpError{Err: zoneOp.Error}
			}

			return nil
		}

		return errGCEInstanceStopNotDone
	})

	c.errChan <- err

	if err != nil {
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (i *gceInstance) stepWaitForInstanceDeleted(c *gceInstanceStopContext) multistep.StepAction {
	logger := context.LoggerFromContext(c.ctx).WithField("self", "backend/gce_instance")

	if i.provider.ic.SkipStopPoll {
		logger.Debug("skipping instance deletion polling")
		c.errChan <- nil
		return multistep.ActionContinue
	}

	logger.WithFields(logrus.Fields{
		"duration": i.provider.ic.StopPrePollSleep,
	}).Debug("sleeping before first checking instance delete operation")

	var span *trace.Span
	ctx := c.ctx
	ctx, span = trace.StartSpan(ctx, "GCE.timeSleep.WaitForInstanceDeleted")
	time.Sleep(i.provider.ic.StopPrePollSleep)
	span.End()

	err := i.provider.backoffRetry(ctx, func() error {
		_ = i.provider.apiRateLimit(c.ctx)
		zoneOp, err := i.client.ZoneOperations.
			Get(i.projectID, i.getZoneName(), c.instanceDeleteOp.Name).
			Do()

		if err != nil {
			return err
		}

		if zoneOp.Status == "DONE" {
			if zoneOp.Error != nil {
				return &gceOpError{Err: zoneOp.Error}
			}

			return nil
		}

		return errGCEInstanceDeletionNotDone
	})

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
