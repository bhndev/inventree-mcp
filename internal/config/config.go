package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the InvenTree connection settings.
type Config struct {
	URL   string
	Token string

	// Optional: Google Custom Search API credentials for image search.
	GoogleAPIKey string
	GoogleCSEID  string
}

// ImageSearchConfigured reports whether Google image search credentials are set.
func (c *Config) ImageSearchConfigured() bool {
	return c.GoogleAPIKey != "" && c.GoogleCSEID != ""
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

	return &Config{
		URL:          url,
		Token:        token,
		GoogleAPIKey: os.Getenv("GOOGLE_API_KEY"),
		GoogleCSEID:  os.Getenv("GOOGLE_CSE_ID"),
	}, nil
}
