package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
)

func TestDoctorJSONIsLocalAndReportsOptionalSetup(t *testing.T) {
	t.Setenv("PASSO_API_URL", "https://api.example.test/v1")
	t.Setenv("PASSO_ISSUER_URL", "https://login.example.test/issuer")
	t.Setenv("PASSO_CONSOLE_URL", "https://console.example.test")
	home := t.TempDir()
	t.Setenv("HOME", home)
	ledger := filepath.Join(t.TempDir(), "missing.json")
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: doctorTripwireStore{}, HTTPClient: &http.Client{Transport: doctorTripwireTransport{}}})
	cmd.SetArgs([]string{"--ledger", ledger, "doctor", "--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("doctor: %v stderr=%s", err, errOut.String())
	}
	var got doctorReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode report: %v: %s", err, out.String())
	}
	if got.SchemaVersion != 1 || got.Status != "warning" || got.Profile.APIURL != "https://api.example.test/v1" || got.Profile.IssuerURL != "https://login.example.test/issuer" {
		t.Fatalf("unexpected report: %#v", got)
	}
	if got.Checks.Profile.Status != "ok" || got.Checks.Ledger.Status != "warning" || got.Checks.Skill.Status != "absent" || got.Checks.Executable.Status != "ok" {
		t.Fatalf("unexpected check statuses: %#v", got.Checks)
	}
	if _, err := os.Stat(ledger); !os.IsNotExist(err) {
		t.Fatalf("doctor created or changed missing ledger: %v", err)
	}
	if strings.Contains(out.String(), "clientID") || strings.Contains(out.String(), "token") || strings.Contains(out.String(), home) || strings.Contains(out.String(), ledger) {
		t.Fatalf("report exposed sensitive data: %s", out.String())
	}
}

func TestDoctorJSONFailsClosedForUnsafeProfileAndLedgerScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		api     string
		ledger  string
		project string
		want    string
	}{
		{name: "unsafe profile", api: "https://user:secret@example.test", want: "invalid_profile"},
		{name: "bad ledger", api: "https://api.example.test", ledger: "not-json", want: "invalid_ledger"},
		{name: "ledger scope", api: "https://api.example.test", ledger: `{"schemaVersion":1,"profile":"staging","projectId":42}`, project: "--project=7", want: "scope_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PASSO_API_URL", tc.api)
			t.Setenv("PASSO_ISSUER_URL", "https://login.example.test")
			t.Setenv("PASSO_CONSOLE_URL", "https://console.example.test")
			path := filepath.Join(t.TempDir(), "ledger.json")
			if tc.ledger != "" {
				if err := os.WriteFile(path, []byte(tc.ledger), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--ledger", path}
			if tc.project != "" {
				args = append(args, tc.project)
			}
			args = append(args, "doctor", "--json")
			var out bytes.Buffer
			cmd := New(Options{Out: &out, Err: &bytes.Buffer{}, Store: doctorTripwireStore{}, HTTPClient: &http.Client{Transport: doctorTripwireTransport{}}})
			cmd.SetArgs(args)
			err := cmd.ExecuteContext(context.Background())
			if err == nil || ExitCode(err) != 20 {
				t.Fatalf("expected exit 20, got %v; %s", err, out.String())
			}
			var got doctorReport
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode report: %v: %s", err, out.String())
			}
			if got.Status != "error" || got.Error != tc.want {
				t.Fatalf("unexpected report: %#v", got)
			}
			if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), path) {
				t.Fatalf("report leaked sensitive detail: %s", out.String())
			}
		})
	}
}

func TestDoctorRejectsNegativeProjectWithMissingOrPresentLedger(t *testing.T) {
	t.Setenv("PASSO_API_URL", "https://api.example.test")
	t.Setenv("PASSO_ISSUER_URL", "https://login.example.test")
	t.Setenv("PASSO_CONSOLE_URL", "https://console.example.test")
	for _, tc := range []struct {
		name   string
		ledger string
	}{
		{name: "missing ledger"},
		{name: "present ledger", ledger: `{"schemaVersion":1,"profile":"sandbox","projectId":42}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			if tc.ledger != "" {
				if err := os.WriteFile(path, []byte(tc.ledger), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			cmd := New(Options{Out: &out, Err: &bytes.Buffer{}, Store: doctorTripwireStore{}, HTTPClient: &http.Client{Transport: doctorTripwireTransport{}}})
			cmd.SetArgs([]string{"--project=-1", "--ledger", path, "doctor", "--json"})
			err := cmd.ExecuteContext(context.Background())
			if err == nil || ExitCode(err) != 20 {
				t.Fatalf("negative project should fail with exit 20: %v output=%s", err, out.String())
			}
			var got doctorReport
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode report: %v output=%s", err, out.String())
			}
			if got.Status != "error" || got.Error != "scope_mismatch" || got.Checks.Ledger.Status != "error" {
				t.Fatalf("unexpected report: %#v", got)
			}
		})
	}
}

func TestDoctorReportsManagedSkillAndCatalogListsCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PASSO_API_URL", "https://api.example.test")
	t.Setenv("PASSO_ISSUER_URL", "https://login.example.test")
	t.Setenv("PASSO_CONSOLE_URL", "https://console.example.test")
	home := os.Getenv("HOME")
	target, _, err := planPassoSkill(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, passoSkillMarker), []byte(passoSkillOwner), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("managed"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := New(Options{Out: &out, Err: &bytes.Buffer{}, Store: doctorTripwireStore{}})
	cmd.SetArgs([]string{"doctor", "--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got doctorReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Checks.Skill.Status != "installed" {
		t.Fatalf("skill status=%#v", got.Checks.Skill)
	}
	var catalog bytes.Buffer
	catalogCmd := New(Options{Out: &catalog, Err: &bytes.Buffer{}, Store: doctorTripwireStore{}})
	catalogCmd.SetArgs([]string{"commands", "--json"})
	if err := catalogCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(catalog.String(), `"path":"doctor"`) {
		t.Fatalf("catalog omitted doctor: %s", catalog.String())
	}
}

func TestDoctorReportsUnmanagedSkillWithoutFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PASSO_API_URL", "https://api.example.test")
	t.Setenv("PASSO_ISSUER_URL", "https://login.example.test")
	t.Setenv("PASSO_CONSOLE_URL", "https://console.example.test")
	target := filepath.Join(home, ".agents", "skills", "passo")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("user-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := New(Options{Out: &out, Err: &bytes.Buffer{}, Store: doctorTripwireStore{}})
	cmd.SetArgs([]string{"doctor", "--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unmanaged optional skill should warn only: %v", err)
	}
	var got doctorReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "warning" || got.Checks.Skill.Status != "unmanaged" {
		t.Fatalf("unexpected report: %#v", got)
	}
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil || string(data) != "user-owned" {
		t.Fatalf("doctor changed unmanaged skill: %q, %v", data, err)
	}
}

type doctorTripwireStore struct{}

func (doctorTripwireStore) Load(string) (passoauth.Token, error) {
	panic("doctor accessed credentials")
}
func (doctorTripwireStore) Save(string, passoauth.Token) error { panic("doctor wrote credentials") }
func (doctorTripwireStore) Delete(string) error                { panic("doctor deleted credentials") }

type doctorTripwireTransport struct{}

func (doctorTripwireTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("doctor used network")
}
