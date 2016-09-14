package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cenk/backoff"
	"github.com/pkg/errors"
)

type cbInstanceData struct {
	ID          string `json:"id"`
	Image       string `json:"image"`
	IPAddress   string `json:"ip_address"`
	Provider    string `json:"provider"`
	State       string `json:"state"`
	UpstreamID  string `json:"upstream_id"`
	ErrorReason string `json:"error_reason"`
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

	instance, err := c.httpRequest(req, 200)
	if err != nil {
		return nil, errors.Wrap(err, "error creating instance")
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

	instance, err := c.httpRequest(req, 200)
	if err != nil {
		return nil, errors.Wrap(err, "error getting instance")
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

	instance, err := c.httpRequest(req, 200)
	if err != nil {
		return nil, errors.Wrap(err, "error deleting instance")
	}

	return instance, nil
}

func (c *cbClient) httpRequest(req *http.Request, expectedStatus int) (*cbInstanceData, error) {
	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	var resp *http.Response
	err := backoff.Retry(func() (err error) {
		resp, err = c.httpClient.Do(req)
		return
	}, b)

	if err != nil {
		return nil, errors.Wrap(err, "error sending request")
	}

	defer resp.Body.Close()

	if resp.StatusCode != expectedStatus {
		body := new(bytes.Buffer)
		body.ReadFrom(resp.Body)
		return nil, errors.Errorf("expected %v response code, got status=%v body=%v", expectedStatus, resp.StatusCode, body.String())
	}

	instance := &cbInstanceData{}
	err = json.NewDecoder(resp.Body).Decode(instance)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't decode json response")
	}

	return instance, nil
}
