package backend

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/dustin/go-humanize"
	"github.com/pborman/uuid"

	"github.com/fsouza/go-dockerclient"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
)

var (
	dockerHelp = map[string]string{
		"ENDPOINT / HOST": "[REQUIRED] tcp or unix address for connecting to Docker",
		"CERT_PATH":       "directory where ca.pem, cert.pem, and key.pem are located (default \"\")",
		"CMD":             "command (CMD) to run when creating containers (default \"/sbin/init\")",
		"MEMORY":          "memory to allocate to each container (default \"4G\")",
		"CPUS":            "cpu count to allocate to each container (default 2)",
		"PRIVILEGED":      "run containers in privileged mode (default false)",
	}
)

func init() {
	RegisterBackend("docker", "Docker", dockerHelp, newDockerProvider)
}

type dockerProvider struct {
	client *docker.Client

	runPrivileged bool
	runCmd        []string
	runMemory     uint64
	runCPUs       int

	cpuSetsMutex sync.Mutex
	cpuSets      []bool
}

type dockerInstance struct {
	client    *docker.Client
	provider  *dockerProvider
	container *docker.Container

	imageName string
}

func newDockerProvider(cfg *config.ProviderConfig) (Provider, error) {
	client, err := buildDockerClient(cfg)
	if err != nil {
		return nil, err
	}

	cpuSetSize := runtime.NumCPU()
	if cpuSetSize < 2 {
		cpuSetSize = 2
	}

	privileged := false
	if cfg.IsSet("PRIVILEGED") {
		privileged = (cfg.Get("PRIVILEGED") == "true")
	}

	cmd := []string{"/sbin/init"}
	if cfg.IsSet("CMD") {
		cmd = strings.Split(cfg.Get("CMD"), " ")
	}

	memory := uint64(1024 * 1024 * 1024 * 4)
	if cfg.IsSet("MEMORY") {
		if parsedMemory, err := humanize.ParseBytes(cfg.Get("MEMORY")); err == nil {
			memory = parsedMemory
		}
	}

	cpus := uint64(2)
	if cfg.IsSet("CPUS") {
		if parsedCPUs, err := strconv.ParseUint(cfg.Get("CPUS"), 10, 64); err == nil {
			cpus = parsedCPUs
		}
	}

	return &dockerProvider{
		client: client,

		runPrivileged: privileged,
		runCmd:        cmd,
		runMemory:     memory,
		runCPUs:       int(cpus),

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
		Hostname: fmt.Sprintf("testing-docker-%s", uuid.NewUUID()),
	}

	dockerHostConfig := &docker.HostConfig{
		Privileged: p.runPrivileged,
	}

	if cpuSets != "" {
		dockerConfig.CPUSet = cpuSets
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

	err = p.client.StartContainer(container.ID, &docker.HostConfig{})
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
			client:    p.client,
			provider:  p,
			container: container,
			imageName: imageName,
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

func (p *dockerProvider) imageForLanguage(language string) (string, string, error) {
	images, err := p.client.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return "", "", err
	}

	for _, image := range images {
		for _, searchTag := range []string{
			"travis:" + language,
			language,
			"travis:default",
			"default",
		} {
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

func (i *dockerInstance) sshClient() (*ssh.Client, error) {
	var err error
	i.container, err = i.client.InspectContainer(i.container.ID)
	if err != nil {
		return nil, err
	}

	time.Sleep(2 * time.Second)

	return ssh.Dial("tcp", fmt.Sprintf("%s:22", i.container.NetworkSettings.IPAddress), &ssh.ClientConfig{
		User: "travis",
		Auth: []ssh.AuthMethod{
			ssh.Password("travis"),
		},
	})
}

func (i *dockerInstance) UploadScript(ctx gocontext.Context, script []byte) error {
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

func (i *dockerInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
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
