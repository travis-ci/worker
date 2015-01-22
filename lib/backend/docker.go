package backend

import (
	"fmt"
	"io"
	"runtime"
	"sync"

	"github.com/fsouza/go-dockerclient"
	"golang.org/x/net/context"
)

type DockerProvider struct {
	client *docker.Client

	cpuSetsMutex sync.Mutex
	cpuSets      []bool
}

type DockerInstance struct {
	client    *docker.Client
	container *docker.Container
}

func NewDockerProvider(endpoint string) (*DockerProvider, error) {
	client, err := docker.NewClient(endpoint)
	if err != nil {
		return nil, err
	}

	return &DockerProvider{
		client:  client,
		cpuSets: make([]bool, runtime.NumCPU()),
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
			Hostname: "",
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

	err = p.client.StartContainer(container.ID, &docker.HostConfig{})
	if err != nil {
		return nil, err
	}

	return &DockerInstance{
		client:    p.client,
		container: container,
	}, nil
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
		if len(cpuSets) > 2 {
			break
		}
		if !checkedOut {
			cpuSets = append(cpuSets, i)
		}
	}

	if len(cpuSets) != 2 {
		return "", fmt.Errorf("not enough free CPUsets")
	}

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

func (i *DockerInstance) UploadScript(ctx context.Context, script []byte) error {
	return nil
}

func (i *DockerInstance) RunScript(ctx context.Context, output io.WriteCloser) (RunResult, error) {
	return RunResult{}, nil
}

func (i *DockerInstance) Stop(ctx context.Context) error {
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
