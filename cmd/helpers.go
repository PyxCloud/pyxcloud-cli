package cmd

import (
	"fmt"
	"os"

	"github.com/pyxcloud/pyxcloud-cli/internal/api"
	"github.com/pyxcloud/pyxcloud-cli/internal/config"
)

// getClient builds an API client from config + flags.
func getClient() (*api.Client, error) {
	if !config.ValidProfile(profile) {
		return nil, fmt.Errorf("unknown profile %q (supported: sandbox, staging)", profile)
	}
	cfg, err := config.LoadProfile(profile)
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
		cfg.APIURL = "https://beta-api.pyxcloud.io"
	}

	return api.NewClientFromConfig(cfg), nil
}
