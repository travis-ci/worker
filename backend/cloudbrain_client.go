package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/pkg/errors"
)

type cbInstanceData struct {
	ID         string `json:"id"`
	Image      string `json:"image"`
	IPAddress  string `json:"ip_address"`
	Provider   string `json:"provider"`
	State      string `json:"state"`
	UpstreamID string `json:"upstream_id"`
}

type cbInstanceRequest struct {
	Provider     string `json:"provider"`
	Image        string `json:"image"`
	InstanceType string `json:"instance_type"`
	PublicSSHKey string `json:"public_ssh_key"`
}

type cbClient struct {
	baseURL    *url.URL
	provider   string
	httpClient *http.Client
}

func (c *cbClient) Create(instRequest *cbInstanceRequest) (*cbInstanceData, error) {
	u, err := c.baseURL.Parse("instances")
	if err != nil {
		return nil, errors.Wrap(err, "error creating instance create URL")
	}

	jsonBody, err := json.Marshal(instRequest)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't marshal instance create request to JSON")
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "error creating instance create request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "error sending instance create request")
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		body := new(bytes.Buffer)
		body.ReadFrom(resp.Body)
		return nil, errors.Errorf("expected 200 or 201 response code for create, got status=%v body=%v", resp.StatusCode, body.String())
	}

	instance := &cbInstanceData{}
	err = json.NewDecoder(resp.Body).Decode(instance)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't decode create response")
	}

	return instance, nil
}

func (c *cbClient) Get(id string) (*cbInstanceData, error) {
	u, err := c.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(id)))
	if err != nil {
		return nil, errors.Wrap(err, "error creating instance get URL")
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "error creating instance get request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "error sending instance get request")
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body := new(bytes.Buffer)
		body.ReadFrom(resp.Body)
		return nil, errors.Errorf("expected 200 response code for get, got status=%v body=%v", resp.StatusCode, body.String())
	}

	instance := &cbInstanceData{}
	err = json.NewDecoder(resp.Body).Decode(instance)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't decode get response")
	}

	return instance, nil
}

func (c *cbClient) Delete(id string) (*cbInstanceData, error) {
	u, err := c.baseURL.Parse(fmt.Sprintf("instances/%s", url.QueryEscape(id)))
	if err != nil {
		return nil, errors.Wrap(err, "error creating instance delete URL")
	}

	req, err := http.NewRequest("DELETE", u.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "error creating instance delete request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "error sending instance delete request")
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body := new(bytes.Buffer)
		body.ReadFrom(resp.Body)
		return nil, errors.Errorf("expected 200 response code for delete, got status=%v body=%v", resp.StatusCode, body.String())
	}

	instance := &cbInstanceData{}
	err = json.NewDecoder(resp.Body).Decode(instance)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't decode delete response")
	}

	return instance, nil
}
