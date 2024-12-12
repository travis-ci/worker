package worker

// worker watchdog - performs basic checks for worker lxd backend
// runs as a single check or a loop with '-l' or '--loop' parameters
// configuration:
// WATCHDOG_PING_URL: url to ping, default www.google.com
// WATCHDOG_INTERVAL: sleep in minutes before retry (when run with --loop param)
// DATADOG_URL: sends notification to Datadog - requires full url with key
// WATCHDOG_IMAGE: image to be used, default alpine:3.20
// additionally following envs (equal to worker) are available:
// NETWORK_STATIC, NETWORK_DNS, NETWORK_DNS, HTTP_PROXY, HTTPS_PROXY, FTP_PROXY, NO_PROXY
//
// on connection error, watchdog kills the worker basing on it's pid from /tmp/worker.pid
// and creates a /tmp/worker.lock until connection is available again

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	lxd "github.com/canonical/lxd/client"
	lxdconfig "github.com/canonical/lxd/lxc/config"
	lxdapi "github.com/canonical/lxd/shared/api"
)

type lxdWatchdog struct {
	client            lxd.InstanceServer
	url               string
	networkStatic     bool
	networkGateway    string
	networkSubnet     *net.IPNet
	networkMTU        string
	networkDNS        []string
	networkLeases     map[string]string
	networkLeasesLock sync.Mutex

	httpProxy, httpsProxy, ftpProxy, noProxy string
	lastSleep                                int
}

func newLxdWatchdog() (*lxdWatchdog, error) {
	client, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		fmt.Printf("[LXDWATCHDOG] can't connect lxd: %v\n", err)
		return nil, err
	}

	networkStatic := false
	networkMTU := "1500"
	var networkGateway string
	var networkSubnet *net.IPNet
	var networkLeases map[string]string

	if os.Getenv("TRAVIS_WORKER_LXD_NETWORK_STATIC") != "" {
		networkStatic = os.Getenv("TRAVIS_WORKER_LXD_NETWORK_STATIC") == "true"

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
				return nil, fmt.Errorf("no IPv4 subnet set on the network")
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

	networkDNS := []string{"1.1.1.1", "1.0.0.1"}
	if os.Getenv("TRAVIS_WORKER_LXD_NETWORK_DNS") != "" {
		networkDNS = strings.Split(os.Getenv("TRAVIS_WORKER_LXD_NETWORK_DNS"), ",")
	}

	httpProxy := os.Getenv("TRAVIS_WORKER_LXD_HTTP_PROXY")
	httpsProxy := os.Getenv("TRAVIS_WORKER_LXD_HTTPS_PROXY")
	ftpProxy := os.Getenv("TRAVIS_WORKER_LXD_FTP_PROXY")
	noProxy := os.Getenv("TRAVIS_WORKER_LXD_NO_PROXY")
	url := "www.google.com"

	if os.Getenv("WATCHDOG_PING_URL") != "" {
		url = os.Getenv("WATCHDOG_PING_URL")
	}

	return &lxdWatchdog{
		client: client,

		url: url,

		networkSubnet:  networkSubnet,
		networkGateway: networkGateway,
		networkStatic:  networkStatic,
		networkMTU:     networkMTU,
		networkDNS:     networkDNS,
		networkLeases:  networkLeases,

		httpProxy:  httpProxy,
		httpsProxy: httpsProxy,
		ftpProxy:   ftpProxy,
		noProxy:    noProxy,
		lastSleep:  0,
	}, nil
}

func (p *lxdWatchdog) getInstancesIps() []string {
	result := []string{}
	instances, err := p.client.GetInstances(lxdapi.InstanceTypeAny)
	if err != nil {
		fmt.Printf("[LXDWATCHDOG] Error on getting instances: %v\n", err)
		return result
	}
	for _, i := range instances {
		state, _, err := p.client.GetInstanceState(i.Name)
		if err != nil {
			fmt.Printf("[LXDWATCHDOG] Error on getting instance state: %v\n", err)
			return result
		}
		net := state.Network["eth0"]
		for _, addr := range net.Addresses {
			if addr.Family == "inet" {
				result = append(result, addr.Address)
			}

		}

	}
	return result
}

func (p *lxdWatchdog) allocateAddress(containerName string) (string, error) {
	p.networkLeasesLock.Lock()
	defer p.networkLeasesLock.Unlock()

	reservedIps := p.getInstancesIps()

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
		if ip[3] >= 230 {
			ips = append(ips, ip.String())
		}
	}

	usedIPs := []string{}
	for _, usedIP := range p.networkLeases {
		usedIPs = append(usedIPs, usedIP)
	}

	usedIPs = append(usedIPs, reservedIps...)

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

		fmt.Printf("[LXDWATCHDOG] FREE ADDRESS: %v\n", ip)
		// Allocate the address
		p.networkLeases[containerName] = ip
		size, _ := p.networkSubnet.Mask.Size()
		return fmt.Sprintf("%s/%d", ip, size), nil
	}

	return "", fmt.Errorf("no free addresses found")
}

func (p *lxdWatchdog) releaseAddress(containerName string) {
	p.networkLeasesLock.Lock()
	defer p.networkLeasesLock.Unlock()

	delete(p.networkLeases, containerName)
}

func (p *lxdWatchdog) getImage(imageName string) (lxd.ImageServer, *lxdapi.Image, error) {
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

func (p *lxdWatchdog) Start(singleRun bool) error {

	var (
		err error
	)

	containerName := "watchdogContainer"
	imageName := os.Getenv("WATCHDOG_IMAGE")
	if imageName == "" {
		imageName = "images:alpine/3.20"
	}

	imageServer, image, err := p.getImage(imageName)
	if err != nil {
		fmt.Printf("[LXDWATCHDOG] Error getting image: %v\n", err)
		return err
	}

	existingContainer, _, err := p.client.GetInstance(containerName)
	if err == nil {
		if existingContainer.StatusCode != lxdapi.Stopped {
			// Force stop the container
			req := lxdapi.InstanceStatePut{
				Action:  "stop",
				Timeout: -1,
				Force:   true,
			}

			op, err := p.client.UpdateInstanceState(containerName, req, "")
			if err != nil {
				return fmt.Errorf("couldn't stop preexisting container before create: %v", err)
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf("couldn't stop preexisting container before create: %v", err)
			}
		}

		op, err := p.client.DeleteInstance(containerName)
		if err != nil {
			return fmt.Errorf("couldn't remove preexisting container before create: %v", err)
		}

		err = op.Wait()
		if err != nil {
			return fmt.Errorf("couldn't remove preexisting container before create: %v", err)
		}

		if p.networkStatic {
			p.releaseAddress(containerName)
		}

		fmt.Printf("[LXDWATCHDOG] removed preexisting container before create\n")
	}

	// Create the container
	config := map[string]string{
		"security.devlxd":                      "false",
		"security.idmap.isolated":              "true",
		"security.idmap.size":                  "100000",
		"security.nesting":                     "true",
		"security.privileged":                  "false",
		"security.syscalls.intercept.mknod":    "true",
		"security.syscalls.intercept.setxattr": "true",
		"limits.memory":                        "500MB",
		"limits.processes":                     "1000",
		"linux.kernel_modules":                 "overlay",
		"limits.cpu":                           "1",
	}

	req := lxdapi.InstancesPost{
		Name: containerName,
	}
	req.Config = config

	rop, err := p.client.CreateInstanceFromImage(imageServer, *image, req)
	if err != nil {
		return fmt.Errorf("couldn't create a new container: %v", err)
	}

	err = rop.Wait()
	if err != nil {
		return fmt.Errorf("couldn't create a new container: %v", err)
	}

	// Configure the container devices
	container, etag, err := p.client.GetInstance(containerName)
	if err != nil {
		return fmt.Errorf("failed to get the container: %v", err)
	}

	// Disk limits
	container.Devices["root"] = container.ExpandedDevices["root"]
	container.Devices["root"]["size"] = "1GB"

	// Network limits
	container.Devices["eth0"] = container.ExpandedDevices["eth0"]
	container.Devices["eth0"]["limits.max"] = "100Mbit"
	container.Devices["eth0"]["security.mac_filtering"] = "true"
	container.Devices["eth0"]["security.ipv4_filtering"] = "true"
	container.Devices["eth0"]["security.ipv6_filtering"] = "false"

	// Static networking
	if p.networkStatic {
		address, err := p.allocateAddress(containerName)
		if err != nil {
			return err
		}

		container.Devices["eth0"]["ipv4.address"] = strings.Split(address, "/")[0]

		var fileName, content string
		fileName = "/etc/network/interfaces"
		content = fmt.Sprintf(`auto eth0		
iface eth0 inet static
  address %s
  gateway: %s
  netmask: 255.255.255.0
`, strings.Split(address, "/")[0], p.networkGateway)

		args := lxd.InstanceFileArgs{
			Type:    "file",
			Mode:    0644,
			UID:     0,
			GID:     0,
			Content: strings.NewReader(string(content)),
		}

		err = p.client.CreateInstanceFile(containerName, fileName, args)
		if err != nil {
			fmt.Printf("failed to upload network/interfaces to container: %v\n", err)
		}

		fileName = "/etc/resolv.conf"
		content = fmt.Sprintf("search lxd\nnameserver %s\n", p.networkGateway)
		for _, d := range p.networkDNS {
			content = fmt.Sprintf("%snameserver %s\n", content, d)
		}

		args = lxd.InstanceFileArgs{
			Type:    "file",
			Mode:    0644,
			UID:     0,
			GID:     0,
			Content: strings.NewReader(string(content)),
		}

		err = p.client.CreateInstanceFile(containerName, fileName, args)
		if err != nil {
			fmt.Printf("[LXDWATCHDOG] failed to upload resolv.conf to container: %v\n", err)
		}
	}

	// Save the changes
	op, err := p.client.UpdateInstance(containerName, container.Writable(), etag)
	if err != nil {
		return fmt.Errorf("failed to update the container config: %v", err)
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf("failed to update the container config: %v", err)
	}

	fmt.Printf("STARTING\n")
	// Start the container
	op, err = p.client.UpdateInstanceState(containerName, lxdapi.InstanceStatePut{Action: "start", Timeout: -1}, "")
	if err != nil {
		return fmt.Errorf("couldn't start new container: %v", err)
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf("couldn't start new container: %v", err)
	}

	fmt.Printf("STARTED - check connection\n")

	// Wait for connectivity
	connectivityCheck := func() error {
		if p.networkStatic {
			exec := lxdapi.InstanceExecPost{
				Command: []string{"route", "add", "default", "gw", p.networkGateway},
			}
			_, err = p.client.ExecInstance(containerName, exec, nil)
			if err != nil {
				fmt.Printf("[LXDWATCHDOG] couldn't add default gateway: %v\n", err)
			}
		}
		exec := lxdapi.InstanceExecPost{
			Command: []string{"ping", p.url, "-c", "1"},
		}

		// Spawn the command
		op, err := p.client.ExecInstance(containerName, exec, nil)
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

	testStartTime := time.Now().Unix()
	// Wait 30s for network
	time.Sleep(1 * time.Second)
	for i := 0; i < 60; i++ {
		err = connectivityCheck()
		if err == nil {
			break
		}
		fmt.Printf("[LXDWATCHDOG] wait for connection\n")

		time.Sleep(500 * time.Millisecond)

		testCurrentTime := time.Now().Unix()
		if testCurrentTime - testStartTime > 30 {
			fmt.Printf("[LXDWATCHDOG] timeout while waiting for connection\n")
			err = fmt.Errorf("connection test timeout")
			break
		}
	}

	if err != nil {
		fmt.Printf("[LXDWATCHDOG] container didn't have connectivity after 30s: %v\n", err)
		err = p.killWorker(singleRun)
		if err != nil {
			fmt.Printf("kill worker error: %v\n", err)
		}

		p.datadogAlert("[TRAVIS][LXC] Watchdog error", "container didn't have connectivity after 30s")
	}
	fmt.Printf("[LXDWATCHDOG] STARTED - OK\n")

	// Get the container
	container, _, err = p.client.GetInstance(containerName)
	if err != nil {
		return fmt.Errorf("failed to get the container: %v", err)
	}

	if container.StatusCode != lxdapi.Stopped {
		// Force stop the container
		req := lxdapi.InstanceStatePut{
			Action:  "stop",
			Timeout: -1,
			Force:   true,
		}

		op, err := p.client.UpdateInstanceState(container.Name, req, "")
		if err != nil {
			return fmt.Errorf("couldn't stop preexisting container before create: %v", err)

		}

		err = op.Wait()
		if err != nil {
			return fmt.Errorf("couldn't stop preexisting container before create: %v", err)

		}
	}

	op, err = p.client.DeleteInstance(container.Name)
	if err != nil {
		return fmt.Errorf("couldn't remove preexisting container before create: %v", err)
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf("couldn't remove preexisting container before create: %v", err)
	}

	if p.networkStatic {
		p.releaseAddress(container.Name)
	}

	_ = p.setStartInterval(1)
	fmt.Printf("[LXDWATCHDOG] CLEANUP DONE\n")
	return nil
}

func (p *lxdWatchdog) killWorker(singleRun bool) error {
	if singleRun {
		fmt.Printf("[LXDWATCHDOG] Can't reach network from LXD watchdog killing the worker")
		if p.lastSleep == 0 {
			p.lastSleep = 1
		} else {
			p.lastSleep *= 2
		}

		_ = p.setStartInterval(p.lastSleep)
		os.Exit(-1)
	}
	pid := os.Getpid()
	if syscall.Kill(pid, syscall.SIGINT) != nil {
		fmt.Printf("[LXDWATCHDOG] Couldn't send SIGINT to worker process [%d]\n", pid)
	} else {
		fmt.Printf("[LXDWATCHDOG] Sent SIGINT to worker process [%d]\n", pid)
	}
	return nil
}

func (p *lxdWatchdog) datadogAlert(title string, text string) {

	priority := "high"
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "n/a"
	}
	content := fmt.Sprintf(`{"title": "%s", "text" : "%s", "priority": "%s", "host": "%s", "tags": ["TravisCI", "lxc_alerts"], "alert_type": "error"}`, title, text, priority, hostname)
	url := os.Getenv("DATADOG_URL")
	if url == "" {
		return
	}
	r, err := http.NewRequest("POST", url, bytes.NewBufferString(content))
	if err != nil {
		fmt.Printf("[LXDWATCHDOG] ERROR on creating request for Datadog: %v\n", err)
	}
	r.Header.Add("Content-Type", "application/json")

	client := &http.Client{}
	_, err = client.Do(r)
	if err != nil {
		fmt.Printf("[LXDWATCHDOG] ERROR on sending request to Datadog: %v\n", err)
	}
}

func (p *lxdWatchdog) setStartInterval(interval int) error {
	file, err := os.Create("/tmp/ldx_watchdog.interval")
	if err != nil {
		return fmt.Errorf("can't set the worker lock, can't access the worker.lock file: %v", err)
	}
	defer file.Close()
	_, err = file.WriteString(strconv.Itoa(interval))
	if err != nil {
		return fmt.Errorf("can't set the worker lock, can't write the worker.lock file: %v", err)
	}

	return nil
}

func (p *lxdWatchdog) getStartInterval() int {
	defaultInterval := 0
	file, err := os.Open("/tmp/ldx_watchdog.interval")
	if err != nil {
		return defaultInterval
	}
	defer file.Close()
	data := make([]byte, 64)

	var count int
	count, err = file.Read(data)
	if err != nil {
		return defaultInterval
	}
	interval := 0
	interval, err = strconv.Atoi(string(data[:count]))
	if err != nil || interval == 0 {
		return defaultInterval
	}
	return interval
}

func (p *lxdWatchdog) handleSleep() {
	t := p.getStartInterval()
	if t > 0 {
		sleepTime := time.Duration(p.getStartInterval()) * time.Minute
		p.lastSleep = t
		fmt.Printf("[LXDWATCHDOG] last run was unsuccessful waiting for %d minutes before retry\n", t)
		time.Sleep(sleepTime)
	}
}

func watchdogMain(loop bool) {
	sleepTime := 60 * time.Minute
	sleepStr := os.Getenv("WATCHDOG_INTERVAL")
	if sleepStr != "" {

		t, err := strconv.Atoi(sleepStr)
		if err == nil {
			sleepTime = time.Duration(t) * time.Minute
		}
	}
	fmt.Println("[LXDWATCHDOG] Starting LXD watchdog")
	w, err := newLxdWatchdog()
	if !loop {
		w.handleSleep()
	}
	for {
		if err == nil {
			err = w.Start(!loop)
			if err != nil {
				fmt.Printf("[LXDWATCHDOG] error on start: %v\n", err)
			}
		} else {
			fmt.Printf("[LXDWATCHDOG] Starting LXD watchdog error: %v\n", err)
		}
		if !loop {
			break
		}
		err = nil

		time.Sleep(sleepTime)
	}
}

func RunLXDWatchdog(loop bool) {

	if loop {
		go watchdogMain(true)
	} else {
		watchdogMain(false)
	}
}
