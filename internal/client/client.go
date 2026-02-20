package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is an HTTP client for the InvenTree REST API.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// New creates a new InvenTree API client.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Do executes an HTTP request against the InvenTree API with authentication.
func (c *Client) Do(method, path string, body io.Reader) (*http.Response, error) {
	u, err := url.JoinPath(c.BaseURL, path)
	if err != nil {
		return nil, fmt.Errorf("building URL: %w", err)
	}

	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	return c.HTTPClient.Do(req)
}

// Get performs a GET request and decodes the JSON response into dest.
func (c *Client) Get(path string, dest any) error {
	resp, err := c.Do(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(b))
	}

	return json.NewDecoder(resp.Body).Decode(dest)
}
