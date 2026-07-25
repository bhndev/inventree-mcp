package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// Transport identifies how the MCP server talks to its client.
type Transport string

const (
	// TransportStdio serves MCP over stdin/stdout. This is the default so that
	// local Claude Desktop / Claude Code configs keep working unchanged.
	TransportStdio Transport = "stdio"
	// TransportHTTP serves MCP over Streamable HTTP for a hosted deployment.
	TransportHTTP Transport = "http"
)

// AuthMode identifies how HTTP callers authenticate to this server.
type AuthMode string

const (
	// AuthToken is a single shared bearer token held by the whole organization.
	// Simple, but every caller is indistinguishable and InvenTree sees only the
	// service account.
	AuthToken AuthMode = "token"
	// AuthOAuth verifies per-user OAuth 2.0 tokens issued by InvenTree. This
	// server acts purely as a resource server.
	AuthOAuth AuthMode = "oauth"
)

// VerifyMode selects how an OAuth access token is checked against InvenTree.
type VerifyMode string

const (
	// VerifyUserInfo presents the token to the OIDC UserInfo endpoint. This is
	// the default because it is what InvenTree actually advertises, and it needs
	// no client credentials.
	VerifyUserInfo VerifyMode = "userinfo"
	// VerifyIntrospect uses an RFC 7662 introspection endpoint. It is the only
	// mode that reports token scopes, but InvenTree neither advertises the
	// endpoint nor publishes a scope granting access to it.
	VerifyIntrospect VerifyMode = "introspect"
)

// Config holds the InvenTree connection and transport settings.
type Config struct {
	URL   string
	Token string

	// Transport settings. Everything below applies only to TransportHTTP.
	Transport Transport
	Host      string
	Port      string

	AuthMode AuthMode

	// AuthToken is the shared secret, used when AuthMode is AuthToken.
	AuthToken string

	// PublicURL is this server's canonical externally reachable base URL, e.g.
	// https://inventree-mcp.example.com. It must match what the user types into
	// Claude exactly: RFC 9728 requires the advertised `resource` identifier to
	// be identical to the entered URL, and a mismatch fails the OAuth flow.
	PublicURL string

	// OAuthScopes is the set Claude is asked to request during consent. It is
	// advertised in the protected resource metadata. Whether this server also
	// *enforces* them depends on OAuthVerify: only introspection reports the
	// scopes a token actually carries.
	//
	// Do not treat a trimmed scope list as an access control on its own.
	// Measured against a live InvenTree 1.4 instance, a token granted only
	// "openid g:read r:view:part" could still read /api/order/po/. That test
	// ran as a superuser, so it does not distinguish "superusers bypass scope
	// checks" from "the API does not gate on scopes at all" — but until that is
	// settled, the effective limit on a forwarded token is the InvenTree user's
	// own role permissions. Connect as a user whose roles match the access the
	// tools should have.
	//
	// OAuth settings, used when AuthMode is AuthOAuth.
	OAuthIssuer           string
	OAuthVerify           VerifyMode
	OAuthUserInfoURL      string
	OAuthIntrospectionURL string
	OAuthClientID         string
	OAuthClientSecret     string
	OAuthScopes           []string

	// Optional: Google Custom Search API credentials for image search.
	GoogleAPIKey string
	GoogleCSEID  string
}

// ImageSearchConfigured reports whether Google image search credentials are set.
func (c *Config) ImageSearchConfigured() bool {
	return c.GoogleAPIKey != "" && c.GoogleCSEID != ""
}

// Addr returns the host:port the HTTP transport should listen on.
func (c *Config) Addr() string {
	return c.Host + ":" + c.Port
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	url := strings.TrimRight(os.Getenv("INVENTREE_URL"), "/")
	if url == "" {
		return nil, fmt.Errorf("INVENTREE_URL environment variable is required")
	}

	transport, err := loadTransport()
	if err != nil {
		return nil, err
	}
	mode, err := loadAuthMode()
	if err != nil {
		return nil, err
	}
	verify, err := loadVerifyMode()
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		URL:                   url,
		Token:                 os.Getenv("INVENTREE_TOKEN"),
		Transport:             transport,
		Host:                  envOr("MCP_HOST", "127.0.0.1"),
		Port:                  envOr("MCP_PORT", "8000"),
		AuthMode:              mode,
		AuthToken:             os.Getenv("MCP_AUTH_TOKEN"),
		PublicURL:             strings.TrimRight(os.Getenv("MCP_PUBLIC_URL"), "/"),
		OAuthIssuer:           strings.TrimRight(os.Getenv("OAUTH_ISSUER"), "/"),
		OAuthVerify:           verify,
		OAuthUserInfoURL:      os.Getenv("OAUTH_USERINFO_URL"),
		OAuthIntrospectionURL: os.Getenv("OAUTH_INTROSPECTION_URL"),
		OAuthClientID:         os.Getenv("OAUTH_CLIENT_ID"),
		OAuthClientSecret:     os.Getenv("OAUTH_CLIENT_SECRET"),
		OAuthScopes:           strings.Fields(os.Getenv("OAUTH_SCOPES")),
		GoogleAPIKey:          os.Getenv("GOOGLE_API_KEY"),
		GoogleCSEID:           os.Getenv("GOOGLE_CSE_ID"),
	}

	if transport == TransportHTTP {
		if err := cfg.validateHTTP(); err != nil {
			return nil, err
		}
	}
	// A service-account token is only needed when this server calls InvenTree on
	// its own behalf. When it forwards each caller's own token, requiring one
	// would mean provisioning a credential that is never used.
	if !cfg.ForwardsCallerCredentials() && cfg.Token == "" {
		return nil, fmt.Errorf("INVENTREE_TOKEN environment variable is required")
	}
	return cfg, nil
}

// ForwardsCallerCredentials reports whether InvenTree calls are attributed to
// the end user who made the MCP request rather than to a shared service
// account. This is the case exactly when per-user OAuth tokens are verified.
func (c *Config) ForwardsCallerCredentials() bool {
	return c.Transport == TransportHTTP && c.AuthMode == AuthOAuth
}

// validateHTTP enforces that a network listener is never started without a
// complete, usable authentication configuration.
func (c *Config) validateHTTP() error {
	switch c.AuthMode {
	case AuthToken:
		// Refuse to start an unauthenticated listener. Every tool this server
		// exposes can mutate real inventory, so serving them without a
		// credential must be deliberate, not the result of an unset variable.
		if c.AuthToken == "" {
			return fmt.Errorf("MCP_AUTH_TOKEN is required when MCP_TRANSPORT=http " +
				"and MCP_AUTH_MODE=token (generate one with: openssl rand -hex 32)")
		}
	case AuthOAuth:
		missing := []string{}
		if c.PublicURL == "" {
			missing = append(missing, "MCP_PUBLIC_URL")
		}
		if c.OAuthIssuer == "" {
			missing = append(missing, "OAUTH_ISSUER")
		}
		if c.OAuthVerify == VerifyIntrospect {
			// Only introspection authenticates this server to InvenTree.
			if c.OAuthClientID == "" {
				missing = append(missing, "OAUTH_CLIENT_ID")
			}
			if c.OAuthClientSecret == "" {
				missing = append(missing, "OAUTH_CLIENT_SECRET")
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("MCP_AUTH_MODE=oauth (verify=%s) requires %s",
				c.OAuthVerify, strings.Join(missing, ", "))
		}
		if !strings.HasPrefix(c.PublicURL, "https://") && !strings.HasPrefix(c.PublicURL, "http://") {
			return fmt.Errorf("MCP_PUBLIC_URL must be an absolute URL, got %q", c.PublicURL)
		}
		// OAUTH_SCOPES is the set Claude is asked to request. Leaving it empty
		// does not mean "no scopes": Claude falls back to whatever the
		// authorization server advertises, which on InvenTree is every scope it
		// knows, including r:delete:*. Demand an explicit list so consent is
		// scoped to what the tools actually need.
		if len(c.OAuthScopes) == 0 {
			return fmt.Errorf("OAUTH_SCOPES is required with MCP_AUTH_MODE=oauth: " +
				"without it Claude requests every scope InvenTree advertises, " +
				"including delete permissions")
		}
		// UserInfo is an OIDC endpoint and rejects a token lacking this scope,
		// which would make every request look like a bad credential.
		if c.OAuthVerify == VerifyUserInfo && !slices.Contains(c.OAuthScopes, "openid") {
			return fmt.Errorf("OAUTH_SCOPES must include %q with OAUTH_VERIFY=userinfo: "+
				"the UserInfo endpoint rejects tokens without it", "openid")
		}
		// django-oauth-toolkit mounts both under the issuer prefix.
		if c.OAuthUserInfoURL == "" {
			c.OAuthUserInfoURL = c.OAuthIssuer + "/userinfo/"
		}
		if c.OAuthIntrospectionURL == "" {
			c.OAuthIntrospectionURL = c.OAuthIssuer + "/introspect/"
		}
	}
	return nil
}

// ResourceMetadataURL is the RFC 9728 protected-resource-metadata location
// advertised in the WWW-Authenticate header of a 401.
func (c *Config) ResourceMetadataURL() string {
	return c.PublicURL + "/.well-known/oauth-protected-resource"
}

func loadTransport() (Transport, error) {
	switch t := Transport(strings.ToLower(os.Getenv("MCP_TRANSPORT"))); t {
	case "", TransportStdio:
		return TransportStdio, nil
	case TransportHTTP:
		return TransportHTTP, nil
	default:
		return "", fmt.Errorf("unknown MCP_TRANSPORT %q: want %q or %q", t, TransportStdio, TransportHTTP)
	}
}

func loadAuthMode() (AuthMode, error) {
	switch m := AuthMode(strings.ToLower(os.Getenv("MCP_AUTH_MODE"))); m {
	case "", AuthToken:
		return AuthToken, nil
	case AuthOAuth:
		return AuthOAuth, nil
	default:
		return "", fmt.Errorf("unknown MCP_AUTH_MODE %q: want %q or %q", m, AuthToken, AuthOAuth)
	}
}

func loadVerifyMode() (VerifyMode, error) {
	switch v := VerifyMode(strings.ToLower(os.Getenv("OAUTH_VERIFY"))); v {
	case "", VerifyUserInfo:
		return VerifyUserInfo, nil
	case VerifyIntrospect:
		return VerifyIntrospect, nil
	default:
		return "", fmt.Errorf("unknown OAUTH_VERIFY %q: want %q or %q", v, VerifyUserInfo, VerifyIntrospect)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
