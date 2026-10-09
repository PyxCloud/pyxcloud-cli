package passoauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestKeychainStoreSaveLoadDelete(t *testing.T) {
	s := NewKeychainStore().(*keychainStore)
	s.platform = "darwin"
	data := map[string][]byte{}
	s.nativeLoad = func(service, account string) ([]byte, error) {
		v, ok := data[service]
		if !ok {
			return nil, errors.New("absent")
		}
		return v, nil
	}
	s.nativeDelete = func(service, account string) error { delete(data, service); return nil }
	s.nativeSave = func(service, account string, input []byte) error {
		if account != "oauth" {
			t.Fatal("wrong account")
		}
		data[service] = append([]byte{}, input...)
		return nil
	}
	want := Token{AccessToken: "access-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := s.Save("sandbox", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) || got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if err := s.Delete("sandbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("sandbox"); err == nil {
		t.Fatal("expected missing credential error")
	}
}

func TestKeychainStoreRejectsInvalidProfileBeforeRunning(t *testing.T) {
	s := NewKeychainStore().(*keychainStore)
	s.platform = "darwin"
	runs := 0
	s.nativeSave = func(string, string, []byte) error { runs++; return nil }
	if err := s.Save("../sandbox", Token{AccessToken: "secret"}); err == nil {
		t.Fatal("expected invalid profile error")
	}
	if runs != 0 {
		t.Fatalf("runner called %d times", runs)
	}
}

func TestKeychainStoreDoesNotLeakCredentialInErrors(t *testing.T) {
	s := NewKeychainStore().(*keychainStore)
	s.platform = "darwin"
	secret := "do-not-print-this-token"
	s.nativeSave = func(string, string, []byte) error { return errors.New(secret) }
	err := s.Save("sandbox", Token{AccessToken: secret})
	if err == nil {
		t.Fatal("expected command failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
}

func TestKeychainSaveNeverPassesTokenAsArgument(t *testing.T) {
	s := NewKeychainStore().(*keychainStore)
	s.platform = "darwin"
	s.nativeSave = func(service, account string, input []byte) error {
		if service != "passo-cli/sandbox" || account != "oauth" || !strings.Contains(string(input), "private-access") || !strings.Contains(string(input), "private-refresh") {
			t.Fatal("invalid private native payload")
		}
		return nil
	}
	if err := s.Save("sandbox", Token{AccessToken: "private-access", RefreshToken: "private-refresh"}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFailuresKeepSafeCredentialStoreIdentity(t *testing.T) {
	s := NewKeychainStore().(*keychainStore)
	s.platform = "darwin"
	secret := errors.New("private-native-secret")
	s.nativeLoad = func(string, string) ([]byte, error) { return nil, secret }
	s.nativeSave = func(string, string, []byte) error { return secret }
	s.nativeDelete = func(string, string) error { return secret }
	_, load := s.Load("staging")
	for _, err := range []error{load, s.Save("staging", Token{AccessToken: "private-access"}), s.Delete("staging")} {
		if !errors.Is(err, ErrCredentialStoreUnavailable) || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe store error: %v", err)
		}
	}
}

func TestNoninteractiveAccessRequiredPreservesStoredRecord(t *testing.T) {
	s := NewKeychainStore().(*keychainStore)
	s.platform = "darwin"
	reads := 0
	s.nativeLoad = func(service, account string) ([]byte, error) {
		reads++
		if service != "passo-cli/staging" || account != "oauth" {
			t.Fatal("existing record scope changed")
		}
		return nil, ErrCredentialAccessRequired
	}
	s.nativeSave = func(string, string, []byte) error { t.Fatal("denied load rewrote credential"); return nil }
	s.nativeDelete = func(string, string) error { t.Fatal("denied load deleted credential"); return nil }
	if _, err := s.Load("staging"); !errors.Is(err, ErrCredentialAccessRequired) || reads != 1 {
		t.Fatal("OS interaction refusal lost")
	}
}

func TestSavePreservesCredentialAccessRequired(t *testing.T) {
	s := &keychainStore{platform: "darwin", nativeSave: func(string, string, []byte) error { return ErrCredentialAccessRequired }}
	if err := s.Save("staging", Token{AccessToken: "owned-dummy"}); !errors.Is(err, ErrCredentialAccessRequired) {
		t.Fatalf("error=%v", err)
	}
}
