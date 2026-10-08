package passocli

import (
	"bytes"
	"context"
	"errors"
	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"strings"
	"sync"
	"testing"
	"time"
)

type countedTokenStore struct {
	loads, saves int
	token        passoauth.Token
	err          error
}

func (s *countedTokenStore) Load(string) (passoauth.Token, error) { s.loads++; return s.token, s.err }
func (s *countedTokenStore) Save(_ string, t passoauth.Token) error {
	s.saves++
	s.token = t
	return s.err
}
func (s *countedTokenStore) Delete(string) error { return nil }

func TestTokenCacheLoadsOnceAndSerializesRefresh(t *testing.T) {
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	now := time.Now()
	store := &countedTokenStore{token: passoauth.Token{AccessToken: "dummy-old", RefreshToken: "dummy-refresh", ExpiresAt: now.Add(time.Hour)}}
	refreshes := 0
	access := cachedTokenAccess(store, "staging", func() time.Time { return now }, func(context.Context, string) (passoauth.Token, error) {
		refreshes++
		return passoauth.Token{AccessToken: "dummy-new", RefreshToken: "dummy-next", ExpiresAt: now.Add(time.Hour)}, nil
	})
	for _, expected := range []string{"dummy-old", "dummy-new"} {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := access(context.Background())
				if err != nil || got != expected {
					t.Errorf("unexpected cached credential result")
				}
			}()
		}
		wg.Wait()
		now = now.Add(2 * time.Hour)
	}
	if store.loads != 1 || store.saves != 1 || refreshes != 1 {
		t.Fatalf("loads=%d saves=%d refreshes=%d", store.loads, store.saves, refreshes)
	}
}
func TestTokenCacheDoesNotRepeatedlyPromptAfterDeniedLoad(t *testing.T) {
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	store := &countedTokenStore{err: errors.New("dummy denied")}
	access := cachedTokenAccess(store, "staging", time.Now, func(context.Context, string) (passoauth.Token, error) {
		t.Fatal("unexpected refresh")
		return passoauth.Token{}, nil
	})
	for i := 0; i < 3; i++ {
		if _, err := access(context.Background()); err == nil {
			t.Fatal("denied credential accepted")
		}
	}
	if store.loads != 1 {
		t.Fatalf("loads=%d, want one denied access per command", store.loads)
	}
}

func TestTokenCacheCancellationAndEnvironmentOverrideAvoidStore(t *testing.T) {
	store := &countedTokenStore{}
	access := cachedTokenAccess(store, "staging", time.Now, func(context.Context, string) (passoauth.Token, error) {
		t.Fatal("unexpected refresh")
		return passoauth.Token{}, nil
	})
	t.Setenv("PASSO_ACCESS_TOKEN", "dummy-child-token")
	if got, err := access(context.Background()); err != nil || got != "dummy-child-token" {
		t.Fatal("child environment override failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := access(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled access accepted")
	}
	if store.loads != 0 || store.saves != 0 {
		t.Fatal("override or cancellation accessed credential store")
	}
}

func TestLoginHelpExplainsExactApplicationKeychainAccess(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Out: &out, Err: &out, Store: &countedTokenStore{}})
	cmd.SetArgs([]string{"login", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"one unchanged official passo", "never grant access to all applications", "caches credentials in memory"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing safe auth guidance: %s", want)
		}
	}
}

// This fixture never accesses the native credential store.
type blockedTokenStore struct {
	started  chan struct{}
	release  chan struct{}
	returned chan struct{}
}

func (s *blockedTokenStore) Load(string) (passoauth.Token, error) {
	close(s.started)
	<-s.release
	close(s.returned)
	return passoauth.Token{AccessToken: "late-dummy", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*blockedTokenStore) Save(string, passoauth.Token) error { panic("unexpected save") }
func (*blockedTokenStore) Delete(string) error                { panic("unexpected delete") }
func TestTokenCacheHonorsDeadlineDuringCredentialRead(t *testing.T) {
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	store := &blockedTokenStore{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	access := cachedTokenAccess(store, "staging", time.Now, func(context.Context, string) (passoauth.Token, error) {
		t.Error("unexpected refresh")
		return passoauth.Token{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := access(ctx); done <- err }()
	<-store.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		close(store.release)
		t.Fatal("credential read ignored command cancellation")
	}
	close(store.release)
	<-store.returned
	if token, err := access(context.Background()); token != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("late credential was accepted: error=%v", err)
	}
}
