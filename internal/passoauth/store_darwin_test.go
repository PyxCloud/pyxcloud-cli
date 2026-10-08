//go:build darwin

package passoauth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOwnedKeychainPromptRoundTrip(t *testing.T) {
	if os.Getenv("PASSO_KEYCHAIN_ROUNDTRIP") != "1" {
		t.Skip("opt-in owned dummy keychain entry")
	}
	s := NewKeychainStore().(*keychainStore)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	service := fmt.Sprintf("passo-cli/owned-fixture-%d", time.Now().UnixNano())
	fixture := `{"access_token":"owned-dummy","refresh_token":"dummy-quote-\"-newline-\n"}`
	defer s.runner(ctx, "security", "delete-generic-password", "-a", "fixture", "-s", service)
	if _, e := s.inputRunner(ctx, []byte(fixture+"\n"+fixture+"\n"), "security", "add-generic-password", "-a", "fixture", "-s", service, "-w"); e != nil {
		t.Fatal("owned fixture save failed")
	}
	got, e := s.runner(ctx, "security", "find-generic-password", "-a", "fixture", "-s", service, "-w")
	if e != nil || strings.TrimSpace(string(got)) != fixture {
		t.Fatal("owned fixture roundtrip mismatch")
	}
}
