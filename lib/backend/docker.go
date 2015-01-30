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
	"github.com/rcrowley/go-metrics"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/context"
)

type DockerProvider struct {
	client *docker.Client

	cpuSetsMutex sync.Mutex
	cpuSets      []bool

	bootTimer    metrics.Timer
	timeoutMeter metrics.Meter
}

type DockerInstance struct {
	client    *docker.Client
	provider  *DockerProvider
	container *docker.Container
}

func NewDockerProvider(endpoint string) (*DockerProvider, error) {
	client, err := docker.NewClient(endpoint)
	if err != nil {
		return nil, err
	}

	bootTimer := metrics.NewTimer()
	metrics.Register("worker.vm.provider.docker.boot", bootTimer)

	timeoutMeter := metrics.NewMeter()
	metrics.Register("worker.vm.provider.docker.boot.timeout", timeoutMeter)

	return &DockerProvider{
		client:       client,
		cpuSets:      make([]bool, runtime.NumCPU()),
		bootTimer:    bootTimer,
		timeoutMeter: timeoutMeter,
	}, nil
}

func (p *DockerProvider) Start(ctx context.Context) (Instance, error) {
	cpuSets, err := p.checkoutCPUSets()
	if err != nil {
		return nil, err
	}

	imageID, err := p.imageForLanguage("ruby")
	if err != nil {
		return nil, err
	}

	createOptions := docker.CreateContainerOptions{
		Config: &docker.Config{
			Cmd:      []string{"/sbin/init"},
			Image:    imageID,
			Memory:   1024 * 1024 * 1024 * 4,
			CPUSet:   cpuSets,
			Hostname: fmt.Sprintf("testing-go-%s", uuid.NewUUID()),
		},
	}

	container, err := p.client.CreateContainer(createOptions)
	if err != nil {
		if container != nil {
			err := p.client.RemoveContainer(docker.RemoveContainerOptions{
				ID:            container.ID,
				RemoveVolumes: true,
				Force:         true,
			})
			if err != nil {
				// TODO: logrusify
				fmt.Printf("error: couldn't remove container after create failure: %v\n", err)
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
		p.bootTimer.UpdateSince(startBooting)
		return &DockerInstance{
			client:    p.client,
			provider:  p,
			container: container,
		}, nil
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			p.timeoutMeter.Mark(1)
		}
		return nil, ctx.Err()
	}

}

func (p *DockerProvider) imageForLanguage(language string) (string, error) {
	searchTag := "travis:" + language

	images, err := p.client.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return "", err
	}

	for _, image := range images {
		for _, tag := range image.RepoTags {
			if tag == searchTag {
				return image.ID, nil
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

func (i *DockerInstance) UploadScript(ctx context.Context, script []byte) error {
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

	f, err := sftp.Create("build.sh")
	if err != nil {
		return err
	}

	if _, err := f.Write(script); err != nil {
		return err
	}

	return nil
}

func (i *DockerInstance) RunScript(ctx context.Context, output io.WriteCloser) (RunResult, error) {
	client, err := i.sshClient()
	if err != nil {
		return RunResult{Completed: false}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return RunResult{Completed: false}, err
	}
	defer session.Close()

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
	if err != nil {
		return RunResult{Completed: false}, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Run("bash ~/build.sh")
	if err == nil {
		return RunResult{Completed: true, ExitCode: 0}, nil
	}

	switch err := err.(type) {
	case *ssh.ExitError:
		return RunResult{Completed: true, ExitCode: uint8(err.ExitStatus())}, nil
	default:
		return RunResult{Completed: false}, err
	}
}

func (i *DockerInstance) Stop(ctx context.Context) error {
	defer i.provider.checkinCPUSets(i.container.Config.CPUSet)

	err := i.client.StopContainer(i.container.ID, 30)
	if err != nil {
		err2 := i.client.RemoveContainer(docker.RemoveContainerOptions{
			ID:            i.container.ID,
			RemoveVolumes: true,
			Force:         true,
		})

		if err2 != nil {
			// TODO: logrusify
			fmt.Printf("error: couldn't remove container after stop failure: %v\n", err2)
		}

		return err
	}

	return nil
}
