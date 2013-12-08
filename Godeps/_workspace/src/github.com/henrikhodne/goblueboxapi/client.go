package goblueboxapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// A Client manages communication with the Blue Box API
type Client struct {
	client *http.Client

	BaseURL *url.URL

	// Credentials which is used for authentication during API requests
	CustomerID string
	APIKey     string

	Blocks *BlocksService
}

// NewClient creates a Client with the given customer ID and API key and the
// default API endpoint.
func NewClient(customerID, apiKey string) *Client {
	c := &Client{
		client: http.DefaultClient,
		BaseURL: &url.URL{
			Scheme: "https",
			Host:   "boxpanel.bluebox.net",
		},
		CustomerID: customerID,
		APIKey:     apiKey,
	}

	c.Blocks = &BlocksService{client: c}

	return c
}

func (c *Client) urlForPath(path string) *url.URL {
	u, _ := url.Parse(fmt.Sprintf("api%s.json", path))
	return c.BaseURL.ResolveReference(u)
}

func (c *Client) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	url := c.urlForPath(path)

	req, err := http.NewRequest(method, url.String(), body)

	if method == "POST" || method == "PUT" {
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	}

	req.SetBasicAuth(c.CustomerID, c.APIKey)
	req.Header.Add("Accept", "application/json; charset=utf-8")

	return req, err
}

func (c *Client) do(req *http.Request, v interface{}) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if c := resp.StatusCode; c < 200 || c >= 300 {
		return fmt.Errorf("expected status 2xx, got %d", c)
	}

	if v != nil {
		err = json.NewDecoder(resp.Body).Decode(v)
	}

	return err
}
