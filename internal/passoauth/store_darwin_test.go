//go:build darwin && cgo

package passoauth

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOwnedKeychainLongNativeRoundTrip(t *testing.T) {
	if os.Getenv("PASSO_KEYCHAIN_ROUNDTRIP") != "1" {
		t.Skip("opt-in owned dummy keychain entry")
	}
	service := fmt.Sprintf("passo-cli/owned-fixture-%d", time.Now().UnixNano())
	fixture := `{"access_token":"` + strings.Repeat("owned-dummy", 1000) + `","refresh_token":"dummy-quote-\"-newline-\n"}`
	defer nativeKeychainDelete(service, "fixture")
	if e := nativeKeychainSave(service, "fixture", []byte(fixture)); e != nil {
		t.Fatal("owned native fixture save failed")
	}

	got, e := nativeKeychainLoad(service, "fixture")
	if e != nil || strings.TrimSpace(string(got)) != fixture {
		t.Fatal("owned fixture roundtrip mismatch")
	}
	defer nativeKeychainDelete(service, "other-account")
	if err := nativeKeychainSave(service, "other-account", []byte("owned-neighbor")); err != nil {
		t.Fatal("owned neighbor create failed")
	}
	updated := fixture + strings.Repeat(" update", 1000)
	if nativeKeychainSave(service, "fixture", []byte(updated)) != nil {
		t.Fatal("owned update failed")
	}
	got, e = nativeKeychainLoad(service, "fixture")
	if e != nil || string(got) != updated {
		t.Fatal("owned update roundtrip mismatch")
	}
	neighbor, err := nativeKeychainLoad(service, "other-account")
	if err != nil || string(neighbor) != "owned-neighbor" {
		t.Fatal("owned update changed neighboring account")
	}
}
