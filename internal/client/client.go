package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is an HTTP client for the InvenTree REST API.
// Fields are unexported to prevent accidental exposure of credentials
// in logs, fmt output, or JSON serialization.
type Client struct {
	baseURL string
	token   string
	// scheme is the Authorization scheme paired with token: "Token" for an
	// InvenTree API token, "Bearer" for a forwarded OAuth access token.
	scheme     string
	httpClient *http.Client
	// requiresCaller marks a client that holds no usable credential of its own.
	// It refuses to issue requests, so a tool that forgets to derive a
	// per-caller client fails loudly instead of quietly acting as a shared
	// service account with wider permissions than the user has.
	requiresCaller bool
}

// New creates a client that authenticates with a fixed InvenTree API token.
// Every request it makes is attributed to that token's user.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		scheme:  "Token",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewForwarding creates a client that carries no credential. Callers must
// derive a usable client per request with [Client.WithCallerToken], so each
// InvenTree call is attributed to the end user who triggered it and InvenTree's
// own role permissions apply to them rather than to a shared account.
func NewForwarding(baseURL string) *Client {
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		requiresCaller: true,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// RequiresCaller reports whether this client needs a per-request credential.
func (c *Client) RequiresCaller() bool { return c.requiresCaller }

// WithCallerToken returns a copy of c that authenticates as the end user with
// the given OAuth access token. The underlying HTTP client is shared, so
// deriving a client per request is cheap.
func (c *Client) WithCallerToken(token string) *Client {
	derived := *c
	derived.token = token
	derived.scheme = "Bearer"
	derived.requiresCaller = false
	return &derived
}

// Do executes an HTTP request against the InvenTree API with authentication.
// The path may include query parameters (e.g., "/api/part/?search=foo").
func (c *Client) Do(method, path string, body io.Reader) (*http.Response, error) {
	if c.requiresCaller {
		// Reaching here means a tool handler skipped deriving a per-caller
		// client. Refuse rather than fall back to any wider credential.
		return nil, fmt.Errorf("no caller credential for this request: " +
			"the server is configured to act as the requesting user, but this " +
			"tool did not supply their token")
	}

	// Split path from query string to avoid url.JoinPath encoding the '?'.
	pathPart, query, _ := strings.Cut(path, "?")

	u, err := url.JoinPath(c.baseURL, pathPart)
	if err != nil {
		return nil, fmt.Errorf("building URL for %s: %w", pathPart, err)
	}
	if query != "" {
		u += "?" + query
	}

	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, fmt.Errorf("creating %s request for %s: %w", method, pathPart, err)
	}

	req.Header.Set("Authorization", c.scheme+" "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %s", method, pathPart, sanitizeError(err))
	}
	return resp, nil
}

// Get performs a GET request and decodes the JSON response into dest.
func (c *Client) Get(path string, dest any) error {
	resp, err := c.Do(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, dest)
}

// Post performs a POST request with a JSON body and decodes the response.
func (c *Client) Post(path string, payload any, dest any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.Do(http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, dest)
}

// Patch performs a PATCH request with a JSON body and decodes the response.
func (c *Client) Patch(path string, payload any, dest any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.Do(http.MethodPatch, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, dest)
}

// Delete performs a DELETE request.
func (c *Client) Delete(path string) error {
	resp, err := c.Do(http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, nil)
}

// decodeResponse reads the full response body and handles errors uniformly.
// If dest is nil the body is consumed but not decoded.
func decodeResponse(resp *http.Response, dest any) error {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, data)
	}

	if dest == nil {
		return nil
	}

	if len(data) == 0 {
		return fmt.Errorf("empty response body (HTTP %d)", resp.StatusCode)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("decoding response: %w (body: %.200s)", err, string(data))
	}
	return nil
}

// apiError returns a descriptive error for non-2xx responses.
func apiError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))

	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("authentication failed (401): check INVENTREE_TOKEN")
	case http.StatusForbidden:
		return fmt.Errorf("permission denied (403): token lacks access to this resource")
	case http.StatusNotFound:
		return fmt.Errorf("not found (404): resource does not exist")
	default:
		if msg == "" {
			return fmt.Errorf("API error %d (empty response)", status)
		}
		// Truncate long error bodies to keep messages readable.
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return fmt.Errorf("API error %d: %s", status, msg)
	}
}

// sanitizeError strips potential credential info from network errors.
func sanitizeError(err error) string {
	s := err.Error()
	// Remove any token values that might appear in URL-related errors.
	if i := strings.Index(s, "Token "); i != -1 {
		end := strings.IndexAny(s[i+6:], " \"')")
		if end == -1 {
			s = s[:i] + "Token [REDACTED]"
		} else {
			s = s[:i] + "Token [REDACTED]" + s[i+6+end:]
		}
	}
	return s
}

// PaginatedResponse represents a Django REST Framework paginated response.
type PaginatedResponse[T any] struct {
	Count    int    `json:"count"`
	Next     string `json:"next"`
	Previous string `json:"previous"`
	Results  []T    `json:"results"`
}
