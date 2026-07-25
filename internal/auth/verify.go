// Package auth verifies OAuth 2.0 bearer tokens presented by MCP clients.
//
// The MCP server is only an OAuth *resource server*. It never issues tokens and
// runs no authorization endpoints — InvenTree's django-oauth-toolkit is the
// authorization server. InvenTree's tokens are opaque, so they cannot be
// verified locally by signature and must be checked against InvenTree itself.
//
// Two strategies are available; see [NewUserInfoVerifier] and [NewIntrospector]
// for when each applies.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// cacheTTL bounds how long a successful verification is reused. Every MCP
// request carries a token, so re-verifying each one would put a request on
// InvenTree per tool call. Entries are additionally clamped to the token's own
// expiry when that is known.
const cacheTTL = 60 * time.Second

// A Verifier checks a bearer token against InvenTree.
type Verifier interface {
	Verify(ctx context.Context, token string, req *http.Request) (*mcpauth.TokenInfo, error)
}

// tokenCache memoizes verification results, keyed by token.
type tokenCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	info      *mcpauth.TokenInfo
	expiresAt time.Time
}

func newTokenCache() *tokenCache {
	return &tokenCache{entries: make(map[string]cacheEntry)}
}

func (c *tokenCache) get(token string) (*mcpauth.TokenInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[token]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.info, true
}

func (c *tokenCache) put(token string, info *mcpauth.TokenInfo) {
	expiresAt := time.Now().Add(cacheTTL)
	if !info.Expiration.IsZero() && info.Expiration.Before(expiresAt) {
		expiresAt = info.Expiration
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Bound growth without a background sweeper: drop expired entries whenever
	// the map gets large. Tokens are long-lived relative to cacheTTL, so the
	// live set stays small in practice.
	if len(c.entries) > 1024 {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[token] = cacheEntry{info: info, expiresAt: expiresAt}
}

// -- OIDC UserInfo strategy --

// UserInfoVerifier validates a token by presenting it to the authorization
// server's OIDC UserInfo endpoint. A 200 means InvenTree accepts the token,
// which is exactly the property this server relies on when it forwards the same
// token to the InvenTree API.
//
// This needs no client credentials and works against any InvenTree instance
// that advertises a userinfo_endpoint — unlike introspection, which InvenTree
// does not advertise in its discovery document and exposes no usable scope for.
//
// UserInfo reports neither expiry nor scope, so:
//   - Expiration is set to the cache horizon, not the token's real lifetime.
//     InvenTree stays the authority: once the cached entry lapses the token is
//     presented again, and one InvenTree has since rejected fails then. A token
//     that expires mid-window fails at the InvenTree call itself.
//   - Scopes are unknown, so scope gating cannot be enforced here. InvenTree
//     enforces scopes per endpoint against the forwarded token, which is where
//     the real check belongs.
type UserInfoVerifier struct {
	endpoint   string
	httpClient *http.Client
	cache      *tokenCache
}

// NewUserInfoVerifier returns a Verifier that calls the OIDC UserInfo endpoint.
func NewUserInfoVerifier(endpoint string) *UserInfoVerifier {
	return &UserInfoVerifier{
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		cache:      newTokenCache(),
	}
}

// Verify implements [Verifier].
func (v *UserInfoVerifier) Verify(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	if info, ok := v.cache.get(token); ok {
		return info, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling userinfo endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading userinfo response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: InvenTree rejected the token", mcpauth.ErrInvalidToken)
	case resp.StatusCode != http.StatusOK:
		// Anything else is InvenTree misbehaving, not a bad credential.
		return nil, fmt.Errorf("userinfo endpoint returned %d: %.200s", resp.StatusCode, body)
	}

	var claims struct {
		Sub               string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("decoding userinfo response: %w", err)
	}
	if claims.Sub == "" && claims.PreferredUsername == "" {
		return nil, fmt.Errorf("%w: userinfo response identifies no user", mcpauth.ErrInvalidToken)
	}

	info := &mcpauth.TokenInfo{
		// Not the token's real expiry — see the type doc.
		Expiration: time.Now().Add(cacheTTL),
		UserID:     firstNonEmpty(claims.Sub, claims.PreferredUsername),
	}
	v.cache.put(token, info)
	return info, nil
}

// -- RFC 7662 introspection strategy --

// Introspector validates tokens against an RFC 7662 introspection endpoint,
// authenticating with HTTP Basic client credentials (RFC 7662 §2.1).
//
// Prefer [UserInfoVerifier] on InvenTree: introspection is absent from
// InvenTree's discovery document and it publishes no `introspection` scope, so
// whether the endpoint accepts client-credential auth depends on the
// django-oauth-toolkit version. Use this against an authorization server that
// documents introspection, or when scope gating must happen at this server —
// introspection is the only strategy that reports the token's scopes.
type Introspector struct {
	endpoint     string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	cache        *tokenCache
}

// NewIntrospector returns a Verifier that calls an RFC 7662 endpoint.
func NewIntrospector(endpoint, clientID, clientSecret string) *Introspector {
	return &Introspector{
		endpoint:     endpoint,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		cache:        newTokenCache(),
	}
}

// introspectionResponse is the subset of RFC 7662 fields this server uses.
type introspectionResponse struct {
	Active   bool   `json:"active"`
	Scope    string `json:"scope"`
	Username string `json:"username"`
	Exp      int64  `json:"exp"`
	Sub      string `json:"sub"`
}

// Verify implements [Verifier].
//
// It returns an error wrapping mcpauth.ErrInvalidToken when the token is
// rejected, which the SDK middleware turns into a 401. Transport failures
// return a bare error so they surface as a 500 — a temporarily unreachable
// InvenTree must not be reported to the client as an invalid credential.
func (i *Introspector) Verify(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	if info, ok := i.cache.get(token); ok {
		return info, nil
	}

	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building introspection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(i.clientID, i.clientSecret)

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling introspection endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading introspection response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// A 401/403 here means *our* client credentials are wrong, not the
		// caller's token. Surface it as a server fault so the misconfiguration
		// is visible instead of being reported to every user as a bad token.
		return nil, fmt.Errorf("introspection endpoint returned %d: %.200s", resp.StatusCode, body)
	}

	var ir introspectionResponse
	if err := json.Unmarshal(body, &ir); err != nil {
		return nil, fmt.Errorf("decoding introspection response: %w", err)
	}
	if !ir.Active {
		return nil, fmt.Errorf("%w: token is not active", mcpauth.ErrInvalidToken)
	}
	// The SDK rejects a TokenInfo with a zero Expiration, so a response without
	// exp cannot be honoured — fail loudly rather than mint an endless session.
	if ir.Exp == 0 {
		return nil, fmt.Errorf("%w: introspection response has no exp claim", mcpauth.ErrInvalidToken)
	}

	info := &mcpauth.TokenInfo{
		Scopes:     strings.Fields(ir.Scope),
		Expiration: time.Unix(ir.Exp, 0),
		UserID:     firstNonEmpty(ir.Username, ir.Sub),
	}
	i.cache.put(token, info)
	return info, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
