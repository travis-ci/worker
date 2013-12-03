package main

import (
	"fmt"
	"github.com/henrikhodne/goblueboxapi"
	"net"
)

type blueboxApi struct {
	client *goblueboxapi.Client
	config BlueBoxConfig
}

type blueboxServer struct {
	client   *goblueboxapi.Client
	block    *goblueboxapi.Block
	password string
}

func (a *blueboxApi) Start(hostname string) (VMCloudServer, error) {
	params := goblueboxapi.BlockParams{
		Product:  a.config.ProductId,
		Template: a.config.TemplateId,
		Location: a.config.LocationId,
		Hostname: hostname,
		Username: "travis",
		Password: generatePassword(),
	}
	block, err := a.client.Blocks.Create(params)

	return &blueboxServer{a.client, block, params.Password}, err
}

func (s *blueboxServer) SSHInfo() VMCloudSSHInfo {
	ipString := ""
	for _, address := range s.block.Ips {
		ip := net.ParseIP(address.Address)
		if ip.To4() != nil {
			ipString = ip.String()
			break
		}
	}

	return VMCloudSSHInfo{
		Addr:     fmt.Sprintf("%s:22", ipString),
		Username: "travis",
		Password: s.password,
	}
}

func (s *blueboxServer) Destroy() error {
	return s.client.Blocks.Destroy(s.block.Id)
}

func (s *blueboxServer) Refresh() (err error) {
	s.block, err = s.client.Blocks.Get(s.block.Id)
	return
}

func (s *blueboxServer) Ready() bool {
	return s.block.Status == "running"
}

func NewBlueBox(config BlueBoxConfig) VMCloudAPI {
	return &blueboxApi{
		client: goblueboxapi.NewClient(config.CustomerId, config.ApiKey),
		config: config,
	}
}
