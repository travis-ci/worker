package backend

import (
	"fmt"
	"io"

	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

const (
	gceHelp = `
              CLIENT_ID - [REQUIRED] client id for OAuth2
          CLIENT_SECRET - [REQUIRED] client secret for OAuth2
                  TOKEN - [REQUIRED] token for OAuth2

`
)

func init() {
	config.SetProviderHelp("GCE", gceHelp)
}

type GCEProvider struct {
	client *compute.Service
}

type GCEInstance struct {
	client   *compute.Service
	provider *GCEProvider
	instance *compute.Instance

	imageName string
}

func NewGCEProvider(cfg *config.ProviderConfig) (*GCEProvider, error) {
	client, err := buildGoogleComputeService(cfg)
	if err != nil {
		return nil, err
	}

	return &GCEProvider{
		client: client,
	}, nil
}

func buildGoogleComputeService(cfg *config.ProviderConfig) (*compute.Service, error) {
	if !cfg.IsSet("CLIENT_ID") {
		return nil, fmt.Errorf("missing CLIENT_ID")
	}

	if !cfg.IsSet("CLIENT_SECRET") {
		return nil, fmt.Errorf("missing CLIENT_SECRET")
	}

	config := &oauth2.Config{
		ClientID:     cfg.Get("CLIENT_ID"),
		ClientSecret: cfg.Get("CLIENT_SECRET"),
		Endpoint:     google.Endpoint,
		Scopes: []string{
			compute.DevstorageFullControlScope,
			compute.ComputeScope,
		},
	}
	return compute.New(config.Client(gocontext.Background(), &oauth2.Token{
		AccessToken: cfg.Get("TOKEN"),
	}))
}

func (p *GCEProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	/*
		logger := context.LoggerFromContext(ctx)
		imageName, err := p.imageForLanguage(startAttributes.Language)
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
			return &GCEInstance{
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

	*/
	return nil, nil
}

func (p *GCEProvider) imageForLanguage(language string) (string, error) {
	return "", nil
	/*
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
	*/
}

func (i *GCEInstance) sshClient() (*ssh.Client, error) {
	/*
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
	*/
	return nil, nil
}

func (i *GCEInstance) UploadScript(ctx gocontext.Context, script []byte) error {
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

func (i *GCEInstance) RunScript(ctx gocontext.Context, output io.WriteCloser) (*RunResult, error) {
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

func (i *GCEInstance) Stop(ctx gocontext.Context) error {
	/*
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
	*/
	return nil
}

func (i *GCEInstance) ID() string {
	/*
		if i.container == nil {
			return "{unidentified}"
		}

		return fmt.Sprintf("%s:%s", i.container.ID[0:7], i.imageName)
	*/
	return ""
}
