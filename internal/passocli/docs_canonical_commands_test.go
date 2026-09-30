package passocli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
	"github.com/spf13/cobra"
)

func canonicalDocsTestRoot(t *testing.T, server *httptest.Server, project int64, version string) *cobra.Command {
	t.Helper()
	profile, _ := passoauth.ResolveProfile("sandbox")
	root := &cobra.Command{Use: "passo", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().Int64("project", project, "project")
	root.PersistentFlags().String("version", version, "version")
	factory := func(*cobra.Command) (*Runtime, error) {
		return &Runtime{Profile: profile, Client: passotransport.New(server.URL, nil), LedgerPath: filepath.Join(t.TempDir(), "ledger.json"), EvidenceDir: filepath.Join(t.TempDir(), "evidence"), ProjectID: project, VersionID: version, Out: io.Discard, Err: io.Discard, timeout: time.Second, now: time.Now}, nil
	}
	root.AddCommand(newPreexecCommands(factory)...)
	return root
}

func TestCanonicalDocsReadCommandsUseMountedRoutesAndPagination(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		method, path string
		query        url.Values
	}{
		{[]string{"--project", "42", "docs", "workspace"}, http.MethodGet, "/documentation/v1/projects/42/documentation/workspace", nil},
		{[]string{"--project", "42", "docs", "revisions", "--limit", "20", "--offset", "3"}, http.MethodGet, "/documentation/v1/projects/42/documentation/revisions", url.Values{"limit": {"20"}, "offset": {"3"}}},
	} {
		t.Run(strings.Join(tc.args[2:], "_"), func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Method != tc.method || r.URL.Path != tc.path || r.URL.Query().Encode() != tc.query.Encode() {
					t.Errorf("request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
				}
				w.WriteHeader(http.StatusOK)
				io.WriteString(w, `{}`)
			}))
			defer server.Close()
			root := canonicalDocsTestRoot(t, server, 42, "")
			root.SetArgs(tc.args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if hits != 1 {
				t.Fatalf("requests=%d", hits)
			}
		})
	}
}

func TestCanonicalDocsPublishUsesInputAndOptionalIfMatchHeader(t *testing.T) {
	for _, withIfMatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "zero"}[withIfMatch], func(t *testing.T) {
			var inputPath string
			tmp := t.TempDir()
			inputPath = filepath.Join(tmp, "input.json")
			if err := os.WriteFile(inputPath, []byte(`{"runId":"run-1","documentationId":"doc-2"}`), 0600); err != nil {
				t.Fatal(err)
			}
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Method != http.MethodPost || r.URL.Path != "/documentation/v1/projects/42/documentation/revisions" {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Idempotency-Key") == "" {
					t.Error("missing Idempotency-Key")
				}
				wantMatch := ""
				if withIfMatch {
					wantMatch = "0"
				}
				if r.Header.Get("If-Match") != wantMatch {
					t.Errorf("If-Match=%q, want %q", r.Header.Get("If-Match"), wantMatch)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["runId"] != "run-1" || body["documentationId"] != "doc-2" || body["idempotencyKey"] != nil {
					t.Errorf("body=%#v", body)
				}
				w.WriteHeader(http.StatusCreated)
				io.WriteString(w, `{}`)
			}))
			defer server.Close()
			root := canonicalDocsTestRoot(t, server, 42, "")
			args := []string{"--project", "42", "docs", "publish", "--input", inputPath}
			if withIfMatch {
				args = append(args, "--if-match", "0")
			}
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if hits != 1 {
				t.Fatalf("requests=%d", hits)
			}
		})
	}
}

func TestCanonicalDocsRevisionDecisionsAndSnapshotLockUseRequiredRawIfMatch(t *testing.T) {
	cases := []struct {
		args []string
		path string
	}{
		{[]string{"docs", "begin-review", "rev-1", "--if-match", "0"}, "/documentation/v1/projects/42/documentation/revisions/rev-1/begin-review"},
		{[]string{"docs", "accept", "rev-1", "--if-match", "0"}, "/documentation/v1/projects/42/documentation/revisions/rev-1/accept"},
		{[]string{"docs", "reject", "rev-1", "--if-match", "0"}, "/documentation/v1/projects/42/documentation/revisions/rev-1/reject"},
		{[]string{"--version", "version-uuid", "docs", "snapshot", "lock", "--if-match", "0"}, "/documentation/v1/projects/42/documentation/snapshots/version-uuid/lock"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tc.path {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("If-Match") != "0" || r.Header.Get("Idempotency-Key") == "" {
					t.Errorf("headers=%v", r.Header)
				}
				b, _ := io.ReadAll(r.Body)
				if len(b) != 0 || r.Header.Get("Content-Type") != "" {
					t.Errorf("body=%s content-type=%q", b, r.Header.Get("Content-Type"))
				}
				w.WriteHeader(http.StatusOK)
				io.WriteString(w, `{}`)
			}))
			defer server.Close()
			root := canonicalDocsTestRoot(t, server, 42, "version-uuid")
			args := append([]string{"--project", "42"}, tc.args...)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCanonicalDocsSnapshotReadUsesGlobalVersionUUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/documentation/v1/projects/42/documentation/snapshots/version-uuid" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{}`)
	}))
	defer server.Close()
	root := canonicalDocsTestRoot(t, server, 42, "version-uuid")
	root.SetArgs([]string{"--project", "42", "--version", "version-uuid", "docs", "snapshot", "read"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalDocsBodylessMutationFlagsAreRequiredAndInputIsRejected(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	for _, args := range [][]string{{"docs", "begin-review", "rev-1"}, {"docs", "snapshot", "lock"}, {"docs", "accept", "rev-1", "--if-match", "0", "--input", "body.json"}, {"docs", "publish"}} {
		root := canonicalDocsTestRoot(t, server, 42, "version-uuid")
		root.SetArgs(append([]string{"--project", "42"}, args...))
		if err := root.ExecuteContext(context.Background()); err == nil {
			t.Errorf("args %v unexpectedly succeeded", args)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid command flags reached network %d times", calls)
	}
}

func TestDocumentationOperationsAreAllowedAtDocsStage(t *testing.T) {
	if got := operationStage("documentation:documentationWorkspaceRead"); got != "docs" {
		t.Fatalf("stage=%q", got)
	}
	plan := runPlan{SchemaVersion: 1, Steps: []runStep{{Stage: "docs", Operation: "documentation:documentationWorkspaceRead", Params: map[string]string{"projectId": "${projectId}"}, Query: map[string][]string{}}}}
	if err := validateRunPlan(plan); err != nil {
		t.Fatalf("docs operation rejected by run allowlist: %v", err)
	}
}

func TestCanonicalDocsRequiresProjectAndSnapshotVersionBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	for _, tc := range []struct {
		project             int64
		version, args, want string
	}{
		{0, "", "docs workspace", "project_required"},
		{42, "", "docs snapshot read", "version_required"},
	} {
		root := canonicalDocsTestRoot(t, server, tc.project, tc.version)
		root.SetArgs(append([]string{"--project", strconv.FormatInt(tc.project, 10)}, strings.Fields(tc.args)...))
		err := root.ExecuteContext(context.Background())
		if err == nil || err.Error() != tc.want {
			t.Errorf("args=%q error=%v, want %s", tc.args, err, tc.want)
		}
	}
	if calls != 0 {
		t.Fatalf("missing scope made %d requests", calls)
	}
}
