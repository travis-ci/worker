// Package gotravismacapi is a client interface to the Travis CI/Sauce Labs API
// for spinning up Mac VMs.
package gotravismacapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// UnexpectedStatusCodeError is returned as an error if a respond did not have
// a 2xx (success) status code. The integer value is the actual status code
// returned.
type UnexpectedStatusCodeError int

func (c UnexpectedStatusCodeError) Error() string {
	return fmt.Sprintf("expected status 2xx, got %d", c)
}

// A Client is an API client.
type Client struct {
	client *http.Client

	BaseURL *url.URL
}

type Instance struct {
	ID          string            `json:"instance_id"`
	ImageID     string            `json:"image_id"`
	RealImageID string            `json:"real_image_id"`
	State       string            `json:"state"`
	PrivateIP   string            `json:"private_ip"`
	ExtraInfo   map[string]string `json:"extra_info"`
}

func NewClient(baseURL *url.URL) *Client {
	return &Client{
		client:  http.DefaultClient,
		BaseURL: baseURL,
	}
}

func (c *Client) StartInstance(imageName, hostname string) (*Instance, error) {
	startupInfo, err := json.Marshal(map[string]string{"hostname": hostname})
	if err != nil {
		return nil, err
	}

	req, err := c.newRequest("POST", fmt.Sprintf("instances?image=%s", url.QueryEscape(imageName)), bytes.NewReader(startupInfo))
	if err != nil {
		return nil, err
	}

	instance := new(Instance)
	err = c.do(req, instance)
	if err != nil {
		return nil, err
	}

	return c.InstanceInfo(instance.ID)
}

// ListInstances returns a list of instance IDs for currently running instances
func (c *Client) ListInstances() ([]string, error) {
	req, err := c.newRequest("GET", "instances", nil)
	if err != nil {
		return nil, err
	}

	var response map[string][]string
	err = c.do(req, &response)
	if err != nil {
		return nil, err
	}

	return response["instances"], nil
}

func (c *Client) InstanceInfo(instanceID string) (*Instance, error) {
	req, err := c.newRequest("GET", fmt.Sprintf("instances/%s", url.QueryEscape(instanceID)), nil)
	if err != nil {
		return nil, err
	}

	instance := new(Instance)
	err = c.do(req, instance)

	return instance, err
}

func (c *Client) DestroyInstance(instanceID string) error {
	req, err := c.newRequest("DELETE", fmt.Sprintf("instances/%s", url.QueryEscape(instanceID)), nil)
	if err != nil {
		return err
	}

	return c.do(req, nil)
}

func (c *Client) urlForPath(path string) *url.URL {
	u, _ := url.Parse(path)
	return c.BaseURL.ResolveReference(u)
}

func (c *Client) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	return http.NewRequest(method, c.urlForPath(path).String(), body)
}

func (c *Client) do(req *http.Request, v interface{}) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if c := resp.StatusCode; c < 200 || c >= 300 {
		return UnexpectedStatusCodeError(c)
	}

	if v != nil {
		err = json.NewDecoder(resp.Body).Decode(v)
	}

	return err
}
