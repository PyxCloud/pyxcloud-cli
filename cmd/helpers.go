package cmd

import (
	"fmt"
	"os"

	"github.com/pyxcloud/pyxcloud-cli/internal/api"
	"github.com/pyxcloud/pyxcloud-cli/internal/config"
)

// getClient builds an API client from config + flags.
func getClient() (*api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if cfg.Token == "" && cfg.RefreshToken == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run: pyxcloud auth login")
		os.Exit(1)
	}

	if apiURL != "" {
		cfg.APIURL = apiURL // flag override
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "http://localhost:8080"
	}

	return api.NewClientFromConfig(cfg), nil
}
