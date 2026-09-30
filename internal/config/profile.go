package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Profiles supported by the CLI (ROADMAP wave B, cli-M0 slice 1). The default
// (empty) profile keeps the original ~/.pyxcloud/config.json file; a named
// profile stores credentials in a separate per-profile file so sandbox and
// staging logins never mix with production tokens.
const (
	ProfileSandbox = "sandbox"
	ProfileStaging = "staging"
)

// ValidProfile reports whether name is a supported profile. The empty string
// is the default (production) profile.
func ValidProfile(name string) bool {
	switch name {
	case "", ProfileSandbox, ProfileStaging:
		return true
	}
	return false
}

// SetPathForTests overrides the resolved config directory; it exists for
// unit tests so they never touch the real ~/.pyxcloud files.
func SetPathForTests(dir string) {
	configPath = filepath.Join(dir, configDir, configFileName)
}

// ProfilePath returns the config file path for the given profile. The default
// profile keeps the historical config.json name.
func ProfilePath(name string) (string, error) {
	if !ValidProfile(name) {
		return "", fmt.Errorf("unknown profile %q (supported: %s, %s)", name, ProfileSandbox, ProfileStaging)
	}
	if name == "" {
		return configPath, nil
	}
	if configPath == "" {
		return "", fmt.Errorf("cannot determine config path")
	}
	return filepath.Join(filepath.Dir(configPath), "config."+name+".json"), nil
}

// LoadProfile reads the config file of the given profile.
func LoadProfile(name string) (*Config, error) {
	p, err := ProfilePath(name)
	if err != nil {
		return nil, err
	}
	return loadPath(p)
}

// SaveProfile writes the config file of the given profile.
func SaveProfile(name string, cfg *Config) error {
	p, err := ProfilePath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return savePath(p, cfg)
}
