package backend

import (
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"time"

	gocontext "context"

	lxd "github.com/lxc/lxd/client"
	lxdconfig "github.com/lxc/lxd/lxc/config"
	lxdapi "github.com/lxc/lxd/shared/api"

	"github.com/pkg/errors"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
)

var (
	lxdLimitCPU     = "2"
	lxdLimitDisk    = "10GB"
	lxdLimitMemory  = "4GB"
	lxdLimitNetwork = "100Mbit"
	lxdLimitProcess = "2000"
	lxdImage        = "ubuntu:18.04"
	lxdExecCmd      = "bash /home/travis/build.sh"
	lxdHelp         = map[string]string{
		"EXEC_CMD": fmt.Sprintf("command to run via exec/ssh (default %q)", lxdExecCmd),
		"MEMORY":   fmt.Sprintf("memory to allocate to each container (default %q)", lxdLimitMemory),
		"CPUS":     fmt.Sprintf("cpu count to allocate to each container (default %q)", lxdLimitCPU),
		"NETWORK":  fmt.Sprintf("network bandwidth (default %q)", lxdLimitNetwork),
		"DISK":     fmt.Sprintf("disk size (default %q)", lxdLimitDisk),
		"PROCESS":  fmt.Sprintf("maximum number of processes (default %q)", lxdLimitProcess),
		"IMAGE":    fmt.Sprintf("image to use for the containers (default %q)", lxdImage),
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

	runCmd       []string
	limitCPU     string
	limitDisk    string
	limitMemory  string
	limitNetwork string
	limitProcess string
	image        string

	httpProxy, httpsProxy, ftpProxy, noProxy string
}

func newLXDProvider(cfg *config.ProviderConfig) (Provider, error) {
	client, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return nil, err
	}

	execCmd := strings.Split(lxdExecCmd, " ")
	if cfg.IsSet("EXEC_CMD") {
		execCmd = strings.Split(cfg.Get("EXEC_CMD"), " ")
	}

	limitMemory := lxdLimitMemory
	if cfg.IsSet("MEMORY") {
		limitMemory = cfg.Get("MEMORY")
	}

	limitCPU := lxdLimitCPU
	if cfg.IsSet("CPUS") {
		limitCPU = cfg.Get("CPUS")
	}

	limitNetwork := lxdLimitNetwork
	if cfg.IsSet("NETWORK") {
		limitNetwork = cfg.Get("NETWORK")
	}

	limitProcess := lxdLimitProcess
	if cfg.IsSet("PROCESS") {
		limitNetwork = cfg.Get("PROCESS")
	}

	limitDisk := lxdLimitDisk
	if cfg.IsSet("DISK") {
		limitNetwork = cfg.Get("DISK")
	}

	image := lxdImage
	if cfg.IsSet("IMAGE") {
		image = cfg.Get("IMAGE")
	}

	httpProxy := cfg.Get("HTTP_PROXY")
	httpsProxy := cfg.Get("HTTPS_PROXY")
	ftpProxy := cfg.Get("FTP_PROXY")
	noProxy := cfg.Get("NO_PROXY")

	return &lxdProvider{
		client: client,

		limitCPU:     limitCPU,
		limitDisk:    limitDisk,
		limitMemory:  limitMemory,
		limitNetwork: limitNetwork,
		limitProcess: limitProcess,

		runCmd: execCmd,
		image:  image,

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

	// Select the image
	imageName := p.image
	if startAttributes.ImageName != "" {
		imageName = startAttributes.ImageName
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

		logger.Warn("removed preexisting container before create")
	}

	// Create the container
	config := map[string]string{
		"security.idmap.isolated": "true",
		"security.idmap.size":     "65536",
		"security.nesting":        "true",
		"limits.cpu":              p.limitCPU,
		"limits.memory":           p.limitMemory,
		"limits.processes":        p.limitProcess,
	}

	req := lxdapi.ContainersPost{
		Name: containerName,
	}
	req.Config = config

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

	// Wait 30s in 500ms increments
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
		startBooting:     time.Now(),
		imageFingerprint: image.Fingerprint,
	}, nil
}

func (p *lxdProvider) Setup(ctx gocontext.Context) error {
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

	return nil
}

func (i *lxdInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/lxd_provider")

	args := lxd.ContainerFileArgs{
		Type:    "file",
		Mode:    0700,
		UID:     1000,
		GID:     1000,
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
	env := map[string]string{}
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
	cmd := []string{"sudo", "-E", "-u", "travis", "--"}
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
		Stderr:   lxdWriteCloser{Writer: output},
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
