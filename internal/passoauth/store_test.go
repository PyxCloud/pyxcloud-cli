package passoauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestKeychainStoreSaveLoadDelete(t *testing.T) {
	s := NewKeychainStore().(*keychainStore)
	s.platform = "darwin"
	data := map[string][]byte{}
	s.runner = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		service := ""
		password := ""
		for i := 0; i < len(args); i++ {
			if args[i] == "-s" && i+1 < len(args) {
				service = args[i+1]
			}
			if args[i] == "-w" && i+1 < len(args) {
				password = args[i+1]
			}
		}
		if args[0] == "add-generic-password" {
			data[service] = []byte(password)
			return nil, nil
		}
		if args[0] == "find-generic-password" {
			v, ok := data[service]
			if !ok {
				return nil, errors.New("not found")
			}
			return v, nil
		}
		if args[0] == "delete-generic-password" {
			delete(data, service)
			return nil, nil
		}
		return nil, errors.New("unexpected command")
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
	s.runner = func(context.Context, string, ...string) ([]byte, error) { runs++; return nil, nil }
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
	s.runner = func(context.Context, string, ...string) ([]byte, error) { return []byte(secret), errors.New(secret) }
	err := s.Save("sandbox", Token{AccessToken: secret})
	if err == nil {
		t.Fatal("expected command failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
}
