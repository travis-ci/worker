package backend

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	defaultGCENetwork            = "default"
	defaultGCEDiskSize           = int64(20)
	defaultGCELanguage           = "minimal"
	defaultGCEBootPollSleep      = 3 * time.Second
	defaultGCEBootPrePollSleep   = 15 * time.Second
	defaultGCEStopPollSleep      = 3 * time.Second
	defaultGCEStopPrePollSleep   = 15 * time.Second
	defaultGCEUploadRetries      = uint64(120)
	defaultGCEUploadRetrySleep   = 1 * time.Second
	defaultGCEHardTimeoutMinutes = int64(130)
	defaultGCEImageSelectorType  = "env"
	defaultGCEImage              = "travis-ci-mega.+"
	defaultGCERateLimitTick      = 5 * time.Second
	gceImageTravisCIPrefixFilter = "name eq ^travis-ci-%s.+"
)

var (
	gceHelp = map[string]string{
		"ACCOUNT_JSON":          "[REQUIRED] account JSON config",
		"AUTO_IMPLODE":          "schedule a poweroff at HARD_TIMEOUT_MINUTES in the future (default true)",
		"BOOT_POLL_SLEEP":       fmt.Sprintf("sleep interval between polling server for instance ready status (default %v)", defaultGCEBootPollSleep),
		"BOOT_PRE_POLL_SLEEP":   fmt.Sprintf("time to sleep prior to polling server for instance ready status (default %v)", defaultGCEBootPrePollSleep),
		"DEFAULT_LANGUAGE":      fmt.Sprintf("default language to use when looking up image (default %q)", defaultGCELanguage),
		"DISK_SIZE":             fmt.Sprintf("disk size in GB (default %v)", defaultGCEDiskSize),
		"HARD_TIMEOUT_MINUTES":  fmt.Sprintf("time in minutes in the future when poweroff is scheduled if AUTO_IMPLODE is true (default %v)", defaultGCEHardTimeoutMinutes),
		"IMAGE_ALIASES":         "comma-delimited strings used as stable names for images, used only when image selector type is \"env\"",
		"IMAGE_DEFAULT":         fmt.Sprintf("default image name to use when none found (default %q)", defaultGCEImage),
		"IMAGE_SELECTOR_TYPE":   fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultGCEImageSelectorType),
		"IMAGE_SELECTOR_URL":    "URL for image selector API, used only when image selector is \"api\"",
		"IMAGE_[ALIAS_]{ALIAS}": "full name for a given alias given via IMAGE_ALIASES, where the alias form in the key is uppercased and normalized by replacing non-alphanumerics with _",
		"MACHINE_TYPE":          fmt.Sprintf("machine name (default %q)", defaultGCEMachineType),
		"NETWORK":               fmt.Sprintf("network name (default %q)", defaultGCENetwork),
		"PROJECT_ID":            "[REQUIRED] GCE project id",
		"RATE_LIMIT_TICK":       "duration to wait between GCE API calls",
		"SKIP_STOP_POLL":        "immediately return after issuing first instance deletion request (default false)",
		"SSH_KEY_PASSPHRASE":    "[REQUIRED] passphrase for ssh key given as ssh_key_path",
		"SSH_KEY_PATH":          "[REQUIRED] path to ssh key used to access job vms",
		"SSH_PUB_KEY_PATH":      "[REQUIRED] path to ssh public key used to access job vms",
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
	client    *compute.Service
	projectID string
	ic        *gceInstanceConfig
	cfg       *config.ProviderConfig

	imageSelectorType string
	imageSelector     image.Selector
	bootPollSleep     time.Duration
	bootPrePollSleep  time.Duration
	defaultLanguage   string
	defaultImage      string
	uploadRetries     uint64
	uploadRetrySleep  time.Duration

	rateLimiter *time.Ticker
}

type gceInstanceConfig struct {
	MachineType        *compute.MachineType
	Zone               *compute.Zone
	Network            *compute.Network
	DiskType           string
	DiskSize           int64
	SSHKeySigner       ssh.Signer
	SSHPubKey          string
	AutoImplode        bool
	HardTimeoutMinutes int64
	StopPollSleep      time.Duration
	StopPrePollSleep   time.Duration
	SkipStopPoll       bool
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

	if !cfg.IsSet("SSH_KEY_PATH") {
		return nil, fmt.Errorf("missing SSH_KEY_PATH config key")
	}

	sshKeyBytes, err := ioutil.ReadFile(cfg.Get("SSH_KEY_PATH"))

	if err != nil {
		return nil, err
	}

	if !cfg.IsSet("SSH_PUB_KEY_PATH") {
		return nil, fmt.Errorf("missing SSH_PUB_KEY_PATH config key")
	}

	sshPubKeyBytes, err := ioutil.ReadFile(cfg.Get("SSH_PUB_KEY_PATH"))

	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(sshKeyBytes)
	if block == nil {
		return nil, fmt.Errorf("ssh key does not contain a valid PEM block")
	}

	if !cfg.IsSet("SSH_KEY_PASSPHRASE") {
		return nil, fmt.Errorf("missing SSH_KEY_PASSPHRASE config key")
	}

	der, err := x509.DecryptPEMBlock(block, []byte(cfg.Get("SSH_KEY_PASSPHRASE")))
	if err != nil {
		return nil, err
	}

	parsedKey, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, err
	}

	sshKeySigner, err := ssh.NewSignerFromKey(parsedKey)
	if err != nil {
		return nil, err
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

	hardTimeoutMinutes := defaultGCEHardTimeoutMinutes
	if cfg.IsSet("HARD_TIMEOUT_MINUTES") {
		ht, err := strconv.ParseInt(cfg.Get("HARD_TIMEOUT_MINUTES"), 10, 64)
		if err != nil {
			return nil, err
		}
		hardTimeoutMinutes = ht
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

	rateLimitTick := defaultGCERateLimitTick
	if cfg.IsSet("RATE_LIMIT_TICK") {
		si, err := time.ParseDuration(cfg.Get("RATE_LIMIT_TICK"))
		if err != nil {
			return nil, err
		}
		rateLimitTick = si
	}

	return &gceProvider{
		client:    client,
		projectID: projectID,
		cfg:       cfg,

		ic: &gceInstanceConfig{
			DiskSize:           diskSize,
			SSHKeySigner:       sshKeySigner,
			SSHPubKey:          string(sshPubKeyBytes),
			AutoImplode:        autoImplode,
			HardTimeoutMinutes: hardTimeoutMinutes,
			StopPollSleep:      stopPollSleep,
			StopPrePollSleep:   stopPrePollSleep,
			SkipStopPoll:       skipStopPoll,
		},

		imageSelector:     imageSelector,
		imageSelectorType: imageSelectorType,
		bootPollSleep:     bootPollSleep,
		bootPrePollSleep:  bootPrePollSleep,
		defaultLanguage:   defaultLanguage,
		defaultImage:      defaultImage,
		uploadRetries:     uploadRetries,
		uploadRetrySleep:  uploadRetrySleep,

		rateLimiter: time.NewTicker(rateLimitTick),
	}, nil
}

func (p *gceProvider) apiRateLimit() {
	<-p.rateLimiter.C
}

func (p *gceProvider) Setup() error {
	var err error

	p.apiRateLimit()
	p.ic.Zone, err = p.client.Zones.Get(p.projectID, p.cfg.Get("ZONE")).Do()
	if err != nil {
		return err
	}

	p.ic.DiskType = fmt.Sprintf("zones/%s/diskTypes/pd-ssd", p.ic.Zone.Name)

	p.apiRateLimit()
	p.ic.MachineType, err = p.client.MachineTypes.Get(p.projectID, p.ic.Zone.Name, p.cfg.Get("MACHINE_TYPE")).Do()
	if err != nil {
		return err
	}

	p.apiRateLimit()
	p.ic.Network, err = p.client.Networks.Get(p.projectID, p.cfg.Get("NETWORK")).Do()
	if err != nil {
		return err
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
			p.apiRateLimit()
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
	err := gceStartupScript.Execute(&scriptBuf, p.ic)
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

	p.apiRateLimit()
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

		p.apiRateLimit()
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

func (p *gceProvider) imageByFilter(filter string) (*compute.Image, error) {
	p.apiRateLimit()
	// TODO: add some TTL cache in here maybe?
	images, err := p.client.Images.List(p.projectID).Filter(filter).Do()
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

func (p *gceProvider) imageForLanguage(language string) (*compute.Image, error) {
	return p.imageByFilter(fmt.Sprintf(gceImageTravisCIPrefixFilter, language))
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

	return p.imageByFilter(fmt.Sprintf("name eq ^%s", imageName))
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
			Preemptible: true,
		},
		MachineType: p.ic.MachineType.SelfLink,
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
			&compute.NetworkInterface{
				AccessConfigs: []*compute.AccessConfig{
					&compute.AccessConfig{
						Name: "AccessConfig brought to you by travis-worker",
						Type: "ONE_TO_ONE_NAT",
					},
				},
				Network: p.ic.Network.SelfLink,
			},
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

func (i *gceInstance) sshClient() (*ssh.Client, error) {
	if i.cachedIPAddr == "" {
		err := i.refreshInstance()
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

	return ""
}

func (i *gceInstance) refreshInstance() error {
	i.provider.apiRateLimit()
	inst, err := i.client.Instances.Get(i.projectID, i.ic.Zone.Name, i.instance.Name).Do()
	if err != nil {
		return err
	}

	i.instance = inst
	return nil
}

func (i *gceInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	uploadedChan := make(chan error)

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
		return ctx.Err()
	}
}

func (i *gceInstance) uploadScriptAttempt(ctx gocontext.Context, script []byte) error {
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

	if _, err := f.Write(script); err != nil {
		return err
	}

	return nil
}

func (i *gceInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
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
		i.provider.apiRateLimit()
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
