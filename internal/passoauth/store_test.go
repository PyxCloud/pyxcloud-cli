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
