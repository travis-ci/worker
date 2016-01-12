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
	"github.com/codegangsta/cli"
	"github.com/dustin/go-humanize"
	"github.com/fsouza/go-dockerclient"
	"github.com/pborman/uuid"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
)

var (
	dockerFlags = []cli.Flag{
		backendStringFlag("docker", "endpoint, host", "unix:///var/run/docker.sock",
			"ENDPOINT", "TCP or unix address for connecting to Docker"),
		backendStringFlag("docker", "cert-path", "",
			"CERT_PATH", "Directory where ca.pem, cert.pem, and key.pem are located"),
		backendStringFlag("docker", "cmd", "/sbin/init",
			"CMD", "Command (CMD) to run when creating containers"),
		backendStringFlag("docker", "memory", "4G",
			"MEMORY", "Memory to allocate to each container (0 disables allocation)"),
		backendStringFlag("docker", "cpus", "2",
			"CPUS", "CPU count to allocate to each container (0 disables allocation)"),
		&cli.BoolFlag{
			Name:   "privileged",
			Usage:  "Run containers in privileged mode",
			EnvVar: beEnv("docker", "PRIVILEGED"),
		},
	}
)

func init() {
	Register("docker", "Docker", dockerFlags, newDockerProvider)
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
	readyAt   time.Time

	imageName string
}

func newDockerProvider(c ConfigGetter) (Provider, error) {
	client, err := buildDockerClient(c.String("endpoint"), c.String("cert-path"))
	if err != nil {
		return nil, err
	}

	cpuSetSize := runtime.NumCPU()
	if cpuSetSize < 2 {
		cpuSetSize = 2
	}

	memory := uint64(1024 * 1024 * 1024 * 4)
	if parsedMemory, err := humanize.ParseBytes(c.String("memory")); err == nil {
		memory = parsedMemory
	}

	cpus := uint64(2)
	if parsedCPUs, err := strconv.ParseUint(c.String("cpus"), 10, 64); err == nil {
		cpus = parsedCPUs
	}

	return &dockerProvider{
		client: client,

		runPrivileged: c.Bool("privileged"),
		runCmd:        strings.Split(c.String("cmd"), " "),
		runMemory:     memory,
		runCPUs:       int(cpus),

		cpuSets: make([]bool, cpuSetSize),
	}, nil
}

func buildDockerClient(endpoint, certPath string) (*docker.Client, error) {
	if endpoint == "" {
		return nil, ErrMissingEndpointConfig
	}

	if certPath != "" {
		ca := fmt.Sprintf("%s/ca.pem", certPath)
		cert := fmt.Sprintf("%s/cert.pem", certPath)
		key := fmt.Sprintf("%s/key.pem", certPath)
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

func (p *dockerProvider) Setup() error { return nil }

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

func (i *dockerInstance) StartupDuration() time.Duration {
	if i.container == nil {
		return zeroDuration
	}
	return i.container.Created.Sub(i.readyAt)
}
