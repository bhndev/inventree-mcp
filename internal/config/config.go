package config

import (
	"fmt"
	"os"
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

// Config holds the InvenTree connection and transport settings.
type Config struct {
	URL   string
	Token string

	// Transport settings. Host/Port/AuthToken only apply to TransportHTTP.
	Transport Transport
	Host      string
	Port      string
	AuthToken string

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

	token := os.Getenv("INVENTREE_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("INVENTREE_TOKEN environment variable is required")
	}

	transport, err := loadTransport()
	if err != nil {
		return nil, err
	}

	authToken := os.Getenv("MCP_AUTH_TOKEN")
	// Refuse to start an unauthenticated HTTP listener. Every tool this server
	// exposes can mutate real inventory, so serving them without a credential
	// must be a deliberate act, not the result of an unset variable.
	if transport == TransportHTTP && authToken == "" {
		return nil, fmt.Errorf("MCP_AUTH_TOKEN is required when MCP_TRANSPORT=http " +
			"(generate one with: openssl rand -hex 32)")
	}

	return &Config{
		URL:          url,
		Token:        token,
		Transport:    transport,
		Host:         envOr("MCP_HOST", "127.0.0.1"),
		Port:         envOr("MCP_PORT", "8000"),
		AuthToken:    authToken,
		GoogleAPIKey: os.Getenv("GOOGLE_API_KEY"),
		GoogleCSEID:  os.Getenv("GOOGLE_CSE_ID"),
	}, nil
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
