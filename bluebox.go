package main

import (
	"fmt"
	"github.com/henrikhodne/goblueboxapi"
	"net"
	"regexp"
	"strings"
	"time"
)

var (
	templateRegexp *regexp.Regexp
)

func init() {
	templateRegexp = regexp.MustCompile(`travis-([\w-]+)-\d{4}-\d{2}-\d{2}-\d{2}-\d{2}`)
}

type blueboxAPI struct {
	client *goblueboxapi.Client
	config BlueBoxConfig
}

// NewBlueBox creates a VMProvider that talks to the Blue Box blocks API.
func NewBlueBox(config BlueBoxConfig) VMProvider {
	return &blueboxAPI{
		client: goblueboxapi.NewClient(config.CustomerID, config.APIKey),
		config: config,
	}
}

func (a *blueboxAPI) Start(hostname, language string, bootTimeout time.Duration) (VM, error) {
	params := goblueboxapi.BlockParams{
		Product:  a.config.ProductID,
		Template: a.templateIDForLanguage(language),
		Location: a.config.LocationID,
		Hostname: hostname,
		Username: "travis",
		Password: generatePassword(),
		IPv6Only: a.config.IPv6Only,
	}
	block, err := a.client.Blocks.Create(params)
	if err != nil {
		return nil, err
	}

	doneChan, cancelChan := waitFor(func() bool {
		block, err = a.client.Blocks.Get(block.ID)
		return block.Status == "running"
	}, 3*time.Second)

	select {
	case <-doneChan:
		return &blueboxServer{a.client, block, params.Password}, nil
	case <-time.After(bootTimeout):
		cancelChan <- true
		return nil, BootTimeoutError(bootTimeout)
	}
}

func (a *blueboxAPI) templateIDForLanguage(language string) string {
	latestTemplates := a.latestTemplates()
	if templateID, ok := latestTemplates[language]; ok {
		return templateID
	}

	return latestTemplates["ruby"]
}

func (a *blueboxAPI) latestTemplates() map[string]string {
	latestTemplates := make(map[string]goblueboxapi.Template)
	latestTemplateIDs := make(map[string]string)

	templates, err := a.client.Templates.List()
	if err != nil {
		fmt.Printf("error trying to get templates: %s\n", err)
		return nil
	}

	for _, t := range templates {
		if t.Public || !strings.HasPrefix(t.Description, "travis-") {
			continue
		}

		language := templateRegexp.FindStringSubmatch(t.Description)[1]
		if _, ok := latestTemplates[language]; !ok || t.Created.After(latestTemplates[language].Created) {
			latestTemplates[language] = t
			latestTemplateIDs[language] = t.ID
		}
	}

	return latestTemplateIDs
}

type blueboxServer struct {
	client   *goblueboxapi.Client
	block    *goblueboxapi.Block
	password string
}

func (s *blueboxServer) SSHInfo() VMSSHInfo {
	ipString := s.block.IPs[0].Address
	for _, address := range s.block.IPs {
		ip := net.ParseIP(address.Address)
		if ip.To4() != nil {
			ipString = ip.String()
			break
		}
	}

	return VMSSHInfo{
		Addr:     fmt.Sprintf("%s:22", ipString),
		Username: "travis",
		Password: s.password,
	}
}

func (s *blueboxServer) Destroy() error {
	return s.client.Blocks.Destroy(s.block.ID)
}

func (s *blueboxServer) Refresh() (err error) {
	s.block, err = s.client.Blocks.Get(s.block.ID)
	return
}

func (s *blueboxServer) Ready() bool {
	return s.block.Status == "running"
}
