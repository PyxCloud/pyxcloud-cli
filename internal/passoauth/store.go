package passoauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Token is an OAuth token set held by the local credential store.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Store persists tokens for named profiles.
type Store interface {
	Load(profile string) (Token, error)
	Save(profile string, token Token) error
	Delete(profile string) error
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

type inputCommandRunner func(context.Context, []byte, string, ...string) ([]byte, error)

type keychainStore struct {
	inputRunner inputCommandRunner
	runner      commandRunner
	platform    string
}

// NewKeychainStore creates a store backed by macOS Keychain.
func NewKeychainStore() Store {
	return &keychainStore{inputRunner: func(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdin = strings.NewReader(string(input))
		return cmd.Output()
	}, platform: runtime.GOOS, runner: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		return cmd.Output()
	}}
}

func (s *keychainStore) Load(profile string) (Token, error) {
	if err := validateStoreProfile(profile); err != nil {
		return Token{}, err
	}
	if s.platform != "darwin" {
		return Token{}, errors.New("Keychain token storage is supported only on macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := s.runner(ctx, "security", "find-generic-password", "-a", "oauth", "-s", "passo-cli/"+profile, "-w")
	if err != nil {
		return Token{}, errors.New("could not load credentials from Keychain")
	}
	var token Token
	if err := json.Unmarshal(out, &token); err != nil {
		return Token{}, errors.New("invalid credentials stored in Keychain")
	}
	return token, nil
}

func (s *keychainStore) Save(profile string, token Token) error {
	if err := validateStoreProfile(profile); err != nil {
		return err
	}
	if s.platform != "darwin" {
		return errors.New("Keychain token storage is supported only on macOS")
	}
	data, err := json.Marshal(token)
	if err != nil {
		return errors.New("could not encode credentials")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// macOS security prompts twice when -w is the final option. JSON marshal
	// escapes control characters, so each prompt receives one bounded line.
	input := append(append(append([]byte{}, data...), '\n'), data...)
	input = append(input, '\n')
	_, err = s.inputRunner(ctx, input, "security", "add-generic-password", "-U", "-a", "oauth", "-s", "passo-cli/"+profile, "-w")
	if err != nil {
		return errors.New("could not save credentials to Keychain")
	}
	return nil
}

func (s *keychainStore) Delete(profile string) error {
	if err := validateStoreProfile(profile); err != nil {
		return err
	}
	if s.platform != "darwin" {
		return errors.New("Keychain token storage is supported only on macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.runner(ctx, "security", "delete-generic-password", "-a", "oauth", "-s", "passo-cli/"+profile)
	if err != nil {
		return errors.New("could not delete credentials from Keychain")
	}
	return nil
}

func validateStoreProfile(profile string) error {
	if profile != "sandbox" && profile != "staging" {
		return fmt.Errorf("invalid profile %q", profile)
	}
	return nil
}
