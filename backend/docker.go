package backend

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	gocontext "context"

	dockertypes "github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	docker "github.com/docker/docker/client"
	"github.com/docker/go-connections/tlsconfig"
	humanize "github.com/dustin/go-humanize"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ssh"
)

const (
	defaultDockerImageSelectorType = "tag"

	// DockerMinSupportedAPIVersion of 1.24 means the client library in use here
	// can only support docker-engine 1.12 and above
	// (https://docs.docker.com/release-notes/docker-engine/#1120-2016-07-28).
	DockerMinSupportedAPIVersion = "1.24"
)

var (
	defaultDockerNumCPUer       dockerNumCPUer = &stdlibNumCPUer{}
	defaultDockerSSHDialTimeout                = 5 * time.Second
	defaultInspectInterval                     = 500 * time.Millisecond
	defaultExecCmd                             = "bash /home/travis/build.sh"
	defaultTmpfsMap                            = map[string]string{"/run": "rw,nosuid,nodev,exec,noatime,size=65536k"}
	dockerHelp                                 = map[string]string{
		"ENDPOINT / HOST":     "[REQUIRED] tcp or unix address for connecting to Docker",
		"CERT_PATH":           "directory where ca.pem, cert.pem, and key.pem are located (default \"\")",
		"CMD":                 "command (CMD) to run when creating containers (default \"/sbin/init\")",
		"EXEC_CMD":            fmt.Sprintf("command to run via exec/ssh (default %q)", defaultExecCmd),
		"INSPECT_INTERVAL":    fmt.Sprintf("time to wait between container inspections as duration (default %q)", defaultInspectInterval),
		"TMPFS_MAP":           fmt.Sprintf("\"+\"-delimited key:value map of tmpfs mounts (example \"/run:rw,exec+/run/lock:rw,exec\", default %q)", defaultTmpfsMap),
		"MEMORY":              "memory to allocate to each container (0 disables allocation, default \"4G\")",
		"SHM":                 "/dev/shm to allocate to each container (0 disables allocation, default \"64MiB\")",
		"CONTAINER_LABELS":    "comma- or space-delimited key:value pairs of labels to apply to each container (default \"\")",
		"CPUS":                "cpu count to allocate to each container (0 disables allocation, default 2)",
		"CPU_SET_SIZE":        "size of available cpu set (default detected locally via runtime.NumCPU)",
		"NATIVE":              "upload and run build script via docker API instead of over ssh (default false)",
		"PRIVILEGED":          "run containers in privileged mode (default false)",
		"SSH_DIAL_TIMEOUT":    fmt.Sprintf("connection timeout for ssh connections (default %v)", defaultDockerSSHDialTimeout),
		"IMAGE_SELECTOR_TYPE": fmt.Sprintf("image selector type (\"tag\", \"api\", or \"env\", default %q)", defaultDockerImageSelectorType),
		"IMAGE_SELECTOR_URL":  "URL for image selector API, used only when image selector is \"api\"",
		"BINDS":               "Bind mount a volume (example: \"/var/run/docker.sock:/var/run/docker.sock\", default \"\")",
		"SECURITY_OPT":        "Security configuration (example: \"seccomp=unconfined\") to turn off seccomp confinement for the container",
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
	client         docker.CommonAPIClient
	sshDialer      ssh.Dialer
	sshDialTimeout time.Duration

	runPrivileged   bool
	runCmd          []string
	runBinds        []string
	setSecurityOpt  []string
	runMemory       uint64
	runShm          uint64
	runCPUs         uint
	runNative       bool
	execCmd         []string
	inspectInterval time.Duration
	tmpFs           map[string]string
	imageSelector   image.Selector
	containerLabels map[string]string

	httpProxy, httpsProxy, ftpProxy, noProxy string

	cpuSetsMutex sync.Mutex
	cpuSets      []bool
}

type dockerInstance struct {
	client       docker.CommonAPIClient
	provider     *dockerProvider
	container    *dockertypes.ContainerJSON
	startBooting time.Time

	imageName string
	runNative bool
}

type dockerTagImageSelector struct {
	client *docker.Client
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

	inspectInterval := defaultInspectInterval
	if cfg.IsSet("INSPECT_INTERVAL") {
		v, err := time.ParseDuration(cfg.Get("INSPECT_INTERVAL"))
		if err != nil {
			return nil, err
		}
		inspectInterval = v
	}

	binds := []string{}
	if cfg.IsSet("BINDS") {
		binds = strings.Split(cfg.Get("BINDS"), " ")
	}

	securityOpt := []string{}
	if cfg.IsSet("SECURITY_OPT") {
		securityOpt = strings.Split(cfg.Get("SECURITY_OPT"), " ")
	}

	tmpFs := str2map(cfg.Get("TMPFS_MAP"), " ")
	if len(tmpFs) == 0 {
		tmpFs = defaultTmpfsMap
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

	imageSelectorType := defaultDockerImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	imageSelector, err := buildDockerImageSelector(imageSelectorType, client, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't build docker image selector")
	}

	containerLabels := map[string]string{}
	if cfg.IsSet("CONTAINER_LABELS") {
		containerLabels = str2map(cfg.Get("CONTAINER_LABELS"), " ,")
	}

	httpProxy := cfg.Get("HTTP_PROXY")
	httpsProxy := cfg.Get("HTTPS_PROXY")
	ftpProxy := cfg.Get("FTP_PROXY")
	noProxy := cfg.Get("NO_PROXY")

	return &dockerProvider{
		client:         client,
		sshDialer:      sshDialer,
		sshDialTimeout: sshDialTimeout,

		runPrivileged:   privileged,
		runCmd:          cmd,
		runBinds:        binds,
		setSecurityOpt:  securityOpt,
		runMemory:       memory,
		runShm:          shm,
		runCPUs:         uint(cpus),
		runNative:       runNative,
		imageSelector:   imageSelector,
		containerLabels: containerLabels,

		httpProxy:  httpProxy,
		httpsProxy: httpsProxy,
		ftpProxy:   ftpProxy,
		noProxy:    noProxy,

		execCmd:         execCmd,
		inspectInterval: inspectInterval,
		tmpFs:           tmpFs,

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

	var httpClient *http.Client
	if cfg.IsSet("CERT_PATH") {
		certPath := cfg.Get("CERT_PATH")
		tlsOptions := tlsconfig.Options{
			CAFile:             filepath.Join(certPath, "ca.pem"),
			CertFile:           filepath.Join(certPath, "cert.pem"),
			KeyFile:            filepath.Join(certPath, "key.pem"),
			InsecureSkipVerify: cfg.Get("TLS_VERIFY") == "",
		}

		tlsc, err := tlsconfig.Client(tlsOptions)
		if err != nil {
			return nil, err
		}

		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsc,
			},
			CheckRedirect: docker.CheckRedirect,
		}
	}

	dockerAPIVersion := DockerMinSupportedAPIVersion
	if cfg.IsSet("API_VERSION") {
		dockerAPIVersion = cfg.Get("API_VERSION")
	}

	return docker.NewClientWithOpts(docker.WithHost(endpoint), docker.WithVersion(dockerAPIVersion), docker.WithHTTPClient(httpClient), docker.WithHTTPHeaders(nil))
}

func buildDockerImageSelector(selectorType string, client *docker.Client, cfg *config.ProviderConfig) (image.Selector, error) {
	switch selectorType {
	case "tag":
		return &dockerTagImageSelector{client: client}, nil
	case "env":
		return image.NewEnvSelector(cfg)
	case "api":
		baseURL, err := url.Parse(cfg.Get("IMAGE_SELECTOR_URL"))
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse image selector URL")
		}
		return image.NewAPISelector(baseURL), nil
	default:
		return nil, fmt.Errorf("invalid image selector type %q", selectorType)
	}
}

// dockerImageNameForID returns a human-readable name for the image with the requested ID.
// Currently, we are using the tag that includes the stack-name (e.g "travisci/ci-garnet:packer-1505167479") and reverting back to the ID if nothing is found.
func (p *dockerProvider) dockerImageNameForID(ctx gocontext.Context, imageID string) string {
	images, err := p.client.ImageList(ctx, dockerimage.ListOptions{All: true})
	if err != nil {
		return imageID
	}
	for _, image := range images {
		if image.ID == imageID {
			for _, tag := range image.RepoTags {
				if strings.HasPrefix(tag, "travisci/ci-") {
					return tag
				}
			}
		}
	}
	return imageID
}

func (p *dockerProvider) SupportsProgress() bool {
	return false
}

func (p *dockerProvider) StartWithProgress(ctx gocontext.Context, startAttributes *StartAttributes, _ Progresser) (Instance, error) {
	return p.Start(ctx, startAttributes)
}

func (p *dockerProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	var (
		imageID   string
		imageName string
	)

	logger := context.LoggerFromContext(ctx).WithField("self", "backend/docker_provider")

	if startAttributes.ImageName != "" {
		imageName = startAttributes.ImageName
	} else {
		selectedImageID, err := p.imageSelector.Select(ctx, &image.Params{
			Language: startAttributes.Language,
			Infra:    "docker",
		})
		if err != nil {
			logger.WithField("err", err).Error("couldn't select image")
			return nil, err
		}
		imageID = selectedImageID
		imageName = p.dockerImageNameForID(ctx, imageID)
	}

	containerName := hostnameFromContext(ctx)
	existingContainer, err := p.client.ContainerInspect(ctx, containerName)
	if err == nil {
		err := p.client.ContainerRemove(ctx, existingContainer.ID,
			dockercontainer.RemoveOptions{
				Force:         true,
				RemoveLinks:   false,
				RemoveVolumes: true,
			})
		if err != nil {
			logger.WithField("err", err).Error("couldn't remove preexisting container before create")
		} else {
			logger.Warn("removed preexisting container before create")
		}
	}

	labels := map[string]string{
		"travis.dist": startAttributes.Dist,
	}
	for key, value := range p.containerLabels {
		labels[key] = value
	}

	r, ok := context.RepositoryFromContext(ctx)
	if ok {
		labels["travis.repo"] = r
	}

	jid, ok := context.JobIDFromContext(ctx)
	if ok {
		labels["travis.job_id"] = strconv.FormatUint(jid, 10)
	}

	dockerConfig := &dockercontainer.Config{
		Cmd:        p.runCmd,
		Image:      imageID,
		Hostname:   strings.ToLower(containerName),
		Domainname: "travisci.net",
		Labels:     labels,
	}

	dockerHostConfig := &dockercontainer.HostConfig{
		Binds:      p.runBinds,
		Privileged: p.runPrivileged,
		Tmpfs:      p.tmpFs,
		ShmSize:    int64(p.runShm),
		Resources: dockercontainer.Resources{
			Memory: int64(p.runMemory),
		},
	}

	if len(p.setSecurityOpt) > 0 {
		dockerHostConfig.SecurityOpt = p.setSecurityOpt
	}

	useCPUSets := p.runCPUs != uint(0)
	cpuSets := ""

	if useCPUSets {
		cpuSets, err = p.checkoutCPUSets(ctx)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"err":            err,
				"cpu_set_length": len(p.cpuSets),
				"run_cpus":       p.runCPUs,
			}).Error("couldn't checkout CPUSets")
			return nil, err
		}

		if cpuSets != "" {
			dockerHostConfig.Resources.CpusetCpus = cpuSets
		}
	}

	logger.WithFields(logrus.Fields{
		"config":      fmt.Sprintf("%#v", dockerConfig),
		"host_config": fmt.Sprintf("%#v", dockerHostConfig),
	}).Debug("creating container")

	container, err := p.client.ContainerCreate(
		ctx, dockerConfig, dockerHostConfig, nil, nil, containerName)

	if err != nil {
		logger.WithField("err", err).Error("couldn't create container")
		if useCPUSets {
			p.checkinCPUSets(ctx, cpuSets)
		}

		err := p.client.ContainerRemove(ctx, container.ID,
			dockercontainer.RemoveOptions{
				Force:         true,
				RemoveLinks:   false,
				RemoveVolumes: true,
			})
		if err != nil {
			logger.WithField("err", err).Error("couldn't remove container after create failure")
		}

		return nil, err
	}

	startBooting := time.Now()

	err = p.client.ContainerStart(ctx, container.ID, dockercontainer.StartOptions{})
	if err != nil {
		logger.WithField("err", err).Error("couldn't start container")
		if useCPUSets {
			p.checkinCPUSets(ctx, cpuSets)
		}
		return nil, err
	}

	containerReady := make(chan dockertypes.ContainerJSON)
	errChan := make(chan error)
	go func(id string) {
		for {
			container, err := p.client.ContainerInspect(ctx, id)
			if err != nil {
				errChan <- err
				return
			}

			if container.State != nil && container.State.Running {
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
			container:    &container,
			imageName:    imageName,
			startBooting: startBooting,
		}, nil
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			if useCPUSets {
				p.checkinCPUSets(ctx, cpuSets)
			}
			metrics.Mark("worker.vm.provider.docker.boot.timeout")
		}
		return nil, ctx.Err()
	}
}

func (p *dockerProvider) Setup(ctx gocontext.Context) error { return nil }

func (p *dockerProvider) checkoutCPUSets(ctx gocontext.Context) (string, error) {
	p.cpuSetsMutex.Lock()
	defer p.cpuSetsMutex.Unlock()

	cpuSets := []int{}

	for i, checkedOut := range p.cpuSets {
		if !checkedOut {
			cpuSets = append(cpuSets, i)
		}

		if len(cpuSets) == int(p.runCPUs) {
			break
		}
	}

	if len(cpuSets) != int(p.runCPUs) {
		return "", fmt.Errorf("not enough free CPUsets")
	}

	cpuSetsString := []string{}

	for _, cpuSet := range cpuSets {
		p.cpuSets[cpuSet] = true
		cpuSetsString = append(cpuSetsString, fmt.Sprintf("%d", cpuSet))
	}

	logger := context.LoggerFromContext(ctx).WithField("self", "backend/docker_provider")
	logger.WithField("cpu_sets", cpuSetsString).Info("checked out")
	return strings.Join(cpuSetsString, ","), nil
}

func (p *dockerProvider) checkinCPUSets(ctx gocontext.Context, sets string) {
	p.cpuSetsMutex.Lock()
	defer p.cpuSetsMutex.Unlock()
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/docker_provider")

	for _, cpuString := range strings.Split(sets, ",") {
		cpu, err := strconv.ParseUint(cpuString, 10, 64)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"err":        err,
				"cpu_string": cpuString,
			}).Error("couldn't parse CPU string; CPU set not checked in")
			continue
		}

		if !p.cpuSets[int(cpu)] {
			logger.WithField("cpu_set", cpuString).Info("already checked in")
			continue
		}

		p.cpuSets[int(cpu)] = false
	}
	logger.WithField("cpu_sets", sets).Info("checked in")
}

func (i *dockerInstance) sshConnection(ctx gocontext.Context) (ssh.Connection, error) {
	var err error
	container, err := i.client.ContainerInspect(ctx, i.container.ID)
	if err != nil {
		return nil, err
	}
	i.container = &container

	time.Sleep(2 * time.Second)

	return i.provider.sshDialer.Dial(fmt.Sprintf("%s:22", i.container.NetworkSettings.IPAddress), "travis", i.provider.sshDialTimeout)
}

func (i *dockerInstance) Warmed() bool {
	return false
}

func (i *dockerInstance) SupportsProgress() bool {
	return false
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

	return i.client.CopyToContainer(ctx, i.container.ID, "/",
		bytes.NewReader(tarBuf.Bytes()), dockercontainer.CopyToContainerOptions{})
}

func (i *dockerInstance) uploadScriptSCP(ctx gocontext.Context, script []byte) error {
	conn, err := i.sshConnection(ctx)
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
	execConfig := dockercontainer.ExecOptions{
		AttachStdin:  false,
		AttachStdout: true,
		AttachStderr: true,
		Detach:       false,
		Tty:          true,
		Cmd:          i.provider.execCmd,
		User:         "travis",
	}

	if i.provider.httpProxy != "" {
		execConfig.Env = append(execConfig.Env, "HTTP_PROXY="+i.provider.httpProxy)
		execConfig.Env = append(execConfig.Env, "http_proxy="+i.provider.httpProxy)
	}
	if i.provider.httpsProxy != "" {
		execConfig.Env = append(execConfig.Env, "HTTPS_PROXY="+i.provider.httpsProxy)
		execConfig.Env = append(execConfig.Env, "https_proxy="+i.provider.httpsProxy)
	}
	if i.provider.ftpProxy != "" {
		execConfig.Env = append(execConfig.Env, "FTP_PROXY="+i.provider.ftpProxy)
		execConfig.Env = append(execConfig.Env, "ftp_proxy="+i.provider.ftpProxy)
	}
	if i.provider.noProxy != "" {
		execConfig.Env = append(execConfig.Env, "NO_PROXY="+i.provider.noProxy)
		execConfig.Env = append(execConfig.Env, "no_proxy="+i.provider.noProxy)
	}

	exec, err := i.client.ContainerExecCreate(ctx, i.container.ID, execConfig)
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	hijackedResponse, err := i.client.ContainerExecAttach(ctx, exec.ID, dockercontainer.ExecStartOptions{
		Detach: execConfig.Detach,
		Tty:    execConfig.Tty,
	})
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	defer hijackedResponse.Close()

	tee := io.TeeReader(hijackedResponse.Reader, output)
	firstByte := make(chan struct{})
	go func() {
		buf := make([]byte, 8192)
		didFirstByte := false
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, _ := tee.Read(buf)
			if n != 0 && !didFirstByte {
				firstByte <- struct{}{}
				didFirstByte = true
			}
		}
	}()

	<-firstByte
	for {
		inspect, err := i.client.ContainerExecInspect(ctx, exec.ID)
		if err != nil {
			return &RunResult{Completed: false}, err
		}

		if !inspect.Running {
			return &RunResult{Completed: true, ExitCode: int32(inspect.ExitCode)}, nil
		}

		select {
		case <-time.After(i.provider.inspectInterval):
			continue
		case <-ctx.Done():
			return &RunResult{Completed: false}, ctx.Err()
		}
	}
}

func (i *dockerInstance) runScriptSSH(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	conn, err := i.sshConnection(ctx)
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	exitStatus, err := conn.RunCommand(strings.Join(i.provider.execCmd, " "), output)

	return &RunResult{Completed: err != nil, ExitCode: exitStatus}, errors.Wrap(err, "error running script")
}

func (i *dockerInstance) DownloadTrace(ctx gocontext.Context) ([]byte, error) {
	if i.runNative {
		return i.downloadTraceNative(ctx)
	}
	return i.downloadTraceSSH(ctx)
}

func (i *dockerInstance) downloadTraceNative(ctx gocontext.Context) ([]byte, error) {
	r, _, err := i.client.CopyFromContainer(ctx, i.container.ID, "/tmp/build.trace")
	if r != nil {
		defer r.Close()
	}
	if err != nil {
		return nil, errors.Wrap(err, "couldn't copy trace from container")
	}

	found := false

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "couldn't parse tar")
		}

		if hdr.Name == "build.trace" {
			found = true
			break
		}
	}

	if !found {
		return nil, errors.Wrap(err, "couldn't find trace in tar")
	}

	buf, err := io.ReadAll(tr)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't read contents of file")
	}

	return buf, nil
}

func (i *dockerInstance) downloadTraceSSH(ctx gocontext.Context) ([]byte, error) {
	conn, err := i.sshConnection(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	buf, err := conn.DownloadFile("/tmp/build.trace")
	if err != nil {
		return nil, errors.Wrap(err, "couldn't download trace")
	}

	return buf, nil
}

func (i *dockerInstance) StopOnly(ctx gocontext.Context) error {
	return nil
}

func (i *dockerInstance) CreateImage(ctx gocontext.Context, createCustomImageName string, logWriterFunc func(string)) (int64, string, string, error) {
	return 0, "", "", nil
}

func (i *dockerInstance) Stop(ctx gocontext.Context) error {
	defer i.provider.checkinCPUSets(ctx, i.container.HostConfig.Resources.CpusetCpus)
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/docker_provider")

	timeout := 30
	err := i.client.ContainerStop(ctx, i.container.ID, dockercontainer.StopOptions{Timeout: &timeout})
	if err != nil {
		logger.Warn("couldn't stop container")
		return err
	}

	return i.client.ContainerRemove(ctx, i.container.ID,
		dockercontainer.RemoveOptions{
			Force:         true,
			RemoveLinks:   false,
			RemoveVolumes: true,
		})
}

func (i *dockerInstance) ID() string {
	if i.container == nil {
		return "{unidentified}"
	}

	return i.container.ID[0:7]
}

func (i *dockerInstance) ImageName() string {
	return i.imageName
}

func (i *dockerInstance) StartupDuration() time.Duration {
	if i.container == nil {
		return zeroDuration
	}
	containerCreated, err := time.Parse(time.RFC3339Nano, i.container.Created)
	if err != nil {
		return zeroDuration
	}
	return i.startBooting.Sub(containerCreated)
}

func (s *dockerTagImageSelector) Select(ctx gocontext.Context, params *image.Params) (string, error) {
	images, err := s.client.ImageList(ctx, dockerimage.ListOptions{All: true})
	if err != nil {
		return "", errors.Wrap(err, "failed to list docker images")
	}

	imageID, err := findDockerImageByTag([]string{
		"travis:" + params.Language,
		params.Language,
		"travis:default",
		"default",
	}, images)

	return imageID, err
}

// findDockerImageByTag returns the ID of the image which matches the requested search tags
func findDockerImageByTag(searchTags []string, images []dockerimage.Summary) (string, error) {
	for _, searchTag := range searchTags {
		for _, image := range images {
			if searchTag == image.ID {
				return image.ID, nil
			}
			for _, tag := range image.RepoTags {
				if tag == searchTag {
					return image.ID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("failed to find matching docker image tag")
}
