package backend

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
	"github.com/dustin/go-humanize"
	"github.com/fsouza/go-dockerclient"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ssh"
)

var (
	defaultDockerNumCPUer       dockerNumCPUer = &stdlibNumCPUer{}
	defaultDockerSSHDialTimeout                = 5 * time.Second
	defaultExecCmd                             = "bash /home/travis/build.sh"
	dockerHelp                                 = map[string]string{
		"ENDPOINT / HOST":  "[REQUIRED] tcp or unix address for connecting to Docker",
		"CERT_PATH":        "directory where ca.pem, cert.pem, and key.pem are located (default \"\")",
		"CMD":              "command (CMD) to run when creating containers (default \"/sbin/init\")",
		"EXEC_CMD":         fmt.Sprintf("command to run via exec/ssh (default %q)", defaultExecCmd),
		"MEMORY":           "memory to allocate to each container (0 disables allocation, default \"4G\")",
		"SHM":              "/dev/shm to allocate to each container (0 disables allocation, default \"64MiB\")",
		"CPUS":             "cpu count to allocate to each container (0 disables allocation, default 2)",
		"CPU_SET_SIZE":     "size of available cpu set (default detected locally via runtime.NumCPU)",
		"NATIVE":           "upload and run build script via docker API instead of over ssh (default false)",
		"PRIVILEGED":       "run containers in privileged mode (default false)",
		"SSH_DIAL_TIMEOUT": fmt.Sprintf("connection timeout for ssh connections (default %v)", defaultDockerSSHDialTimeout),
	}
)

func init() {
	Register("docker", "Docker", dockerHelp, newDockerProvider)
}

type dockerNumCPUer interface {
	NumCPU() int
}

type stdlibNumCPUer struct{}

func (nc *stdlibNumCPUer) NumCPU() int {
	return runtime.NumCPU()
}

type dockerProvider struct {
	client         *docker.Client
	sshDialer      ssh.Dialer
	sshDialTimeout time.Duration

	runPrivileged bool
	runCmd        []string
	runMemory     uint64
	runShm        uint64
	runCPUs       int
	runNative     bool
	execCmd       []string

	cpuSetsMutex sync.Mutex
	cpuSets      []bool
}

type dockerInstance struct {
	client       *docker.Client
	provider     *dockerProvider
	container    *docker.Container
	startBooting time.Time

	imageName string
	runNative bool
}

func newDockerProvider(cfg *config.ProviderConfig) (Provider, error) {
	client, err := buildDockerClient(cfg)
	if err != nil {
		return nil, err
	}

	runNative := false
	if cfg.IsSet("NATIVE") {
		v, err := strconv.ParseBool(cfg.Get("NATIVE"))
		if err != nil {
			return nil, err
		}

		runNative = v
	}

	cpuSetSize := 0

	if defaultDockerNumCPUer != nil {
		cpuSetSize = defaultDockerNumCPUer.NumCPU()
	}

	if cfg.IsSet("CPU_SET_SIZE") {
		v, err := strconv.ParseInt(cfg.Get("CPU_SET_SIZE"), 10, 64)
		if err != nil {
			return nil, err
		}
		cpuSetSize = int(v)
	}

	if cpuSetSize < 2 {
		cpuSetSize = 2
	}

	privileged := false
	if cfg.IsSet("PRIVILEGED") {
		v, err := strconv.ParseBool(cfg.Get("PRIVILEGED"))
		if err != nil {
			return nil, err
		}
		privileged = v
	}

	cmd := []string{"/sbin/init"}
	if cfg.IsSet("CMD") {
		cmd = strings.Split(cfg.Get("CMD"), " ")
	}

	execCmd := strings.Split(defaultExecCmd, " ")
	if cfg.IsSet("EXEC_CMD") {
		execCmd = strings.Split(cfg.Get("EXEC_CMD"), " ")
	}

	memory := uint64(1024 * 1024 * 1024 * 4)
	if cfg.IsSet("MEMORY") {
		if parsedMemory, err := humanize.ParseBytes(cfg.Get("MEMORY")); err == nil {
			memory = parsedMemory
		}
	}

	shm := uint64(1024 * 1024 * 64)
	if cfg.IsSet("SHM") {
		if parsedShm, err := humanize.ParseBytes(cfg.Get("SHM")); err == nil {
			shm = parsedShm
		}
	}

	cpus := uint64(2)
	if cfg.IsSet("CPUS") {
		if parsedCPUs, err := strconv.ParseUint(cfg.Get("CPUS"), 10, 64); err == nil {
			cpus = parsedCPUs
		}
	}

	sshDialTimeout := defaultDockerSSHDialTimeout
	if cfg.IsSet("SSH_DIAL_TIMEOUT") {
		sshDialTimeout, err = time.ParseDuration(cfg.Get("SSH_DIAL_TIMEOUT"))
		if err != nil {
			return nil, err
		}
	}

	sshDialer, err := ssh.NewDialerWithPassword("travis")
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create SSH dialer")
	}

	return &dockerProvider{
		client:         client,
		sshDialer:      sshDialer,
		sshDialTimeout: sshDialTimeout,

		runPrivileged: privileged,
		runCmd:        cmd,
		runMemory:     memory,
		runShm:        shm,
		runCPUs:       int(cpus),
		runNative:     runNative,

		execCmd: execCmd,

		cpuSets: make([]bool, cpuSetSize),
	}, nil
}

func buildDockerClient(cfg *config.ProviderConfig) (*docker.Client, error) {
	// check for both DOCKER_ENDPOINT and DOCKER_HOST, the latter for
	// compatibility with docker's own env vars.
	if !cfg.IsSet("ENDPOINT") && !cfg.IsSet("HOST") {
		return nil, ErrMissingEndpointConfig
	}

	endpoint := cfg.Get("ENDPOINT")
	if endpoint == "" {
		endpoint = cfg.Get("HOST")
	}

	if cfg.IsSet("CERT_PATH") {
		path := cfg.Get("CERT_PATH")
		ca := fmt.Sprintf("%s/ca.pem", path)
		cert := fmt.Sprintf("%s/cert.pem", path)
		key := fmt.Sprintf("%s/key.pem", path)
		return docker.NewTLSClient(endpoint, cert, key, ca)
	}

	return docker.NewClient(endpoint)
}

func (p *dockerProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	logger := context.LoggerFromContext(ctx)

	cpuSets, err := p.checkoutCPUSets()
	if err != nil && cpuSets != "" {
		return nil, err
	}

	imageID, imageName, err := p.imageForLanguage(startAttributes.Language)
	if err != nil {
		return nil, err
	}

	dockerConfig := &docker.Config{
		Cmd:      p.runCmd,
		Image:    imageID,
		Memory:   int64(p.runMemory),
		Hostname: fmt.Sprintf("testing-docker-%s", uuid.NewRandom()),
	}

	dockerHostConfig := &docker.HostConfig{
		Privileged: p.runPrivileged,
		Memory:     int64(p.runMemory),
		ShmSize:    int64(p.runShm),
		Tmpfs:		map[string]string{"/run": "rw,nosuid,nodev,exec,noatime,size=65536k"},
	}

	if cpuSets != "" {
		dockerConfig.CPUSet = cpuSets
		dockerHostConfig.CPUSet = cpuSets
	}

	logger.WithFields(logrus.Fields{
		"config":      fmt.Sprintf("%#v", dockerConfig),
		"host_config": fmt.Sprintf("%#v", dockerHostConfig),
	}).Debug("starting container")

	container, err := p.client.CreateContainer(docker.CreateContainerOptions{
		Config:     dockerConfig,
		HostConfig: dockerHostConfig,
	})

	if err != nil {
		if container != nil {
			err := p.client.RemoveContainer(docker.RemoveContainerOptions{
				ID:            container.ID,
				RemoveVolumes: true,
				Force:         true,
			})
			if err != nil {
				logger.WithField("err", err).Error("couldn't remove container after create failure")
			}
		}

		return nil, err
	}

	startBooting := time.Now()

	err = p.client.StartContainer(container.ID, dockerHostConfig)
	if err != nil {
		return nil, err
	}

	containerReady := make(chan *docker.Container)
	errChan := make(chan error)
	go func(id string) {
		for {
			container, err := p.client.InspectContainer(id)
			if err != nil {
				errChan <- err
				return
			}

			if container.State.Running {
				containerReady <- container
				return
			}
		}
	}(container.ID)

	select {
	case container := <-containerReady:
		metrics.TimeSince("worker.vm.provider.docker.boot", startBooting)
		return &dockerInstance{
			client:       p.client,
			provider:     p,
			runNative:    p.runNative,
			container:    container,
			imageName:    imageName,
			startBooting: startBooting,
		}, nil
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.docker.boot.timeout")
		}
		return nil, ctx.Err()
	}
}

func (p *dockerProvider) Setup(ctx gocontext.Context) error { return nil }

func (p *dockerProvider) imageForLanguage(language string) (string, string, error) {
	images, err := p.client.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return "", "", err
	}

	for _, searchTag := range []string{
		"travis:" + language,
		language,
		"travis:default",
		"default",
	} {
		for _, image := range images {
			for _, tag := range image.RepoTags {
				if tag == searchTag {
					return image.ID, tag, nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("no image found with language %s", language)
}

func (p *dockerProvider) checkoutCPUSets() (string, error) {
	p.cpuSetsMutex.Lock()
	defer p.cpuSetsMutex.Unlock()

	cpuSets := []int{}

	for i, checkedOut := range p.cpuSets {
		if !checkedOut {
			cpuSets = append(cpuSets, i)
		}

		if len(cpuSets) == p.runCPUs {
			break
		}
	}

	if len(cpuSets) != p.runCPUs {
		return "", fmt.Errorf("not enough free CPUsets")
	}

	cpuSetsString := []string{}

	for _, cpuSet := range cpuSets {
		p.cpuSets[cpuSet] = true
		cpuSetsString = append(cpuSetsString, fmt.Sprintf("%d", cpuSet))
	}

	return strings.Join(cpuSetsString, ","), nil
}

func (p *dockerProvider) checkinCPUSets(sets string) {
	p.cpuSetsMutex.Lock()
	defer p.cpuSetsMutex.Unlock()

	for _, cpuString := range strings.Split(sets, ",") {
		cpu, err := strconv.ParseUint(cpuString, 10, 64)
		if err != nil {
			continue
		}
		p.cpuSets[int(cpu)] = false
	}
}

func (i *dockerInstance) sshConnection() (ssh.Connection, error) {
	var err error
	i.container, err = i.client.InspectContainer(i.container.ID)
	if err != nil {
		return nil, err
	}

	time.Sleep(2 * time.Second)

	return i.provider.sshDialer.Dial(fmt.Sprintf("%s:22", i.container.NetworkSettings.IPAddress), "travis", i.provider.sshDialTimeout)
}

func (i *dockerInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	if i.runNative {
		return i.uploadScriptNative(ctx, script)
	}
	return i.uploadScriptSCP(ctx, script)
}

func (i *dockerInstance) uploadScriptNative(ctx gocontext.Context, script []byte) error {
	tarBuf := &bytes.Buffer{}
	tw := tar.NewWriter(tarBuf)
	err := tw.WriteHeader(&tar.Header{
		Name: "/home/travis/build.sh",
		Mode: 0755,
		Size: int64(len(script)),
	})
	if err != nil {
		return err
	}
	_, err = tw.Write(script)
	if err != nil {
		return err
	}
	err = tw.Close()
	if err != nil {
		return err
	}

	uploadOpts := docker.UploadToContainerOptions{
		InputStream: bytes.NewReader(tarBuf.Bytes()),
		Path:        "/",
	}

	return i.client.UploadToContainer(i.container.ID, uploadOpts)
}

func (i *dockerInstance) uploadScriptSCP(ctx gocontext.Context, script []byte) error {
	conn, err := i.sshConnection()
	if err != nil {
		return err
	}
	defer conn.Close()

	existed, err := conn.UploadFile("build.sh", script)
	if existed {
		return ErrStaleVM
	}
	if err != nil {
		return errors.Wrap(err, "couldn't upload build script")
	}

	return nil
}

func (i *dockerInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	if i.runNative {
		return i.runScriptExec(ctx, output)
	}
	return i.runScriptSSH(ctx, output)
}

func (i *dockerInstance) runScriptExec(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	logger := context.LoggerFromContext(ctx)
	createExecOpts := docker.CreateExecOptions{
		AttachStdin:  false,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          i.provider.execCmd,
		User:         "travis",
		Container:    i.container.ID,
	}
	exec, err := i.client.CreateExec(createExecOpts)
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	successChan := make(chan struct{})

	startExecOpts := docker.StartExecOptions{
		Detach:       false,
		Success:      successChan,
		Tty:          true,
		OutputStream: output,
		ErrorStream:  output,

		// IMPORTANT!  If this is false, then
		// github.com/docker/docker/pkg/stdcopy.StdCopy is used instead of io.Copy,
		// which will result in busted behavior.
		RawTerminal: true,
	}

	go func() {
		err := i.client.StartExec(exec.ID, startExecOpts)
		if err != nil {
			// not much to be done about it, though...
			logger.WithField("err", err).Error("start exec error")
		}
	}()

	<-successChan
	logger.Debug("exec success; returning control to hijacked streams")
	successChan <- struct{}{}

	for {
		inspect, err := i.client.InspectExec(exec.ID)
		if err != nil {
			return &RunResult{Completed: false}, err
		}

		if !inspect.Running {
			return &RunResult{Completed: true, ExitCode: uint8(inspect.ExitCode)}, nil
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func (i *dockerInstance) runScriptSSH(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	conn, err := i.sshConnection()
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	exitStatus, err := conn.RunCommand(strings.Join(i.provider.execCmd, " "), output)

	return &RunResult{Completed: err != nil, ExitCode: exitStatus}, errors.Wrap(err, "error running script")
}

func (i *dockerInstance) Stop(ctx gocontext.Context) error {
	defer i.provider.checkinCPUSets(i.container.Config.CPUSet)

	err := i.client.StopContainer(i.container.ID, 30)
	if err != nil {
		return err
	}

	return i.client.RemoveContainer(docker.RemoveContainerOptions{
		ID:            i.container.ID,
		RemoveVolumes: true,
		Force:         true,
	})
}

func (i *dockerInstance) ID() string {
	if i.container == nil {
		return "{unidentified}"
	}

	return fmt.Sprintf("%s:%s", i.container.ID[0:7], i.imageName)
}

func (i *dockerInstance) StartupDuration() time.Duration {
	if i.container == nil {
		return zeroDuration
	}
	return i.startBooting.Sub(i.container.Created)
}
