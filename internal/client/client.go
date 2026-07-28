package client

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Authorization schemes accepted by the InvenTree REST API. Which one is in use
// also decides whether the CSRF workaround below applies.
const (
	schemeToken  = "Token"  // a fixed InvenTree API token
	schemeBearer = "Bearer" // a forwarded OAuth access token
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
		scheme:  schemeToken,
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
	derived.scheme = schemeBearer
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

	if c.scheme == schemeBearer && !csrfSafeMethod(method) {
		if err := setCSRFHeaders(req, c.baseURL); err != nil {
			return nil, fmt.Errorf("%s %s: %w", method, pathPart, err)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %s", method, pathPart, sanitizeError(err))
	}
	return resp, nil
}

// csrfSafeMethod reports whether Django exempts a method from CSRF checking.
// The list matches CsrfViewMiddleware.process_view, which accepts these
// unconditionally — which is exactly why reads work today and writes do not.
func csrfSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// setCSRFHeaders satisfies Django's CSRF check on a bearer-authenticated write.
//
// InvenTree lists SessionAuthentication *before* ExtendedOAuth2Authentication in
// DEFAULT_AUTHENTICATION_CLASSES, and runs oauth2_provider's OAuth2TokenMiddleware,
// which populates request.user from the bearer token before DRF is reached. So
// SessionAuthentication sees an active user, calls enforce_csrf(), and rejects
// the request — the OAuth authenticator never gets a turn. GET is exempt, so the
// symptom is that every read succeeds and every write 403s with
// {"detail":"CSRF Failed: ..."}, which reads exactly like a permissions problem.
// The API-token path is unaffected because ApiTokenAuthentication is first in
// the list and returns before SessionAuthentication runs.
//
// Django's default CSRF is double-submit: CSRF_USE_SESSIONS is unset, so
// _get_secret reads the csrftoken cookie and compares it against the
// X-CSRFToken header and nothing else. The value is never tied to a session, so
// a self-generated one satisfies the check. Origin is sent because
// _origin_verified returns true immediately when it equals the request's own
// host, which skips the stricter Referer check on HTTPS without depending on
// how CSRF_TRUSTED_ORIGINS happens to be configured.
//
// This is not a bypass of protection that was doing anything. CSRF defends
// *ambient* credentials — cookies a browser attaches on its own. This client
// sends no session cookie and authenticates with an explicit Authorization
// header, so there is no cross-site request for the check to prevent; it is
// running only because of the authenticator ordering above. If InvenTree
// reorders those classes, these headers become inert rather than breaking.
func setCSRFHeaders(req *http.Request, baseURL string) error {
	token, err := csrfToken()
	if err != nil {
		return err
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("parsing base URL for CSRF origin: %w", err)
	}

	req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	req.Header.Set("X-CSRFToken", token)
	req.AddCookie(&http.Cookie{Name: "csrftoken", Value: token})
	return nil
}

// csrfToken returns a value Django accepts as a CSRF secret. _check_token_format
// requires exactly CSRF_SECRET_LENGTH (32) or CSRF_TOKEN_LENGTH (64) characters
// drawn from ASCII letters and digits; hex satisfies both constraints without
// the modulo bias of folding random bytes into a 62-character alphabet.
//
// It is generated per request and compared only against itself, so it carries no
// secrecy requirement — crypto/rand is used because there is no reason not to.
func csrfToken() (string, error) {
	b := make([]byte, 16) // 16 bytes -> 32 hex characters
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating CSRF token: %w", err)
	}
	return hex.EncodeToString(b), nil
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
		return apiError(resp, data)
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
func apiError(resp *http.Response, body []byte) error {
	status := resp.StatusCode
	msg := strings.TrimSpace(string(body))

	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("authentication failed (401): %s", authDetail(resp, msg,
			"the credential was rejected; check INVENTREE_TOKEN, or reconnect if using OAuth"))
	case http.StatusForbidden:
		// 401 and 403 have two very different causes that need opposite fixes:
		// a scope the token was never granted (reconnect to re-consent) versus
		// an InvenTree role the user does not hold (grant the role). Only the
		// response says which, so never discard it — see authDetail.
		return fmt.Errorf("permission denied (403): %s", authDetail(resp, msg,
			"the credential is valid but not permitted to perform this operation"))
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

// authDetail explains a 401/403 using what the server actually reported.
//
// OAuth-aware servers signal the cause in WWW-Authenticate (RFC 6750): an
// `error=insufficient_scope` means the token is fine but was never granted the
// scope, which no amount of changing InvenTree roles will fix — the user must
// reconnect and re-consent. Django REST Framework instead returns a `detail`
// string in the body when a *role* permission is missing. Reporting whichever
// is present is what makes the two distinguishable; the canned fallback is used
// only when the server said nothing useful.
func authDetail(resp *http.Response, body, fallback string) string {
	if resp != nil {
		if e, scope := bearerError(resp.Header.Get("WWW-Authenticate")); e != "" {
			switch e {
			case "insufficient_scope":
				s := "the token was not granted the scope this operation needs"
				if scope != "" {
					s += " (" + scope + ")"
				}
				return s + "; disconnect and reconnect the connector to re-consent " +
					"with the scopes the server now advertises"
			case "invalid_token":
				return "the token is expired, revoked, or malformed; reconnect the connector"
			default:
				return "OAuth error " + e
			}
		}
	}

	// Django REST Framework's {"detail": "..."} is the role-permission case.
	if d := jsonDetail(body); d != "" {
		return d + " — this is an InvenTree role permission, not an OAuth scope: " +
			"grant the user's group the matching role"
	}

	if body != "" {
		if len(body) > 300 {
			body = body[:300] + "..."
		}
		// Redacted for the same reason network errors are: this is the one path
		// that puts an unparsed upstream body into a message the model sees.
		return redactToken(body)
	}
	return fallback
}

// bearerError extracts the error and scope parameters from a WWW-Authenticate
// challenge, e.g. `Bearer realm="api", error="insufficient_scope", scope="r:add:part"`.
func bearerError(header string) (errCode, scope string) {
	if header == "" {
		return "", ""
	}
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "error":
			errCode = v
		case "scope":
			scope = v
		}
	}
	return errCode, scope
}

// jsonDetail pulls the "detail" field out of a DRF error body, if present.
func jsonDetail(body string) string {
	var payload struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Detail)
}

// sanitizeError strips potential credential info from network errors.
func sanitizeError(err error) string {
	return redactToken(err.Error())
}

// redactToken removes any token value that might appear in a string bound for
// a log line or a tool result.
func redactToken(s string) string {
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
