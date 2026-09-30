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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
	"github.com/spf13/cobra"
)

func designTestRoot(t *testing.T, runtime *Runtime, in io.Reader, out io.Writer) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "test", Args: cobra.NoArgs}
	root.SetOut(out)
	root.SetIn(in)
	root.PersistentFlags().Int64("project", runtime.ProjectID, "project")
	root.PersistentFlags().String("version", runtime.VersionID, "version")
	root.AddCommand(newDesignCommands(func(*cobra.Command) (*Runtime, error) { return runtime, nil })...)
	return root
}

func TestDesignCommandsUseGeneratedRoutesAuthAndManagedMutationKeys(t *testing.T) {
	tests := []struct {
		name, args, method, path, input, operation string
		wantBody                                   map[string]any
		managedBodyKey                             bool
		status                                     int
	}{
		{name: "architecture read", args: "design", method: http.MethodGet, path: "/vibe/projects/42/architecture", status: http.StatusOK},
		{name: "generation read", args: "design generation gen-1", method: http.MethodGet, path: "/vibe/projects/42/architecture/generations/gen-1", status: http.StatusOK},
		{name: "proposal read", args: "design proposal prop-1", method: http.MethodGet, path: "/vibe/projects/42/architecture/proposals/prop-1", status: http.StatusOK},
		{name: "generation create", args: "design generate --input", method: http.MethodPost, path: "/vibe/projects/42/architecture/generations", input: `{"optimizationObjective":"BALANCED"}`, operation: "architecture:createArchitectureGeneration", managedBodyKey: true, status: http.StatusAccepted},
		{name: "architecture choose", args: "design choose --input", method: http.MethodPost, path: "/vibe/projects/42/architecture/selections", input: `{"generationId":"gen-1","proposalId":"prop-1"}`, operation: "architecture:createArchitectureSelection", managedBodyKey: true, status: http.StatusAccepted},
		{name: "compare read", args: "compare", method: http.MethodGet, path: "/vibe/projects/42/versions/7/cloud/compare", status: http.StatusOK},
		{name: "evaluation start", args: "compare evaluate --input", method: http.MethodPost, path: "/vibe/projects/42/versions/7/cloud/evaluations", input: `{"deploymentRegionId":"nyc3","objective":"LOWEST_COST"}`, operation: "regioncompare.v2:startCloudEvaluation", wantBody: map[string]any{"deploymentRegionId": "nyc3", "objective": "LOWEST_COST"}, status: http.StatusAccepted},
		{name: "cloud choose", args: "compare choose --input", method: http.MethodPost, path: "/vibe/projects/42/versions/7/cloud/selections", input: `{"deploymentRegionId":"nyc3","evaluationId":"eval-1","candidateId":"candidate-1"}`, operation: "regioncompare:createCloudSelection", wantBody: map[string]any{"deploymentRegionId": "nyc3", "evaluationId": "eval-1", "candidateId": "candidate-1"}, status: http.StatusAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var output bytes.Buffer
			bodyFile := filepath.Join(dir, "input.json")
			args := strings.Fields(tt.args)
			if strings.HasSuffix(tt.args, "--input") {
				if err := os.WriteFile(bodyFile, []byte(tt.input), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, bodyFile)
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != tt.method || req.URL.Path != tt.path {
					t.Errorf("request = %s %s; want %s %s", req.Method, req.URL.Path, tt.method, tt.path)
				}
				if req.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("authorization header = %q", req.Header.Get("Authorization"))
				}
				if tt.method == http.MethodPost {
					var got map[string]json.RawMessage
					if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
						t.Errorf("decode body: %v", err)
					}
					if tt.managedBodyKey && got["idempotencyKey"] == nil {
						t.Error("mutation body has no managed idempotencyKey")
					}
					if tt.wantBody != nil {
						decoded := map[string]any{}
						for key, value := range got {
							var item any
							if err := json.Unmarshal(value, &item); err != nil {
								t.Errorf("decode body field %s: %v", key, err)
							}
							decoded[key] = item
						}
						if !reflect.DeepEqual(decoded, tt.wantBody) {
							t.Errorf("request body = %#v; want exact body %#v", decoded, tt.wantBody)
						}
					}
					if req.Header.Get("Idempotency-Key") == "" {
						t.Error("mutation has no Idempotency-Key header")
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
			runtime := &Runtime{Profile: profile, Client: client, ProjectID: 42, VersionID: "9fce9406-6e8e-4e74-9342-2d3c7ba4207e", VersionSequence: 7, LedgerPath: filepath.Join(dir, "ledger.json"), EvidenceDir: filepath.Join(dir, "evidence"), Out: &output, JSON: true, timeout: time.Second, now: time.Now}
			root := designTestRoot(t, runtime, nil, &output)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if calls != 1 {
				t.Fatalf("request count = %d; want 1", calls)
			}
			if tt.status == http.StatusAccepted {
				var result Result
				if err := json.Unmarshal(output.Bytes(), &result); err != nil {
					t.Fatalf("decode command result: %v", err)
				}
				if result.Status != "accepted" || result.Status == "completed" {
					t.Errorf("mutation result status = %q; want accepted without claiming completion", result.Status)
				}
				var ledger struct {
					Operations map[string]struct {
						State string `json:"state"`
					} `json:"operations"`
				}
				data, err := os.ReadFile(runtime.LedgerPath)
				if err != nil || json.Unmarshal(data, &ledger) != nil {
					t.Fatalf("read mutation ledger: %v", err)
				}
				op, ok := ledger.Operations[tt.operation]
				if !ok {
					t.Errorf("mutation operation %q absent from ledger", tt.operation)
				} else if op.State != "accepted" {
					t.Errorf("%s state = %s; accepted response must not claim completion", tt.operation, op.State)
				}
			}
		})
	}
}

func TestDesignCommandsRejectMissingScopeOrInputBeforeRequest(t *testing.T) {
	tests := []struct {
		name, args, wantCode string
		project              int64
		version              string
	}{
		{name: "project", args: "design", wantCode: "project_required"},
		{name: "version sequence", args: "compare", project: 42, wantCode: "version_sequence_required"},
		{name: "input", args: "design generate", project: 42, wantCode: "input_required"},
		{name: "selection input", args: "compare choose", project: 42, version: "v-7", wantCode: "input_required"},
		{name: "evaluation input", args: "compare evaluate", project: 42, version: "v-7", wantCode: "input_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			defer srv.Close()
			profile, _ := passoauth.ResolveProfile("sandbox")
			client := passotransport.New(srv.URL, nil)
			runtime := &Runtime{Profile: profile, Client: client, ProjectID: tt.project, VersionID: tt.version, LedgerPath: filepath.Join(t.TempDir(), "ledger.json"), Out: io.Discard}
			root := designTestRoot(t, runtime, nil, io.Discard)
			root.SetArgs(strings.Fields(tt.args))
			err := root.Execute()
			var exit *ExitError
			if err == nil || !errors.As(err, &exit) || exit.Code != tt.wantCode {
				t.Fatalf("error = %v; want ExitError %s", err, tt.wantCode)
			}
			if calls != 0 {
				t.Fatalf("request count = %d; want 0", calls)
			}
		})
	}
}
