package goblueboxapi

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// BlocksService exposes the API endpoints to interact with blocks.
type BlocksService struct {
	client *Client
}

// A Block is an on demand virtual computing resource.
type Block struct {
	ID       string
	Hostname string
	IPs      []BlockIP
	Status   string
}

// A BlockIP holds an IPv4 or IPv6 address for a block.
type BlockIP struct {
	Address string
}

// BlockParams holds the information needed to boot a block. Product and
// Template as well as either SshPublicKey or Password (but not both) must be
// specified.
type BlockParams struct {
	Product      string
	Template     string
	Password     string
	SshPublicKey string
	Hostname     string
	Username     string
	Location     string
	IPv6Only     bool
}

func (p BlockParams) validates() error {
	if p.Product == "" {
		return errors.New(`must specify "Product"`)
	}
	if p.Template == "" {
		return errors.New(`must specify "Template"`)
	}
	if p.Password != "" && p.SshPublicKey != "" {
		return errors.New(`only one of "Password" and "SshPublicKey" may be specified`)
	}
	if p.Password == "" && p.SshPublicKey == "" {
		return errors.New(`one of "Password" and "SshPublicKey" must be specified`)
	}

	return nil
}

func (p BlockParams) toValues() url.Values {
	v := url.Values{}
	if p.Product != "" {
		v.Set("product", p.Product)
	}
	if p.Template != "" {
		v.Set("template", p.Template)
	}
	if p.Password != "" {
		v.Set("password", p.Password)
	}
	if p.SshPublicKey != "" {
		v.Set("ssh_public_key", p.SshPublicKey)
	}
	if p.Hostname != "" {
		v.Set("hostname", p.Hostname)
	}
	if p.Username != "" {
		v.Set("username", p.Username)
	}
	if p.Location != "" {
		v.Set("location", p.Location)
	}
	if p.IPv6Only {
		v.Set("ipv6_only", "true")
	}

	return v
}

// List returns the currently running blocks, or an error if there was a problem
// fetching the information from the API.
func (s *BlocksService) List() ([]Block, error) {
	req, err := s.client.newRequest("GET", "/blocks", nil)
	if err != nil {
		return nil, err
	}

	var blocks []Block
	err = s.client.do(req, &blocks)

	return blocks, err
}

// Get returns a block given its Id, or an error if there was a problem fetching
// the information from the API.
func (s *BlocksService) Get(uuid string) (*Block, error) {
	req, err := s.client.newRequest("GET", fmt.Sprintf("/blocks/%s", uuid), nil)
	if err != nil {
		return nil, err
	}

	block := new(Block)
	err = s.client.do(req, block)

	return block, err
}

// Create boots a new block with the given parameters, and returns the block.
func (s *BlocksService) Create(p BlockParams) (*Block, error) {
	if err := p.validates(); err != nil {
		return nil, err
	}

	req, err := s.client.newRequest("POST", "/blocks", strings.NewReader(p.toValues().Encode()))
	if err != nil {
		return nil, err
	}

	block := new(Block)
	err = s.client.do(req, block)

	return block, err
}

// Destroy shuts down a block.
func (s *BlocksService) Destroy(uuid string) error {
	req, err := s.client.newRequest("DELETE", fmt.Sprintf("/blocks/%s", uuid), nil)
	if err != nil {
		return err
	}

	return s.client.do(req, nil)
}
