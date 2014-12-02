package lib

import (
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/henrikhodne/gotravismacapi"
)

type saucelabsAPI struct {
	client *gotravismacapi.Client
	config SauceLabsConfig
}

// NewSauceLabs creates a VMProvider that talks to the Sauce Labs Mac VM API.
func NewSauceLabs(config SauceLabsConfig) VMProvider {
	u, err := url.Parse(config.Endpoint)
	if err != nil {
		return nil
	}

	return &saucelabsAPI{
		client: gotravismacapi.NewClient(u),
		config: config,
	}
}

func (a *saucelabsAPI) Start(hostname, language string, bootTimeout time.Duration) (VM, error) {
	instance, err := a.client.StartInstance(a.config.ImageName, hostname)
	if err != nil {
		return nil, err
	}

	doneChan, cancelChan := waitFor(func() bool {
		conn, err := net.Dial("tcp", fmt.Sprintf("%s:3422", instance.PrivateIP))
		if conn != nil {
			conn.Close()
		}
		return err == nil
	}, 3*time.Second)

	select {
	case <-doneChan:
		return &saucelabsServer{a, instance}, nil
	case <-time.After(bootTimeout):
		cancelChan <- true
		return nil, BootTimeoutError(bootTimeout)
	}
}

type saucelabsServer struct {
	client   *saucelabsAPI
	instance *gotravismacapi.Instance
}

func (s *saucelabsServer) SSHInfo() VMSSHInfo {
	return VMSSHInfo{
		Addr:             fmt.Sprintf("%s:3422", s.instance.PrivateIP),
		Username:         "travis",
		SSHKeyPath:       s.client.config.SSHKeyPath,
		SSHKeyPassphrase: s.client.config.SSHKeyPassphrase,
	}
}

func (s *saucelabsServer) Destroy() error {
	return s.client.client.DestroyInstance(s.instance.ID)
}
