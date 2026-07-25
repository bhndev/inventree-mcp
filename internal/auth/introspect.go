// Package auth verifies OAuth 2.0 bearer tokens presented by MCP clients.
//
// The MCP server is only an OAuth *resource server*. It never issues tokens and
// runs no authorization endpoints — InvenTree's django-oauth-toolkit is the
// authorization server. Tokens issued by it are opaque, so they are validated by
// RFC 7662 introspection rather than by verifying a signature locally.
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

// cacheTTL bounds how long a positive introspection result is reused. Every MCP
// request carries a token, so introspecting each one would put a request on
// InvenTree per tool call. The cached entry is additionally clamped to the
// token's own expiry, so a short-lived token is never honoured past it.
const cacheTTL = 60 * time.Second

// Introspector validates opaque bearer tokens against an RFC 7662 endpoint.
type Introspector struct {
	endpoint     string
	clientID     string
	clientSecret string
	httpClient   *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	info      *mcpauth.TokenInfo
	expiresAt time.Time
}

// NewIntrospector returns an Introspector that authenticates to the
// introspection endpoint with HTTP Basic client credentials (RFC 7662 §2.1).
func NewIntrospector(endpoint, clientID, clientSecret string) *Introspector {
	return &Introspector{
		endpoint:     endpoint,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		cache:        make(map[string]cacheEntry),
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

// Verify implements mcpauth.TokenVerifier.
//
// It returns an error wrapping mcpauth.ErrInvalidToken when the token is
// rejected, which the SDK middleware turns into a 401. Transport failures
// return a bare error so they surface as a 500 — a temporarily unreachable
// InvenTree must not be reported to the client as an invalid credential.
func (i *Introspector) Verify(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	if info, ok := i.cached(token); ok {
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
		// A 401 here means *our* client credentials are wrong, not the caller's
		// token. Surface it as a server fault so the misconfiguration is visible
		// instead of being reported to every user as a bad token.
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
	i.store(token, info)
	return info, nil
}

func (i *Introspector) cached(token string) (*mcpauth.TokenInfo, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	e, ok := i.cache[token]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.info, true
}

func (i *Introspector) store(token string, info *mcpauth.TokenInfo) {
	expiresAt := time.Now().Add(cacheTTL)
	if info.Expiration.Before(expiresAt) {
		expiresAt = info.Expiration
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	// Bound growth without a background sweeper: drop expired entries whenever
	// the map gets large. Tokens are long-lived relative to cacheTTL, so the
	// live set stays small in practice.
	if len(i.cache) > 1024 {
		now := time.Now()
		for k, v := range i.cache {
			if now.After(v.expiresAt) {
				delete(i.cache, k)
			}
		}
	}
	i.cache[token] = cacheEntry{info: info, expiresAt: expiresAt}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
