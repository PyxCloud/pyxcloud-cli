package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
	"github.com/spf13/cobra"
)

func releaseTestRoot(runtime *Runtime, input io.Reader, out io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "test", Args: cobra.NoArgs}
	root.SetOut(out)
	root.SetIn(input)
	root.PersistentFlags().Int64("project", runtime.ProjectID, "project")
	root.PersistentFlags().String("version", runtime.VersionID, "version")
	root.AddCommand(newReleaseCommands(func(*cobra.Command) (*Runtime, error) { return runtime, nil })...)
	return root
}

func TestReleaseCommandsUseContractRoutesAndPassBodies(t *testing.T) {
	tests := []struct {
		name, args, method, path, query, input string
		status                                 int
	}{
		{"freeze preview", "freeze preview", "GET", "/projects/42/contract/freeze-preview", "", "", 200},
		{"eligibility", "freeze eligibility", "GET", "/projects/42/contract", "", "", 200},
		{"proposed pins", "freeze proposed-pins", "GET", "/projects/42/contract/proposed-pins", "", "", 200},
		{"branches", "freeze branches", "GET", "/projects/42/contract/release-scope-lock-branches", "version=v-7", "", 200},
		{"freeze create", "freeze create --input", "POST", "/projects/42/contract/release-freeze", "", `{"expectedVersion":3}`, 202},
		{"freeze lock", "freeze lock --input", "POST", "/projects/42/contract/version-lock", "", `{}`, 202},
		{"freeze mirror", "freeze mirror --input", "POST", "/projects/42/contract/spec-revision", "", `{}`, 202},
		{"branches create", "freeze branches create --input", "POST", "/projects/42/contract/release-scope-lock-branches", "", `{"version":"v-7"}`, 202},
		{"scan", "secure scan", "GET", "/vibe/projects/42/versions/v-7/security/scan", "", "", 200},
		{"gate", "secure gate", "GET", "/vibe/projects/42/versions/v-7/security/gate", "", "", 200},
		{"finding", "secure finding f-1", "GET", "/vibe/projects/42/versions/v-7/security/gate/findings/f-1/detail", "", "", 200},
		{"remediation preview", "secure remediation-preview --input", "POST", "/vibe/projects/42/versions/v-7/security/gate/preview", "", `{"evaluationId":"e-1"}`, 202},
		{"remediation confirm", "secure remediation-confirm --input", "POST", "/vibe/projects/42/versions/v-7/security/gate/confirm", "", `{"previewId":"p-1"}`, 202},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var output bytes.Buffer
			args := strings.Fields(tt.args)
			if strings.HasSuffix(tt.args, "--input") {
				path := filepath.Join(dir, "input.json")
				if err := os.WriteFile(path, []byte(tt.input), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, path)
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != tt.method || req.URL.Path != tt.path || req.URL.RawQuery != tt.query {
					t.Errorf("request = %s %s?%s; want %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery, tt.method, tt.path, tt.query)
				}
				if tt.method == http.MethodPost && req.Header.Get("Idempotency-Key") == "" {
					t.Error("request missing stable idempotency header")
				}
				if tt.method == http.MethodPost {
					var got map[string]json.RawMessage
					if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
						t.Errorf("decode body: %v", err)
					}
					var want map[string]json.RawMessage
					_ = json.Unmarshal([]byte(tt.input), &want)
					for key, value := range want {
						if string(got[key]) != string(value) {
							t.Errorf("body[%s]=%s, want %s", key, got[key], value)
						}
					}
					declaresKey := strings.HasPrefix(tt.name, "remediation")
					if declaresKey != (got["idempotencyKey"] != nil) {
						t.Errorf("body idempotencyKey presence=%v, want %v", got["idempotencyKey"] != nil, declaresKey)
					}
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, `{"data":{"projectId":42}}`)
			}))
			defer srv.Close()
			profile, _ := passoauth.ResolveProfile("sandbox")
			profile.APIURL = srv.URL
			client := passotransport.New(srv.URL, func(context.Context) (string, error) { return "test-token", nil })
			client.HTTPClient = srv.Client()
			runtime := &Runtime{Profile: profile, Client: client, ProjectID: 42, VersionID: "v-7", LedgerPath: filepath.Join(dir, "ledger.json"), EvidenceDir: filepath.Join(dir, "evidence"), Out: &output, JSON: true, timeout: time.Second, now: time.Now}
			root := releaseTestRoot(runtime, nil, &output)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if calls != 1 {
				t.Fatalf("request count=%d, want 1", calls)
			}
			if tt.status == http.StatusAccepted {
				var result Result
				if err := json.Unmarshal(output.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Status != "accepted" {
					t.Errorf("mutation status=%q, want accepted", result.Status)
				}
			}
		})
	}
}

func TestReleaseCommandsRejectBadInputAndMissingScopeBeforeRequest(t *testing.T) {
	tests := []struct {
		name, args, want string
		project          int64
		version          string
		input            string
	}{
		{"project", "freeze preview", "project_required", 0, "", ""},
		{"branches version", "freeze branches", "version_required", 42, "", ""},
		{"scan version", "secure scan", "version_required", 42, "", ""},
		{"mutation input", "freeze create", "input_required", 42, "v-7", ""},
		{"malformed input", "freeze create --input", "invalid_input", 42, "v-7", `[]`},
		{"oversized input", "secure remediation-preview --input", "invalid_input", 42, "v-7", `{"x":"` + strings.Repeat("a", maxInputBytes) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			defer srv.Close()
			profile, _ := passoauth.ResolveProfile("sandbox")
			client := passotransport.New(srv.URL, nil)
			runtime := &Runtime{Profile: profile, Client: client, ProjectID: tt.project, VersionID: tt.version, LedgerPath: filepath.Join(t.TempDir(), "ledger.json"), Out: io.Discard}
			root := releaseTestRoot(runtime, nil, io.Discard)
			args := strings.Fields(tt.args)
			if strings.HasSuffix(tt.args, "--input") {
				path := filepath.Join(t.TempDir(), "input.json")
				if err := os.WriteFile(path, []byte(tt.input), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, path)
			}
			root.SetArgs(args)
			err := root.Execute()
			var exit *ExitError
			if err == nil || !errors.As(err, &exit) || exit.Code != tt.want {
				t.Fatalf("error=%v, want ExitError %s", err, tt.want)
			}
			if calls != 0 {
				t.Fatalf("request count=%d, want 0", calls)
			}
		})
	}
}

func TestRemediationConfirmReturnsHumanPolicyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"not_human"}`)
	}))
	defer srv.Close()
	profile, _ := passoauth.ResolveProfile("sandbox")
	client := passotransport.New(srv.URL, func(context.Context) (string, error) { return "test-token", nil })
	client.HTTPClient = srv.Client()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{"previewId":"p-1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{Profile: profile, Client: client, ProjectID: 42, VersionID: "v-7", LedgerPath: filepath.Join(t.TempDir(), "ledger.json"), Out: io.Discard}
	root := releaseTestRoot(runtime, nil, io.Discard)
	root.SetArgs([]string{"secure", "remediation-confirm", "--input", path})
	err := root.Execute()
	var exit *ExitError
	if err == nil || !errors.As(err, &exit) || exit.ExitCode != 10 || exit.Code != "not_human" {
		t.Fatalf("error=%v, want exit 10 not_human", err)
	}
}
