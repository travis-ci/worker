package goblueboxapi

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TemplatesService exposes the API endpoints to interact with templates.
type TemplatesService struct {
	client *Client
}

// A Teamplate is a bootable OS image.
type Template struct {
	ID          string
	Description string
	Public      bool
	Created     time.Time
}

// A rich description of the output when creating a new Template.
type TemplateCreationStatus struct {
	Status string
	Text   string
	Error  int
}

// List returns the currently known templates, or an error if there
// was a problem fetching the information from the API.
func (s *TemplatesService) List() ([]Template, error) {
	req, err := s.client.newRequest("GET", "/block_templates", nil)
	if err != nil {
		return nil, err
	}

	var templates []Template
	err = s.client.do(req, &templates)

	return templates, err
}

// Get returns a template given its Id, or an error if there
// was a problem fetching the information from the API.
func (s *TemplatesService) Get(uuid string) (*Template, error) {
	req, err := s.client.newRequest("GET", fmt.Sprintf("/block_templates/%s", uuid), nil)
	if err != nil {
		return nil, err
	}

	template := new(Template)
	err = s.client.do(req, template)

	return template, err
}

// Create queues a new template for creation from a block uuid.
func (s *TemplatesService) Create(uuid string, description string) (*TemplateCreationStatus, error) {
	v := url.Values{}
	v.Set("id", uuid)
	if description != "" {
		v.Set("description", description)
	}

	req, err := s.client.newRequest("POST", "/block_templates", strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}

	status := new(TemplateCreationStatus)
	err = s.client.do(req, status)

	return status, err
}

// Destroy destroys the template/image.
func (s *TemplatesService) Destroy(uuid string) error {
	req, err := s.client.newRequest("DELETE", fmt.Sprintf("/block_templates/%s", uuid), nil)
	if err != nil {
		return err
	}

	return s.client.do(req, nil)
}
