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

	return &Config{URL: url, Token: token}, nil
}
