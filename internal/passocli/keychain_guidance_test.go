package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
)

type unavailableCredentialStore struct{ loads, deletes int }

func (s *unavailableCredentialStore) Load(string) (passoauth.Token, error) {
	s.loads++
	return passoauth.Token{}, fmt.Errorf("private-secret: %w", passoauth.ErrCredentialStoreUnavailable)
}
func (s *unavailableCredentialStore) Save(string, passoauth.Token) error {
	return passoauth.ErrCredentialStoreUnavailable
}
func (s *unavailableCredentialStore) Delete(string) error {
	s.deletes++
	return passoauth.ErrCredentialStoreUnavailable
}

func TestCommandExplainsCredentialStoreFailureWithoutRetryOrLeak(t *testing.T) {
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer api.Close()
	t.Setenv("PASSO_API_URL", api.URL)
	t.Setenv("PASSO_ISSUER_URL", api.URL)
	t.Setenv("PASSO_CONSOLE_URL", api.URL)
	for _, asJSON := range []bool{false, true} {
		store := &unavailableCredentialStore{}
		var out, stderr bytes.Buffer
		args := []string{"--profile", "staging", "--project", "114", "--ledger", t.TempDir() + "/ledger.json", "status"}
		if asJSON {
			args = append(args, "--json")
		}
		code := execute(context.Background(), args, &out, &stderr, Options{Store: store})
		if code != 10 || store.loads != 1 || calls != 0 {
			t.Fatalf("code=%d loads=%d requests=%d", code, store.loads, calls)
		}
		combined := out.String() + stderr.String()
		if strings.Contains(combined, "private-secret") {
			t.Fatal("credential error leaked")
		}
		if asJSON {
			var r Result
			if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Code != "credential_store_unavailable" || r.NextAction == nil {
				t.Fatalf("missing guidance: %s", out.String())
			}

			action, ok := r.NextAction.(map[string]any)
			if !ok || action["key"] != "resolve_credential_store" || action["automaticRetry"] != false {
				t.Fatalf("unsafe recovery action: %#v", r.NextAction)
			}
		} else if !strings.Contains(stderr.String(), "same official passo executable") || !strings.Contains(stderr.String(), "Stop automatic retries") {
			t.Fatalf("missing actionable guidance: %s", stderr.String())
		}
	}
}

func TestLogoutCredentialStoreFailureOffersOneHumanRecovery(t *testing.T) {
	store := &unavailableCredentialStore{}
	var out, stderr bytes.Buffer
	code := execute(context.Background(), []string{"--json", "logout", "--ledger", t.TempDir() + "/ledger.json"}, &out, &stderr, Options{Store: store})
	var r Result
	err := json.Unmarshal(out.Bytes(), &r)
	if code != 10 || store.loads != 0 || store.deletes != 1 || err != nil || r.Code != "credential_store_unavailable" {
		t.Fatalf("code=%d loads=%d deletes=%d result=%s", code, store.loads, store.deletes, out.String())
	}
}
