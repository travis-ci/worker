package main

import (
	"fmt"
	"github.com/henrikhodne/goblueboxapi"
	"net"
)

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

func (a *blueboxAPI) Start(hostname string) (VM, error) {
	params := goblueboxapi.BlockParams{
		Product:  a.config.ProductID,
		Template: a.config.TemplateID,
		Location: a.config.LocationID,
		Hostname: hostname,
		Username: "travis",
		Password: generatePassword(),
	}
	block, err := a.client.Blocks.Create(params)

	return &blueboxServer{a.client, block, params.Password}, err
}

type blueboxServer struct {
	client   *goblueboxapi.Client
	block    *goblueboxapi.Block
	password string
}

func (s *blueboxServer) SSHInfo() VMSSHInfo {
	ipString := ""
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
