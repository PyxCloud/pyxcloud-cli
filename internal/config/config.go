package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds CLI persistent settings.
type Config struct {
	Token        string `json:"token"`                   // access_token (short-lived JWT)
	RefreshToken string `json:"refresh_token,omitempty"`  // offline refresh_token (long-lived)
	APIURL       string `json:"api_url"`
	AuthURL      string `json:"auth_url,omitempty"`       // Keycloak realm URL for token refresh
	ClientID     string `json:"client_id,omitempty"`      // OAuth2 client ID
}

const configDir = ".pyxcloud"
const configFileName = "config.json"

// configPath is the resolved path to the config file.
// Package variable so tests can override it.
var configPath string

func init() {
	home, err := os.UserHomeDir()
	if err == nil {
		configPath = filepath.Join(home, configDir, configFileName)
	}
}

// Load reads the config file.
func Load() (*Config, error) {
	if configPath == "" {
		return nil, fmt.Errorf("cannot determine config path")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// Save writes the config file.
func Save(cfg *Config) error {
	if configPath == "" {
		return fmt.Errorf("cannot determine config path")
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0600)
}
