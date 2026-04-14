package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	// Use temp dir to avoid polluting real config
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")

	// Override configPath for testing
	original := configPath
	configPath = configFile
	defer func() { configPath = original }()

	cfg := &Config{
		Token:  "pyxc_testtoken12345",
		APIURL: "https://beta-api.pyxcloud.io",
	}
	err := Save(cfg)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Token != cfg.Token {
		t.Errorf("Token mismatch: got %q, want %q", loaded.Token, cfg.Token)
	}
	if loaded.APIURL != cfg.APIURL {
		t.Errorf("APIURL mismatch: got %q, want %q", loaded.APIURL, cfg.APIURL)
	}
}

func TestLoadMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "nonexistent", "config.json")

	original := configPath
	configPath = configFile
	defer func() { configPath = original }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load of missing file should return empty config, got error: %v", err)
	}
	if cfg.Token != "" {
		t.Errorf("Expected empty token, got %q", cfg.Token)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "new", "dir", "config.json")

	original := configPath
	configPath = configFile
	defer func() { configPath = original }()

	err := Save(&Config{Token: "test"})
	if err != nil {
		t.Fatalf("Save should create parent dirs, got error: %v", err)
	}

	_, statErr := os.Stat(configFile)
	if statErr != nil {
		t.Errorf("Config file should exist after Save, got: %v", statErr)
	}
}

func TestSaveEmptyConfigClearsToken(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")

	original := configPath
	configPath = configFile
	defer func() { configPath = original }()

	// Save with token, then save empty (logout)
	Save(&Config{Token: "pyxc_abc123", APIURL: "http://test"})
	Save(&Config{})

	loaded, _ := Load()
	if loaded.Token != "" {
		t.Errorf("Token should be empty after logout, got %q", loaded.Token)
	}
}
