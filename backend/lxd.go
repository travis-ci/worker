package backend

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	gocontext "context"

	lxd "github.com/lxc/lxd/client"
	lxdconfig "github.com/lxc/lxd/lxc/config"
	lxdapi "github.com/lxc/lxd/shared/api"

	"github.com/pkg/errors"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
)

var (
	lxdArchOverride             = ""
	lxdLimitCPU                 = "2"
	lxdLimitCPUBurst            = false
	lxdLimitDisk                = "10GB"
	lxdLimitMemory              = "4GB"
	lxdNetworkStatic            = false
	lxdNetworkDns               = "1.1.1.1,1.0.0.1"
	lxdLimitNetwork             = "500Mbit"
	lxdLimitProcess             = "5000"
	lxdImage                    = "ubuntu:18.04"
	lxdImageAutoDownload        = false
	defaultLxdImageSelectorType = "env"
	lxdExecCmd                  = "bash /home/travis/build.sh"
	lxdExecUID                  = int64(1000)
	lxdDockerPool               = ""
	lxdDockerDisk               = "10GB"
	lxdNetworkIPv6Filtering     = "true"
	lxdSecurityPrivileged       = "false"

	lxdHelp = map[string]string{
		"ARCH_OVERRIDE":          fmt.Sprintf("override arch value from job config (default %q)", lxdArchOverride),
		"EXEC_CMD":               fmt.Sprintf("command to run via exec/ssh (default %q)", lxdExecCmd),
		"EXEC_UID":               fmt.Sprintf("UID of travis user (default %d)", lxdExecUID),
		"MEMORY":                 fmt.Sprintf("memory to allocate to each container (default %q)", lxdLimitMemory),
		"CPUS":                   fmt.Sprintf("CPU count to allocate to each container (default %q)", lxdLimitCPU),
		"CPUS_BURST":             fmt.Sprintf("allow using all CPUs when not in use (default %v)", lxdLimitCPUBurst),
		"NETWORK":                fmt.Sprintf("network bandwidth (default %q)", lxdLimitNetwork),
		"POOL":                   fmt.Sprintf("storage pool to use for the instances"),
		"DISK":                   fmt.Sprintf("disk size (default %q)", lxdLimitDisk),
		"PROCESS":                fmt.Sprintf("maximum number of processes (default %q)", lxdLimitProcess),
		"IMAGE":                  fmt.Sprintf("image to use for the containers (default %q)", lxdImage),
		"IMAGE_AUTO_DOWNLOAD":    fmt.Sprintf("automatically try to download lxc image if it's missing (default %v)", lxdImageAutoDownload),
		"IMAGE_SERVER_URL":       fmt.Sprintf("base URL for images auto download"),
		"IMAGE_SELECTOR_TYPE":    fmt.Sprintf("image selector type (\"env\" or \"api\", default %q)", defaultLxdImageSelectorType),
		"IMAGE_SELECTOR_URL":     fmt.Sprintf("URL for image selector API, used only when image selector is \"api\""),
		"DOCKER_POOL":            fmt.Sprintf("storage pool to use for Docker (default %q)", lxdDockerPool),
		"DOCKER_DISK":            fmt.Sprintf("disk size to use for Docker (default %q)", lxdDockerDisk),
		"NETWORK_STATIC":         fmt.Sprintf("whether to statically set network configuration (default %v)", lxdNetworkStatic),
		"NETWORK_DNS":            fmt.Sprintf("comma separated list of DNS servers (requires NETWORK_STATIC) (default %q)", lxdNetworkDns),
		"NETWORK_IPV6_FILTERING": fmt.Sprintf("prevent the containers from spoofing another's IPv6 address (default %s)", lxdNetworkIPv6Filtering),
		"SECURITY_PRIVILEGED":    fmt.Sprintf("request a container to run without a UID mapping when set true (default %s)", lxdSecurityPrivileged),
	}
)

func init() {
	Register("lxd", "LXD", lxdHelp, newLXDProvider)
}

type lxdWriteCloser struct {
	Writer io.Writer
}

func (w lxdWriteCloser) Write(data []byte) (n int, err error) {
	return w.Writer.Write(data)
}

func (w lxdWriteCloser) Close() error {
	return nil
}

type lxdProvider struct {
	client lxd.ContainerServer

	limitCPU      string
	limitCPUBurst bool
	limitDisk     string
	limitMemory   string
	limitNetwork  string
	limitProcess  string

	archOverride string
	image        string
	runCmd       []string
	runUID       int64

	imageSelectorType string
	imageSelector     image.Selector
	imageAutoDownload bool
	imageBaseURL      *url.URL

	networkStatic        bool
	networkGateway       string
	networkSubnet        *net.IPNet
	networkMTU           string
	networkDNS           []string
	networkLeases        map[string]string
	networkLeasesLock    sync.Mutex
	networkIPv6Filtering string
	securityPrivileged   string

	pool        string
	dockerCache string
	dockerDisk  string
	dockerPool  string

	httpProxy, httpsProxy, ftpProxy, noProxy string
}

func newLXDProvider(cfg *config.ProviderConfig) (Provider, error) {
	client, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return nil, err
	}

	archOverride := lxdArchOverride
	if cfg.IsSet("ARCH_OVERRIDE") {
		archOverride = cfg.Get("ARCH_OVERRIDE")
	}

	execCmd := strings.Split(lxdExecCmd, " ")
	if cfg.IsSet("EXEC_CMD") {
		execCmd = strings.Split(cfg.Get("EXEC_CMD"), " ")
	}

	execUID := lxdExecUID
	if cfg.IsSet("EXEC_UID") {
		execUIDStr := cfg.Get("EXEC_UID")
		var err error
		execUID, err = strconv.ParseInt(execUIDStr, 10, 64)
		if err != nil {
			return nil, err
		}
	}

	limitMemory := lxdLimitMemory
	if cfg.IsSet("MEMORY") {
		limitMemory = cfg.Get("MEMORY")
	}

	limitCPU := lxdLimitCPU
	if cfg.IsSet("CPUS") {
		limitCPU = cfg.Get("CPUS")
	}

	limitCPUBurst := lxdLimitCPUBurst
	if cfg.IsSet("CPUS_BURST") {
		limitCPUBurst = cfg.Get("CPUS_BURST") == "true"
	}

	limitNetwork := lxdLimitNetwork
	if cfg.IsSet("NETWORK") {
		limitNetwork = cfg.Get("NETWORK")
	}

	networkIPv6Filtering := lxdNetworkIPv6Filtering
	if cfg.IsSet("NETWORK_IPV6_FILTERING") {
		networkIPv6Filtering = cfg.Get("NETWORK_IPV6_FILTERING")
	}

	securityPrivileged := lxdSecurityPrivileged
	if cfg.IsSet("SECURITY_PRIVILEGED") {
		securityPrivileged = cfg.Get("SECURITY_PRIVILEGED")
	}

	networkStatic := lxdNetworkStatic
	networkMTU := "1500"
	var networkGateway string
	var networkSubnet *net.IPNet
	var networkLeases map[string]string
	if cfg.IsSet("NETWORK_STATIC") {
		networkStatic = cfg.Get("NETWORK_STATIC") == "true"

		network, _, err := client.GetNetwork("lxdbr0")
		if err != nil {
			return nil, err
		}

		if network.Managed {
			// Get MTU
			if network.Config["bridge.mtu"] != "" {
				networkMTU = network.Config["bridge.mtu"]
			}

			// Get subnet
			if network.Config["ipv4.address"] == "" {
				return nil, fmt.Errorf("No IPv4 subnet set on the network")
			}

			gateway, subnet, err := net.ParseCIDR(network.Config["ipv4.address"])
			if err != nil {
				return nil, err
			}

			networkGateway = gateway.String()
			networkSubnet = subnet
		} else {
			networkState, err := client.GetNetworkState("lxdbr0")
			if err != nil {
				return nil, err
			}

			// Get MTU
			networkMTU = fmt.Sprintf("%d", networkState.Mtu)

			// Get subnet
			for _, address := range networkState.Addresses {
				if address.Family != "inet" || address.Scope != "global" {
					continue
				}

				gateway, subnet, err := net.ParseCIDR(fmt.Sprintf("%s/%s", address.Address, address.Netmask))
				if err != nil {
					return nil, err
				}

				networkGateway = gateway.String()
				networkSubnet = subnet
			}
		}
		networkLeases = map[string]string{}
	}

	networkDNS := strings.Split(lxdNetworkDns, ",")
	if cfg.IsSet("NETWORK_DNS") {
		networkDNS = strings.Split(cfg.Get("NETWORK_DNS"), ",")
	}

	limitProcess := lxdLimitProcess
	if cfg.IsSet("PROCESS") {
		limitProcess = cfg.Get("PROCESS")
	}

	limitDisk := lxdLimitDisk
	if cfg.IsSet("DISK") {
		limitDisk = cfg.Get("DISK")
	}

	image := lxdImage
	if cfg.IsSet("IMAGE") {
		image = cfg.Get("IMAGE")
	}

	imageSelectorType := defaultLxdImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	imageAutoDownload := lxdImageAutoDownload
	if cfg.IsSet("IMAGE_AUTO_DOWNLOAD") {
		imageAutoDownload = cfg.Get("IMAGE_AUTO_DOWNLOAD") == "true"
	}

	var imageBaseURL *url.URL
	if imageAutoDownload {
		u, err := url.Parse(cfg.Get("IMAGE_SERVER_URL"))
		if err != nil {
			return nil, err
		}

		imageBaseURL = u
	}

	if imageSelectorType != "env" && imageSelectorType != "api" {
		return nil, fmt.Errorf("invalid image selector type %q", imageSelectorType)
	}

	imageSelector, err := buildLxdImageSelector(imageSelectorType, cfg)
	if err != nil {
		return nil, err
	}

	dockerDisk := lxdDockerDisk
	if cfg.IsSet("DOCKER_DISK") {
		dockerDisk = cfg.Get("DOCKER_DISK")
	}

	dockerPool := lxdDockerPool
	dockerCache := ""
	if cfg.IsSet("DOCKER_POOL") {
		dockerPool = cfg.Get("DOCKER_POOL")

		volume, _, err := client.GetStoragePoolVolume(dockerPool, "custom", "cache_docker")
		if err == nil {
			dockerCache = volume.Name
		}
	}

	pool := ""
	if cfg.IsSet("POOL") {
		pool = cfg.Get("POOL")
	}

	httpProxy := cfg.Get("HTTP_PROXY")
	httpsProxy := cfg.Get("HTTPS_PROXY")
	ftpProxy := cfg.Get("FTP_PROXY")
	noProxy := cfg.Get("NO_PROXY")

	return &lxdProvider{
		client: client,

		archOverride: archOverride,

		limitCPU:      limitCPU,
		limitCPUBurst: limitCPUBurst,
		limitDisk:     limitDisk,
		limitMemory:   limitMemory,
		limitNetwork:  limitNetwork,
		limitProcess:  limitProcess,

		runCmd: execCmd,
		runUID: execUID,
		image:  image,

		imageSelector:     imageSelector,
		imageSelectorType: imageSelectorType,
		imageAutoDownload: imageAutoDownload,
		imageBaseURL:      imageBaseURL,

		networkSubnet:        networkSubnet,
		networkGateway:       networkGateway,
		networkStatic:        networkStatic,
		networkMTU:           networkMTU,
		networkDNS:           networkDNS,
		networkLeases:        networkLeases,
		networkIPv6Filtering: networkIPv6Filtering,
		securityPrivileged:   securityPrivileged,

		pool:        pool,
		dockerCache: dockerCache,
		dockerDisk:  dockerDisk,
		dockerPool:  dockerPool,

		httpProxy:  httpProxy,
		httpsProxy: httpsProxy,
		ftpProxy:   ftpProxy,
		noProxy:    noProxy,
	}, nil
}

func (p *lxdProvider) SupportsProgress() bool {
	return false
}

func (p *lxdProvider) StartWithProgress(ctx gocontext.Context, startAttributes *StartAttributes, _ Progresser) (Instance, error) {
	return p.Start(ctx, startAttributes)
}

func (p *lxdProvider) allocateAddress(containerName string) (string, error) {
	p.networkLeasesLock.Lock()
	defer p.networkLeasesLock.Unlock()

	// Get all IPs
	inc := func(ip net.IP) {
		for j := len(ip) - 1; j >= 0; j-- {
			ip[j]++
			if ip[j] > 0 {
				break
			}
		}
	}

	stringInSlice := func(key string, list []string) bool {
		for _, entry := range list {
			if entry == key {
				return true
			}
		}

		return false
	}

	var ips []string
	ip := net.ParseIP(p.networkGateway)
	for ip := ip.Mask(p.networkSubnet.Mask); p.networkSubnet.Contains(ip); inc(ip) {
		ips = append(ips, ip.String())
	}

	usedIPs := []string{}
	for _, usedIP := range p.networkLeases {
		usedIPs = append(usedIPs, usedIP)
	}

	// Find a free address
	for _, ip := range ips {
		// Skip used addresses
		if ip == ips[0] {
			continue
		}

		if ip == p.networkGateway {
			continue
		}

		if ip == ips[len(ips)-1] {
			continue
		}

		if stringInSlice(ip, usedIPs) {
			continue
		}

		// Allocate the address
		p.networkLeases[containerName] = ip
		size, _ := p.networkSubnet.Mask.Size()
		return fmt.Sprintf("%s/%d", ip, size), nil
	}

	return "", fmt.Errorf("No free addresses found")
}

func (p *lxdProvider) releaseAddress(containerName string) {
	p.networkLeasesLock.Lock()
	defer p.networkLeasesLock.Unlock()

	delete(p.networkLeases, containerName)
}

func buildLxdImageSelector(selectorType string, cfg *config.ProviderConfig) (image.Selector, error) {
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

func (p *lxdProvider) getImage(imageName string) (lxd.ImageServer, *lxdapi.Image, error) {
	// Remote images
	if strings.Contains(imageName, ":") {
		defaultConfig := lxdconfig.NewConfig("", true)

		remote, fingerprint, err := defaultConfig.ParseRemote(imageName)
		if err != nil {
			return nil, nil, err
		}

		imageServer, err := defaultConfig.GetImageServer(remote)
		if err != nil {
			return nil, nil, err
		}

		if fingerprint == "" {
			fingerprint = "default"
		}

		alias, _, err := imageServer.GetImageAlias(fingerprint)
		if err == nil {
			fingerprint = alias.Target
		}

		image, _, err := imageServer.GetImage(fingerprint)
		if err != nil {
			return nil, nil, err
		}

		return imageServer, image, nil
	}

	// Local images
	fingerprint := imageName
	alias, _, err := p.client.GetImageAlias(imageName)
	if err == nil {
		fingerprint = alias.Target
	}

	image, _, err := p.client.GetImage(fingerprint)
	if err != nil {
		return nil, nil, err
	}

	return p.client, image, nil
}

func (p *lxdProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/lxd_provider")
	containerName := hostnameFromContext(ctx)

	var (
		imageName string
		err       error
	)

	jobID, _ := context.JobIDFromContext(ctx)
	repo, _ := context.RepositoryFromContext(ctx)

	// Select the image
	if startAttributes.ImageName != "" {
		imageName = startAttributes.ImageName
	} else {
		imageArch := startAttributes.Arch
		if p.archOverride != "" {
			imageArch = p.archOverride
		}

		imageName, err = p.imageSelector.Select(ctx, &image.Params{
			Infra:    "lxd-" + imageArch,
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

		if p.imageAutoDownload {
			apiSelector, ok := p.imageSelector.(*image.APISelector)
			if ok {
				var imgManager *image.Manager
				imgManager, err = image.NewManager(ctx, apiSelector, p.imageBaseURL)
				if err != nil {
					return nil, err
				}

				err = imgManager.Load(imageName)
			}
		}

		if err != nil {
			return nil, err
		}
	}

	if imageName == "default" {
		imageName = p.image
	}

	imageServer, image, err := p.getImage(imageName)
	if err != nil {
		logger.WithField("err", err).Error("unable to locate the image")
		return nil, err
	}

	// Handle existing containers
	existingContainer, _, err := p.client.GetContainer(containerName)
	if err == nil {
		if existingContainer.StatusCode != lxdapi.Stopped {
			// Force stop the container
			req := lxdapi.ContainerStatePut{
				Action:  "stop",
				Timeout: -1,
				Force:   true,
			}

			op, err := p.client.UpdateContainerState(containerName, req, "")
			if err != nil {
				logger.WithField("err", err).Error("couldn't stop preexisting container before create")
				return nil, err
			}

			err = op.Wait()
			if err != nil {
				logger.WithField("err", err).Error("couldn't stop preexisting container before create")
				return nil, err
			}
		}

		op, err := p.client.DeleteContainer(containerName)
		if err != nil {
			logger.WithField("err", err).Error("couldn't remove preexisting container before create")
			return nil, err
		}

		err = op.Wait()
		if err != nil {
			logger.WithField("err", err).Error("couldn't remove preexisting container before create")
			return nil, err
		}

		if p.dockerPool != "" {
			err := p.client.DeleteStoragePoolVolume(p.dockerPool, "custom", fmt.Sprintf("%s-docker", containerName))
			if err != nil {
				logger.WithField("err", err).Error("couldn't remove the container Docker storage volume")
				return nil, err
			}
		}

		if p.networkStatic {
			p.releaseAddress(containerName)
		}

		logger.Warn("removed preexisting container before create")
	}

	// Create the Docker volume
	if p.dockerPool != "" {
		vol := lxdapi.StorageVolumesPost{
			Name: fmt.Sprintf("%s-docker", containerName),
			Type: "custom",
		}
		vol.Config = map[string]string{
			"size": p.dockerDisk,
		}

		err := p.client.CreateStoragePoolVolume(p.dockerPool, vol)
		if err != nil {
			logger.WithField("err", err).Error("couldn't create the container Docker storage volume")
			return nil, err
		}
	}

	// Create the container
	config := map[string]string{
		"security.devlxd":                      "false",
		"security.idmap.isolated":              "true",
		"security.idmap.size":                  "100000",
		"security.nesting":                     "true",
		"security.privileged":                  p.securityPrivileged,
		"security.syscalls.intercept.mknod":    "true",
		"security.syscalls.intercept.setxattr": "true",
		"limits.memory":                        p.limitMemory,
		"limits.processes":                     p.limitProcess,
		"linux.kernel_modules":                 "overlay",
	}

	if !p.limitCPUBurst {
		config["limits.cpu"] = p.limitCPU
	} else {
		config["limits.cpu.allowance"] = fmt.Sprintf("%s00%%", p.limitCPU)
	}

	req := lxdapi.ContainersPost{
		Name: containerName,
	}
	req.Config = config

	if p.pool != "" {
		req.Devices = map[string]map[string]string{}
		req.Devices["root"] = map[string]string{}
		req.Devices["root"]["type"] = "disk"
		req.Devices["root"]["path"] = "/"
		req.Devices["root"]["pool"] = p.pool
	}

	rop, err := p.client.CreateContainerFromImage(imageServer, *image, req)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create a new container")
		return nil, err
	}

	err = rop.Wait()
	if err != nil {
		logger.WithField("err", err).Error("couldn't create a new container")
		return nil, err
	}

	// Configure the container devices
	container, etag, err := p.client.GetContainer(containerName)
	if err != nil {
		logger.WithField("err", err).Error("failed to get the container")
		return nil, err
	}

	// Disk limits
	container.Devices["root"] = container.ExpandedDevices["root"]
	container.Devices["root"]["size"] = p.limitDisk

	// Network limits
	container.Devices["eth0"] = container.ExpandedDevices["eth0"]
	container.Devices["eth0"]["limits.max"] = p.limitNetwork
	container.Devices["eth0"]["security.mac_filtering"] = "true"
	container.Devices["eth0"]["security.ipv4_filtering"] = "true"
	container.Devices["eth0"]["security.ipv6_filtering"] = p.networkIPv6Filtering

	// Docker storage
	if p.dockerPool != "" {
		container.Devices["docker"] = map[string]string{
			"type":   "disk",
			"source": fmt.Sprintf("%s-docker", containerName),
			"pool":   p.dockerPool,
			"path":   "/var/lib/docker",
		}

		if p.dockerCache != "" {
			container.Devices["docker-cache"] = map[string]string{
				"type":     "disk",
				"readonly": "true",
				"shift":    "true",
				// FIXME: workaround for lack of shifting on custom volumes (will be implemented soon)
				"source": fmt.Sprintf("/var/snap/lxd/common/lxd/storage-pools/%s/custom/%s/", p.dockerPool, p.dockerCache),
				"path":   "/var/lib/docker-cache",
			}
		}
	}

	// Static networking
	if p.networkStatic {
		address, err := p.allocateAddress(containerName)
		if err != nil {
			return nil, err
		}

		dns, err := json.Marshal(p.networkDNS)
		if err != nil {
			return nil, err
		}

		container.Devices["eth0"]["ipv4.address"] = strings.Split(address, "/")[0]

		var fileName, content string
		switch startAttributes.Dist {
		case "xenial":
			fileName = "/etc/network/interfaces"
			content = fmt.Sprintf(`# The loopback network interface
auto lo
iface lo inet loopback

auto eth0
iface eth0 inet static
  address %s
  gateway %s
  netmask 255.255.255.0
  dns-nameservers %s
  mtu %s
`, address, p.networkGateway, strings.Join(p.networkDNS, " "), p.networkMTU)
		default:
			fileName = "/etc/netplan/50-cloud-init.yaml"
			content = fmt.Sprintf(`network:
  version: 2
  ethernets:
    eth0:
      addresses:
        - %s
      gateway4: %s
      nameservers:
        addresses: %s
      mtu: %s
`, address, p.networkGateway, dns, p.networkMTU)
		}

		args := lxd.ContainerFileArgs{
			Type:    "file",
			Mode:    0644,
			UID:     0,
			GID:     0,
			Content: strings.NewReader(string(content)),
		}

		err = p.client.CreateContainerFile(containerName, fileName, args)
		if err != nil {
			logger.WithField("err", err).Error("failed to upload netplan/interfaces to container")
			return nil, err
		}
	}

	// Save the changes
	op, err := p.client.UpdateContainer(containerName, container.Writable(), etag)
	if err != nil {
		logger.WithField("err", err).Error("failed to update the container config")
		return nil, err
	}

	err = op.Wait()
	if err != nil {
		logger.WithField("err", err).Error("failed to update the container config")
		return nil, err
	}

	// Start the container
	op, err = p.client.UpdateContainerState(containerName, lxdapi.ContainerStatePut{Action: "start", Timeout: -1}, "")
	if err != nil {
		logger.WithField("err", err).Error("couldn't start new container")
		return nil, err
	}

	err = op.Wait()
	if err != nil {
		logger.WithField("err", err).Error("couldn't start new container")
		return nil, err
	}

	// Wait for connectivity
	connectivityCheck := func() error {
		exec := lxdapi.ContainerExecPost{
			Command: []string{"ping", "www.google.com", "-c", "1"},
		}

		// Spawn the command
		op, err := p.client.ExecContainer(containerName, exec, nil)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
		opAPI := op.Get()

		retVal := int32(opAPI.Metadata["return"].(float64))
		if retVal != 0 {
			return fmt.Errorf("ping exited with %d", retVal)
		}

		return nil
	}

	// Wait 30s for network
	time.Sleep(1 * time.Second)
	for i := 0; i < 60; i++ {
		err = connectivityCheck()
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		logger.WithField("err", err).Error("container didn't have connectivity after 30s")
		return nil, err
	}

	// Get the container
	container, _, err = p.client.GetContainer(containerName)
	if err != nil {
		logger.WithField("err", err).Error("failed to get the container")
		return nil, err
	}

	return &lxdInstance{
		client:           p.client,
		provider:         p,
		container:        container,
		runUID:           p.runUID,
		startBooting:     time.Now(),
		imageFingerprint: image.Fingerprint,
	}, nil
}

func (p *lxdProvider) Setup(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/lxd_provider")
	logger.Warn("The LXD provider is in beta, use it at your own risk!")

	return nil
}

func (i *lxdInstance) Warmed() bool {
	return false
}

func (i *lxdInstance) SupportsProgress() bool {
	return false
}

type lxdInstance struct {
	client           lxd.ContainerServer
	provider         *lxdProvider
	container        *lxdapi.Container
	runUID           int64
	startBooting     time.Time
	imageFingerprint string
}

func (i *lxdInstance) ID() string {
	if i.container == nil {
		return "{unidentified}"
	}

	return i.container.Name
}

func (i *lxdInstance) ImageName() string {
	return i.imageFingerprint
}

func (i *lxdInstance) StartupDuration() time.Duration {
	if i.container == nil {
		return zeroDuration
	}

	return i.startBooting.Sub(i.container.CreatedAt)
}

func (i *lxdInstance) Stop(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/lxd_provider")

	container, _, err := i.client.GetContainer(i.container.Name)
	if err != nil {
		logger.WithField("err", err).Error("failed to find container to stop")
		return err
	}

	if container.StatusCode != lxdapi.Stopped {
		// Force stop the container
		req := lxdapi.ContainerStatePut{
			Action:  "stop",
			Timeout: -1,
			Force:   true,
		}

		op, err := i.client.UpdateContainerState(container.Name, req, "")
		if err != nil {
			logger.WithField("err", err).Error("couldn't stop preexisting container before create")
			return err
		}

		err = op.Wait()
		if err != nil {
			logger.WithField("err", err).Error("couldn't stop preexisting container before create")
			return err
		}
	}

	op, err := i.client.DeleteContainer(container.Name)
	if err != nil {
		logger.WithField("err", err).Error("couldn't remove preexisting container before create")
		return err
	}

	err = op.Wait()
	if err != nil {
		logger.WithField("err", err).Error("couldn't remove preexisting container before create")
		return err
	}

	if i.provider.dockerPool != "" {
		err := i.client.DeleteStoragePoolVolume(i.provider.dockerPool, "custom", fmt.Sprintf("%s-docker", container.Name))
		if err != nil {
			logger.WithField("err", err).Error("couldn't remove the container Docker storage volume")
			return err
		}
	}

	if i.provider.networkStatic {
		i.provider.releaseAddress(container.Name)
	}

	return nil
}

func (i *lxdInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/lxd_provider")

	args := lxd.ContainerFileArgs{
		Type:    "file",
		Mode:    0700,
		UID:     i.runUID,
		GID:     i.runUID,
		Content: strings.NewReader(string(script)),
	}

	err := i.client.CreateContainerFile(i.container.Name, "/home/travis/build.sh", args)
	if err != nil {
		logger.WithField("err", err).Error("failed to upload file to container")
		return err
	}

	return nil
}

func (i *lxdInstance) DownloadTrace(ctx gocontext.Context) ([]byte, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/lxd_provider")

	r, _, err := i.client.GetContainerFile(i.container.Name, "/tmp/build.trace")
	if err != nil {
		logger.WithField("err", err).Error("failed to retrieve file from container")
		return nil, err
	}
	defer r.Close()

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		logger.WithField("err", err).Error("failed to read trace content")
		return nil, errors.Wrap(err, "couldn't read contents of file")
	}

	return buf, nil
}

func (i *lxdInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/lxd_provider")

	// Build the environment
	env := map[string]string{
		"HOME": "/home/travis",
		"USER": "travis",
		"TERM": "xterm",
	}
	if i.provider.httpProxy != "" {
		env["HTTP_PROXY"] = i.provider.httpProxy
		env["http_proxy"] = i.provider.httpProxy
	}

	if i.provider.httpsProxy != "" {
		env["HTTPS_PROXY"] = i.provider.httpsProxy
		env["https_proxy"] = i.provider.httpsProxy
	}

	if i.provider.ftpProxy != "" {
		env["FTP_PROXY"] = i.provider.ftpProxy
		env["ftp_proxy"] = i.provider.ftpProxy
	}

	if i.provider.noProxy != "" {
		env["NO_PROXY"] = i.provider.noProxy
		env["no_proxy"] = i.provider.noProxy
	}

	// Setup the arguments
	cmd := []string{"sudo", "-h", "localhost", "-E", "-u", "travis", "--"}
	cmd = append(cmd, i.provider.runCmd...)
	exec := lxdapi.ContainerExecPost{
		Command:     cmd,
		WaitForWS:   true,
		Interactive: false,
		Environment: env,
	}

	args := lxd.ContainerExecArgs{
		Stdin:    nil,
		Stdout:   lxdWriteCloser{Writer: output},
		Stderr:   nil,
		DataDone: make(chan bool),
	}

	// Spawn the command
	op, err := i.client.ExecContainer(i.container.Name, exec, &args)
	if err != nil {
		logger.WithField("err", err).Error("failed to exec command")
		return nil, err
	}

	err = op.Wait()
	if err != nil {
		logger.WithField("err", err).Error("failed to exec command")
		return &RunResult{Completed: false}, err
	}
	opAPI := op.Get()

	// Wait for any remaining I/O to be flushed
	<-args.DataDone

	return &RunResult{Completed: true, ExitCode: int32(opAPI.Metadata["return"].(float64))}, nil
}
