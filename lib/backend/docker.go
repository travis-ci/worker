package backend

import (
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"code.google.com/p/go-uuid/uuid"

	"github.com/fsouza/go-dockerclient"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/lib/context"
	"github.com/travis-ci/worker/lib/metrics"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
)

var (
	errMissingEndpointConfig = fmt.Errorf("expected config key endpoint")
)

type DockerProvider struct {
	client        *docker.Client
	runPrivileged bool

	cpuSetsMutex sync.Mutex
	cpuSets      []bool
}

type DockerInstance struct {
	client    *docker.Client
	provider  *DockerProvider
	container *docker.Container
}

func NewDockerProvider(config map[string]string) (*DockerProvider, error) {
	client, err := buildClient(config)
	if err != nil {
		return nil, err
	}

	cpuSetSize := runtime.NumCPU()
	if cpuSetSize < 2 {
		cpuSetSize = 2
	}

	privileged := false
	if v, ok := config["privileged"]; ok {
		privileged = v
	}

	return &DockerProvider{
		client:        client,
		runPrivileged: privileged,
		cpuSets:       make([]bool, cpuSetSize),
	}, nil
}

func buildClient(config map[string]string) (*docker.Client, error) {
	endpoint, ok := config["endpoint"]
	if !ok {
		return nil, errMissingEndpointConfig
	}

	if path, ok := config["cert_path"]; ok {
		ca := fmt.Sprintf("%s/ca.pem", path)
		cert := fmt.Sprintf("%s/cert.pem", path)
		key := fmt.Sprintf("%s/key.pem", path)
		return docker.NewTLSClient(endpoint, cert, key, ca)
	}

	return docker.NewClient(endpoint)
}

func (p *DockerProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	cpuSets, err := p.checkoutCPUSets()
	if err != nil && cpuSets != "" {
		return nil, err
	}

	imageID, err := p.imageForLanguage(startAttributes.Language)
	if err != nil {
		return nil, err
	}

	dockerConfig := &docker.Config{
		Cmd:        []string{"/sbin/init"},
		Image:      imageID,
		Memory:     1024 * 1024 * 1024 * 4,
		Privileged: p.runPrivileged,
		Hostname:   fmt.Sprintf("testing-go-%s", uuid.NewUUID()),
	}

	if cpuSets != "" {
		dockerConfig.CPUSet = cpuSets
	}

	container, err := p.client.CreateContainer(docker.CreateContainerOptions{Config: dockerConfig})
	if err != nil {
		if container != nil {
			err := p.client.RemoveContainer(docker.RemoveContainerOptions{
				ID:            container.ID,
				RemoveVolumes: true,
				Force:         true,
			})
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't remove container after create failure")
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
		for true {
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
		return &DockerInstance{
			client:    p.client,
			provider:  p,
			container: container,
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

func (p *DockerProvider) imageForLanguage(language string) (string, error) {
	images, err := p.client.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return "", err
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
					return image.ID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no image found with language %s", language)
}

func (p *DockerProvider) checkoutCPUSets() (string, error) {
	p.cpuSetsMutex.Lock()
	defer p.cpuSetsMutex.Unlock()

	cpuSets := []int{}

	for i, checkedOut := range p.cpuSets {
		if !checkedOut {
			cpuSets = append(cpuSets, i)
		}
		if len(cpuSets) == 2 {
			break
		}
	}

	if len(cpuSets) != 2 {
		return "", fmt.Errorf("not enough free CPUsets")
	}

	p.cpuSets[cpuSets[0]] = true
	p.cpuSets[cpuSets[1]] = true

	return fmt.Sprintf("%d,%d", cpuSets[0], cpuSets[1]), nil
}

func (p *DockerProvider) checkinCPUSets(sets string) {
	p.cpuSetsMutex.Lock()
	defer p.cpuSetsMutex.Unlock()

	var cpu1, cpu2 int
	fmt.Sscanf(sets, "%d,%d", &cpu1, &cpu2)

	p.cpuSets[cpu1] = false
	p.cpuSets[cpu2] = false
}

func (i *DockerInstance) sshClient() (*ssh.Client, error) {
	var err error
	i.container, err = i.client.InspectContainer(i.container.ID)
	if err != nil {
		return nil, err
	}

	fmt.Printf("networksettings: %+v\n", i.container.NetworkSettings)

	time.Sleep(2 * time.Second)

	return ssh.Dial("tcp", fmt.Sprintf("%s:22", i.container.NetworkSettings.IPAddress), &ssh.ClientConfig{
		User: "travis",
		Auth: []ssh.AuthMethod{
			ssh.Password("travis"),
		},
	})
}

func (i *DockerInstance) UploadScript(ctx gocontext.Context, script []byte) error {
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

func (i *DockerInstance) RunScript(ctx gocontext.Context, output io.WriteCloser) (*RunResult, error) {
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

func (i *DockerInstance) Stop(ctx gocontext.Context) error {
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

func (i *DockerInstance) ID() string {
	if i.container == nil {
		return "{unidentified}"
	}

	return fmt.Sprintf("%s:%s", i.container.ID, i.container.Image)
}
