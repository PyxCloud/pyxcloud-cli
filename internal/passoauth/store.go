package passoauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// ErrCredentialStoreUnavailable reports a failed native store operation without
// carrying native messages, credentials or an assumption about token expiry.
// ErrCredentialAccessRequired means the OS refused a noninteractive read.
// It does not establish a missing token or an expired login.
var ErrCredentialAccessRequired = errors.New("credential access required")

var ErrCredentialStoreUnavailable = errors.New("credential store unavailable")

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

type keychainStore struct {
	nativeSave   func(string, string, []byte) error
	nativeLoad   func(string, string) ([]byte, error)
	nativeDelete func(string, string) error
	platform     string
}

func NewKeychainStore() Store {
	return &keychainStore{nativeSave: nativeKeychainSave, nativeLoad: nativeKeychainLoad, nativeDelete: nativeKeychainDelete, platform: runtime.GOOS}
}

func (s *keychainStore) Load(profile string) (Token, error) {
	if err := validateStoreProfile(profile); err != nil {
		return Token{}, err
	}
	if s.platform != "darwin" {
		return Token{}, errors.New("Keychain token storage is supported only on macOS")
	}
	out, err := s.nativeLoad("passo-cli/"+profile, "oauth")
	if errors.Is(err, ErrCredentialAccessRequired) {
		return Token{}, ErrCredentialAccessRequired
	}
	if err != nil {
		return Token{}, ErrCredentialStoreUnavailable
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
	err = s.nativeSave("passo-cli/"+profile, "oauth", data)
	if errors.Is(err, ErrCredentialAccessRequired) {
		return ErrCredentialAccessRequired
	}
	if err != nil {
		return ErrCredentialStoreUnavailable
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
	err := s.nativeDelete("passo-cli/"+profile, "oauth")
	if err != nil {
		return ErrCredentialStoreUnavailable
	}
	return nil
}

func validateStoreProfile(profile string) error {
	if profile != "sandbox" && profile != "staging" {
		return fmt.Errorf("invalid profile %q", profile)
	}
	return nil
}
