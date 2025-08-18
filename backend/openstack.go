package backend

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	gocontext "context"

	"github.com/pkg/errors"
	"github.com/rackspace/gophercloud"
	"github.com/rackspace/gophercloud/openstack"
	"github.com/rackspace/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/rackspace/gophercloud/openstack/compute/v2/flavors"
	"github.com/rackspace/gophercloud/openstack/compute/v2/images"
	"github.com/rackspace/gophercloud/openstack/compute/v2/servers"
	"github.com/rackspace/gophercloud/openstack/networking/v2/networks"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ssh"
)

const (
	defaultOSZone              = "nova"
	defaultOSMachineType       = "m1.small"
	defaultOSNetwork           = "ext"
	defaultOSImage             = ""
	defaultOSImageSelectorType = "env"
	defaultOSRegion            = "RegionOne"
	defaultOSBootPollSleep     = 90 * time.Second  // VM to come to ACTIVE state. This is taken as int
	defaultOSSSHPollTimeout    = 600 * time.Second // Timeout after which IP is declared not sshable
	defaultOSBootPollDialSleep = 10 * time.Second  // Sleep between each dial to ssh port 22
	defaultOSSSHDialTimeout    = 20 * time.Second  // Dial timeout
	defaultOSSecGroup          = "default"
	defaultOSInstancePrefix    = "travis-"
	defaultOSSSHPassword       = ""
	defaultOSSSHUser           = "travis"
	defaultOSKeyPairName       = ""
	defaultOSSSHKeyPath        = ""
)

var (
	openStackHelp = map[string]string{
		"ENDPOINT":             "[REQUIRED] Keystone/Identity Service Endpoint",
		"TENANT_NAME":          "[REQUIRED] Openstack tenant name",
		"OS_USERNAME":          "[REQUIRED] Openstack user name",
		"OS_PASSWORD":          "[REQUIRED] Openstack user password",
		"OS_DOMAIN":            "[REQUIRED] Openstack domain name only in case using v3 Identity service API",
		"SSH_KEY_PATH":         "path to SSH key used to access job VMs",
		"INSTANCE_KEYPAIR":     "Key Pair Name to be used for instance creation",
		"SSH_PASSWORD":         "SSH password to login into the VM",
		"SSH_USER":             "SSH username to login into the VM",
		"AUTO_SSH_KEY_GEN":     "If SSH key generation is to be generated automatically (default false)",
		"IMAGE_DEFAULT":        fmt.Sprintf("default image name to use when none found (default %q)", defaultOSImage),
		"IMAGE_SELECTOR_TYPE":  fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultOSImageSelectorType),
		"IMAGE_SELECTOR_URL":   "URL for image selector API, used only when image selector is \"api\"",
		"IMAGE_ALIASES":        "comma-delimited strings used as stable names for images (default: \"\")",
		"MACHINE_TYPE":         fmt.Sprintf("machine type/flavor (default %q)", defaultOSMachineType),
		"NETWORK":              "Network to which instance is to be attached.",
		"SECURITY_GROUP":       fmt.Sprintf("Instance Security Group Name (default %v)", defaultOSSecGroup),
		"OS_REGION":            fmt.Sprintf("Openstack region (default %v)", defaultOSRegion),
		"OS_ZONE":              fmt.Sprintf("Openstack zone (default %v)", defaultOSZone),
		"INSTANCE_NAME":        fmt.Sprintf("Name of the VM to be created (default %v followed by timeStamp)", defaultOSInstancePrefix),
		"BOOT_POLL_SLEEP":      fmt.Sprintf("sleep interval between polling server for instance ACTIVE status (default %v)", defaultOSBootPollSleep),
		"BOOT_POLL_DIAL_SLEEP": fmt.Sprintf("sleep interval between connection dials (default %v)", defaultOSBootPollDialSleep),
		"SSH_POLL_TIMEOUT":     fmt.Sprintf("Timeout after which VM is marked not sshable (default %v)", defaultOSSSHDialTimeout),
		"SSH_DIAL_TIMEOUT":     fmt.Sprintf("connection timeout for ssh connections (default %v)", defaultOSSSHDialTimeout),
	}
)

func init() {
	Register("openstack", "OpenStack", openStackHelp, newOSProvider)
}

type osClients struct {
	computeClient *gophercloud.ServiceClient
	networkClient *gophercloud.ServiceClient
	authProvider  *gophercloud.ProviderClient
}

type osProvider struct {
	client            *gophercloud.ServiceClient
	networkClient     *gophercloud.ServiceClient
	authClient        *gophercloud.ProviderClient
	sshDialer         ssh.Dialer
	sshDialTimeout    time.Duration
	sshPollTimeout    time.Duration
	bootPollSleep     time.Duration
	bootPollDialSleep time.Duration
	defaultImage      string
	imageSelectorType string
	imageSelector     image.Selector
	cfg               *config.ProviderConfig
	ic                *osInstanceConfig
}

type osInstanceConfig struct {
	Name        string
	Zone        string
	SecGroup    string
	FlavorRef   string
	NetworkRef  string
	AutoKeyGen  bool
	SSHPass     string
	SSHKeyPath  string
	KeyPairName string
	ID          string
	AuthUser    string
	SSHPubKey   string
}

type osInstance struct {
	client          *gophercloud.ServiceClient
	provider        *osProvider
	instance        *servers.Server
	ic              *osInstanceConfig
	imageName       string
	startupDuration time.Duration
	ipAddr          string
}

func newOSProvider(cfg *config.ProviderConfig) (Provider, error) {
	var dialer ssh.Dialer
	var sshPubKey []byte

	clients, err := buildOSComputeService(cfg)
	if err != nil {
		return nil, err
	}

	sshDialTimeout := defaultOSSSHDialTimeout
	if cfg.IsSet("SSH_TIMEOUT") {
		sshDialTimeout, err = time.ParseDuration(cfg.Get("SSH_DIAL_TIMEOUT"))
		if err != nil {
			return nil, err
		}
	}

	sshPollTimeout := defaultOSSSHPollTimeout
	if cfg.IsSet("SSH_POLL_TIMEOUT") {
		sshPollTimeout, err = time.ParseDuration(cfg.Get("SSH_POLL_TIMEOUT"))
		if err != nil {
			return nil, err
		}
	}

	bootPollSleep := defaultOSBootPollSleep
	if cfg.IsSet("BOOT_POLL_SLEEP") {
		bootPollSleep, err = time.ParseDuration(cfg.Get("BOOT_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
	}

	bootPollDialSleep := defaultOSBootPollDialSleep
	if cfg.IsSet("BOOT_POLL_DIAL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("BOOT_POLL_DIAL_SLEEP"))
		if err != nil {
			return nil, errors.Wrap(err, "error parsing boot pool sleep duration")
		}
		bootPollDialSleep = si
	}

	zoneName := defaultOSZone
	if cfg.IsSet("OS_ZONE") {
		zoneName = cfg.Get("OS_ZONE")
	}
	cfg.Set("OS_ZONE", zoneName)

	flavor := defaultOSMachineType
	if cfg.IsSet("MACHINE_TYPE") {
		flavor = cfg.Get("MACHINE_TYPE")
	}
	cfg.Set("MACHINE_TYPE", flavor)

	nName := defaultOSNetwork
	if cfg.IsSet("NETWORK") {
		nName = cfg.Get("NETWORK")
	}
	cfg.Set("NETWORK", nName)

	secGroup := defaultOSSecGroup

	if cfg.IsSet("SECURITY_GROUP") {
		secGroup = cfg.Get("SECURITY_GROUP")
	}
	cfg.Set("SECURITY_GROUP", secGroup)

	instName := defaultOSInstancePrefix + time.Now().Format("20060102150405")
	if cfg.IsSet("INSTANCE_NAME") {
		instName = cfg.Get("INSTANCE_NAME")
	}
	cfg.Set("INSTANCE_NAME", instName)

	defaultImage := defaultOSImage
	if cfg.IsSet("IMAGE_DEFAULT") {
		defaultImage = cfg.Get("IMAGE_DEFAULT")
	}
	cfg.Set("IMAGE_DEFAULT", defaultImage)

	imageSelectorType := defaultOSImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	imageSelector, err := buildOSImageSelector(imageSelectorType, cfg)
	if err != nil {
		return nil, err
	}

	sshUser := defaultOSSSHUser
	if cfg.IsSet("SSH_USER") {
		sshUser = cfg.Get("SSH_USER")
	}
	cfg.Set("SSH_USER", sshUser)

	sshPass := defaultOSSSHPassword
	keyPairName := defaultOSKeyPairName
	sshKeyPath := defaultOSSSHKeyPath

	autoKeyGen := false
	if cfg.IsSet("AUTO_SSH_KEY_GEN") {
		val, err := strconv.ParseBool(cfg.Get("AUTO_SSH_KEY_GEN"))
		if err != nil {
			return nil, err
		}
		autoKeyGen = val
	}

	if autoKeyGen {
		privKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}

		pubKey, err := ssh.FormatPublicKey(&privKey.PublicKey)
		if err != nil {
			return nil, err
		}

		sshPubKey = pubKey

		dialer, err = ssh.NewDialerWithKey(privKey)
		if err != nil {
			return nil, err
		}
	} else {

		if cfg.IsSet("SSH_PASSWORD") {
			sshPass = cfg.Get("SSH_PASSWORD")
		}
		cfg.Set("SSH_PASSWORD", sshPass)

		if cfg.IsSet("INSTANCE_KEYPAIR") {
			keyPairName = cfg.Get("INSTANCE_KEYPAIR")
			if !cfg.IsSet("SSH_KEY_PATH") {
				return nil, errors.Errorf("expected SSH_KEY_PATH config key")
			} else {
				sshKeyPath = cfg.Get("SSH_KEY_PATH")
			}
		}
		dialer, _ = ssh.NewDialerWithPassword(sshPass)
		if sshKeyPath != "" {
			dialer, _ = ssh.NewDialer(sshKeyPath, "")
		}
	}

	networkID, err := networks.IDFromName(clients.networkClient, cfg.Get("NETWORK"))
	if err != nil {
		return nil, err
	}

	flvRef, err := flavors.IDFromName(clients.computeClient, cfg.Get("MACHINE_TYPE"))
	if err != nil {
		return nil, err
	}
	return &osProvider{
		client:            clients.computeClient,
		networkClient:     clients.networkClient,
		authClient:        clients.authProvider,
		sshDialer:         dialer,
		sshDialTimeout:    sshDialTimeout,
		sshPollTimeout:    sshPollTimeout,
		bootPollSleep:     bootPollSleep,
		bootPollDialSleep: bootPollDialSleep,
		defaultImage:      defaultImage,
		imageSelector:     imageSelector,
		imageSelectorType: imageSelectorType,
		cfg:               cfg,
		ic: &osInstanceConfig{
			Name:        instName,
			Zone:        zoneName,
			SecGroup:    secGroup,
			NetworkRef:  networkID,
			FlavorRef:   flvRef,
			SSHPass:     sshPass,
			SSHKeyPath:  sshKeyPath,
			KeyPairName: keyPairName,
			AuthUser:    sshUser,
			AutoKeyGen:  autoKeyGen,
			SSHPubKey:   string(sshPubKey),
		},
	}, nil
}

func buildOSComputeService(cfg *config.ProviderConfig) (*osClients, error) {
	var opts gophercloud.AuthOptions
	if !cfg.IsSet("ENDPOINT") {
		return nil, fmt.Errorf("missing ENDPOINT")
	}
	endpointSplit := strings.Split(cfg.Get("ENDPOINT"), "/")
	keystoneAPIVersion := endpointSplit[len(endpointSplit)-1]

	if !cfg.IsSet("TENANT_NAME") {
		return nil, fmt.Errorf("missing TENANT_NAME")
	}

	if !cfg.IsSet("OS_USERNAME") {
		return nil, fmt.Errorf("missing OS_USERNAME")
	}

	if !cfg.IsSet("OS_PASSWORD") {
		return nil, fmt.Errorf("missing OS_PASSWORD")
	}

	region := defaultOSRegion
	if cfg.IsSet("OS_REGION") {
		region = cfg.Get("OS_REGION")
	}

	if keystoneAPIVersion == "v2.0" {
		opts = gophercloud.AuthOptions{
			IdentityEndpoint: cfg.Get("ENDPOINT"),
			Username:         cfg.Get("OS_USERNAME"),
			Password:         cfg.Get("OS_PASSWORD"),
			TenantName:       cfg.Get("TENANT_NAME"),
			AllowReauth:      true, // TODO: limit the number of attempts using RoundTripper interface
		}
	} else if keystoneAPIVersion == "v3" {
		opts = gophercloud.AuthOptions{
			IdentityEndpoint: cfg.Get("ENDPOINT"),
			Username:         cfg.Get("OS_USERNAME"),
			Password:         cfg.Get("OS_PASSWORD"),
			TenantName:       cfg.Get("TENANT_NAME"),
			DomainName:       cfg.Get("OS_DOMAIN"),
			AllowReauth:      true, // TODO: limit the number of attempts using RoundTripper interface
		}
	}

	provider, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		return nil, err
	}

	compClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: region,
	})
	if err != nil {
		return nil, err
	}

	netClient, err := openstack.NewNetworkV2(provider, gophercloud.EndpointOpts{Name: "neutron", Region: cfg.Get("REGION")})
	if err != nil {
		return nil, err
	}

	return &osClients{
		computeClient: compClient,
		networkClient: netClient,
		authProvider:  provider,
	}, err
}

func (p *osProvider) Setup(ctx gocontext.Context) error { return nil }

func (p *osProvider) waitForSSH(ctx gocontext.Context, ip string) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/openstack_provider")

	logger.WithField("duration", p.sshPollTimeout).Info("Polling for instance to be ready for ssh")

	timeout := time.After(p.sshPollTimeout)

	tick := time.NewTicker(p.bootPollDialSleep)
	defer tick.Stop()

	dialer := &net.Dialer{Timeout: p.sshDialTimeout}
	for {
		if ctx.Err() != nil {
			return errors.Errorf("cancelling waiting for instance to boot, was waiting for SSH to come up")
		}

		select {
		case <-timeout:
			return errors.New("timed out")
		case <-tick.C:
			conn, err := dialer.Dial("tcp", fmt.Sprintf("%s:22", ip))
			if err == nil && conn != nil {
				conn.Close()
				logger.WithFields(logrus.Fields{
					"ip": ip,
				}).Info("instance is ready for ssh")
				return nil
			}
		case <-ctx.Done():
		}

	}
}

func (p *osProvider) waitForStatus(ctx gocontext.Context, id string, status string) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/openstack_provider")

	logger.WithField("duration", p.bootPollSleep).Info("Waiting for instance to be ACTIVE")
	timeout := time.After(p.bootPollSleep)

	tick := time.NewTicker(p.bootPollDialSleep)
	defer tick.Stop()

	for {
		if ctx.Err() != nil {
			return errors.Errorf("cancelling waiting for instance to boot, was waiting for VM to come to ACTIVE")
		}
		select {
		case <-timeout:
			return errors.New("timed out")
		case <-tick.C:
			current, err := servers.Get(p.client, id).Extract()
			if err != nil {
				logger.WithFields(logrus.Fields{
					"id":           id,
					"error_reason": err,
				}).Errorf("encountered an error while waiting for instance to be ACTIVE")
				return err
			}
			if current.Status == status {
				logger.WithFields(logrus.Fields{
					"state": status,
					"id":    id,
				}).Info("instance is ready")
				return nil
			}
		case <-ctx.Done():
		}
	}
}

func (p *osProvider) SupportsProgress() bool {
	return false
}

func (p *osProvider) StartWithProgress(ctx gocontext.Context, startAttributes *StartAttributes, _ Progresser) (Instance, error) {
	return p.Start(ctx, startAttributes)
}

func (p *osProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/openstack_provider")

	var startBooting time.Time
	var inst *servers.Server
	var bootErr, statusErr, waitForSshErr error

	imageName, err := p.getImageName(ctx, startAttributes)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't get image name")
	}

	imageRef, err := images.IDFromName(p.client, imageName)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't get image at Openstack backend")
	}

	if p.ic.SSHPubKey != "" {
		keyName := "travis" + time.Now().Format("20060102150405")
		_, err := keypairs.Create(p.client, keypairs.CreateOpts{
			Name:      keyName,
			PublicKey: p.ic.SSHPubKey,
		}).Extract()
		if err != nil {
			return nil, err
		}
		p.ic.KeyPairName = keyName
	}

	logger.Info("starting instance")
	if p.ic.KeyPairName != "" {
		serverOpts := keypairs.CreateOptsExt{
			CreateOptsBuilder: servers.CreateOpts{
				Name:             p.ic.Name,
				FlavorRef:        p.ic.FlavorRef,
				ImageRef:         imageRef,
				SecurityGroups:   []string{p.ic.SecGroup},
				Networks:         []servers.Network{{UUID: p.ic.NetworkRef}},
				AvailabilityZone: p.ic.Zone,
			},
			KeyName: p.ic.KeyPairName,
		}
		startBooting = time.Now()
		inst, bootErr = servers.Create(p.client, serverOpts).Extract()

	} else {
		serverOpts := servers.CreateOpts{
			Name:             p.ic.Name,
			FlavorRef:        p.ic.FlavorRef,
			ImageRef:         imageRef,
			SecurityGroups:   []string{p.ic.SecGroup},
			Networks:         []servers.Network{{UUID: p.ic.NetworkRef}},
			AvailabilityZone: p.ic.Zone,
		}
		startBooting = time.Now()
		inst, bootErr = servers.Create(p.client, serverOpts).Extract()
	}
	if bootErr != nil {
		return nil, errors.Wrap(bootErr, "error creating instance in Openstack")
	}

	statusErr = p.waitForStatus(ctx, inst.ID, "ACTIVE")
	if statusErr != nil {
		err := servers.Delete(p.client, inst.ID).ExtractErr()
		if err != nil {
			return nil, err
		}
		return nil, statusErr
	}

	serverDetails, err := servers.Get(p.client, inst.ID).Extract()
	if err != nil {
		return nil, err
	}
	ipAddr := serverDetails.Addresses[p.cfg.Get("NETWORK")].([]interface{})[0].(map[string]interface{})["addr"]
	ipAddress, ok := ipAddr.(string)

	if ok {
		waitForSshErr = p.waitForSSH(ctx, ipAddress)
		if waitForSshErr != nil {
			err := servers.Delete(p.client, inst.ID).ExtractErr()
			if err != nil {
				return nil, err
			}
			return nil, waitForSshErr
		}
	}

	metrics.TimeSince("worker.vm.provider.openstack.boot", startBooting)
	logger.WithField("instance_id", inst.ID).Info("booted instance")
	return &osInstance{
		client:          p.client,
		provider:        p,
		ic:              p.ic,
		instance:        inst,
		imageName:       imageName,
		ipAddr:          ipAddress,
		startupDuration: time.Now().UTC().Sub(startBooting),
	}, nil

}

func buildOSImageSelector(selectorType string, cfg *config.ProviderConfig) (image.Selector, error) {
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

func (p *osProvider) getImageName(ctx gocontext.Context, startAttributes *StartAttributes) (string, error) {
	jobID, _ := context.JobIDFromContext(ctx)
	repo, _ := context.RepositoryFromContext(ctx)

	imageName, err := p.imageSelector.Select(ctx, &image.Params{
		Infra:    "openstack",
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

	if imageName == "default" {
		imageName = p.defaultImage
	}

	return imageName, nil
}

func (i *osInstance) sshConnection() (ssh.Connection, error) {
	time.Sleep(time.Second * 20)
	return i.provider.sshDialer.Dial(fmt.Sprintf("%s:22", i.ipAddr), i.ic.AuthUser, i.provider.sshDialTimeout)
}

func (i *osInstance) Warmed() bool {
	return false
}

func (i *osInstance) SupportsProgress() bool {
	return false
}

func (i *osInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/openstack_instance")

	conn, err := i.sshConnection()
	if err != nil {
		logger.Info("could't connect to SSH server")
		return errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	existed, err := conn.UploadFile("build.sh", script)
	if existed {
		return ErrStaleVM
	}
	if err != nil {
		logger.Info("couldn't upload build script")
		return errors.Wrap(err, "couldn't upload build script")
	}
	logger.WithField("instace_id", i.instance.ID).Info("Script Uploaded Succesfully")
	return nil
}

func (i *osInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	conn, err := i.sshConnection()
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	exitStatus, err := conn.RunCommand("bash ~/build.sh", output)

	return &RunResult{Completed: err != nil, ExitCode: exitStatus}, errors.Wrap(err, "error running script")
}

func (i *osInstance) DownloadTrace(ctx gocontext.Context) ([]byte, error) {
	return nil, ErrDownloadTraceNotImplemented
}

func (i *osInstance) StopOnly(ctx gocontext.Context) error {
	return nil
}

func (i *osInstance) CreateImage(ctx gocontext.Context, createCustomImageName string, logWriterFunc func(string)) (int64, string, string, error) {
	return 0, "", "", nil
}

func (i *osInstance) Stop(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/openstack_instance")

	if i.ic.AutoKeyGen {
		keyErr := keypairs.Delete(i.provider.client, i.ic.KeyPairName).ExtractErr()
		if keyErr != nil {
			return errors.Wrap(keyErr, "Instance not yet deleted")
		}
	}

	logger.WithField("instance", i.instance.ID).Info("deleting instance")
	err := servers.Delete(i.provider.client, i.instance.ID).ExtractErr()
	if err != nil {
		return err
	}
	return nil
}

func (i *osInstance) ID() string {
	if i.instance == nil {
		return "{unidentified}"
	}
	return i.instance.ID
}

func (i *osInstance) ImageName() string {
	return i.imageName
}

func (i *osInstance) StartupDuration() time.Duration {
	if i.instance == nil {
		return zeroDuration
	}
	return i.startupDuration
}
