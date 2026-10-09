package passocli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
)

func TestOrganizationSelectionAndLedgerBinding(t *testing.T) {
	const org = "f64852f4-96c5-4874-8d8f-79da946a839b"
	for _, tc := range []struct{ name, flag, bound, want, code string }{
		{name: "explicit", flag: org, want: org},
		{name: "default"},
		{name: "resume", bound: org, want: org},
		{name: "mismatch", flag: "00000000-0000-0000-0000-000000000001", bound: org, code: "scope_mismatch"},
		{name: "invalid", flag: "bad\r\nheader", code: "invalid_organization"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if got := r.Header.Get("X-Organization-Id"); got != tc.want {
					t.Errorf("organization=%q want=%q", got, tc.want)
				}
				io.WriteString(w, `{"data":{"projectId":114,"stage":"gate","primaryAction":{"key":"open_board","label":"Review"}}}`)
			}))
			defer srv.Close()
			t.Setenv("PASSO_API_URL", srv.URL)
			t.Setenv("PASSO_ISSUER_URL", srv.URL)
			t.Setenv("PASSO_CONSOLE_URL", srv.URL)
			ledger := filepath.Join(t.TempDir(), "run.json")
			if tc.bound != "" {
				if err := passostate.Save(ledger, passostate.Ledger{SchemaVersion: 1, OrganizationID: tc.bound}); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			cmd := New(Options{Out: &out, Err: io.Discard, Store: &memoryStore{token: passoauth.Token{AccessToken: "fixture", ExpiresAt: time.Now().Add(time.Hour)}}})
			args := []string{"--ledger", ledger, "--project", "114", "--json", "status"}
			if tc.flag != "" {
				args = append(args, "--organization", tc.flag)
			}
			cmd.SetArgs(args)
			err := cmd.ExecuteContext(context.Background())
			if tc.code != "" {
				if err == nil || err.Error() != tc.code {
					t.Fatalf("error=%v want=%s", err, tc.code)
				}
				if calls != 0 {
					t.Fatal("invalid scope reached API")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
