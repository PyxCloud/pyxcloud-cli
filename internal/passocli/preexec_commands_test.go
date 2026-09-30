package passocli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
	"github.com/spf13/cobra"
)

func preexecRoot(t *testing.T, srv *httptest.Server, project int64) *cobra.Command {
	t.Helper()
	profile, _ := passoauth.ResolveProfile("sandbox")
	out := &strings.Builder{}
	root := &cobra.Command{Use: "passo", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(out)
	root.PersistentFlags().Int64("project", project, "project")
	factory := func(*cobra.Command) (*Runtime, error) {
		return &Runtime{Profile: profile, Client: passotransport.New(srv.URL, nil), LedgerPath: t.TempDir() + "/ledger.json", EvidenceDir: t.TempDir() + "/evidence", ProjectID: project, Out: out, Err: io.Discard, timeout: time.Second, now: time.Now}, nil
	}
	root.AddCommand(newPreexecCommands(factory)...)
	return root
}

func TestProjectsGetUsesMountedDetailRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/vibe/projects/42" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":42,"name":"demo"}`)
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"projects", "get"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConnectAttachAlreadyAttachedIsIdempotent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/projects/42/state" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"projectId":42,"state":"BUILD.DEVELOP","data":{"repos":["owner/repo"]},"version":7}`)
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"connect", "attach", "--repo", "owner/repo"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d; existing attachment must not transition", calls)
	}
}

func TestConnectAttachAdvancesOnlyWithCanonicalSnapshots(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		switch step {
		case 1, 3:
			if r.Method != http.MethodGet || r.URL.Path != "/projects/42/state" {
				t.Errorf("snapshot request %s %s", r.Method, r.URL.Path)
			}
			state, version := "CONNECT.NEW", 3
			if step == 3 {
				state, version = "CONNECT.ADD_REPO", 4
			}
			_, _ = io.WriteString(w, `{"projectId":42,"state":"`+state+`","data":{},"version":`+strconv.Itoa(version)+`}`)
		case 2, 4:
			if r.Method != http.MethodPost || r.URL.Path != "/projects/42/transitions" {
				t.Errorf("transition request %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if step == 2 && (body["event"] != "repo.connect.requested" || body["version"] != float64(3)) {
				t.Errorf("first body %#v", body)
			}
			if step == 4 {
				if body["event"] != "repo.connected" || body["version"] != float64(4) {
					t.Errorf("second body %#v", body)
				}
				payload, _ := body["payload"].(map[string]any)
				if got, _ := payload["repos"].([]any); len(got) != 1 || got[0] != "owner/repo" {
					t.Errorf("payload %#v", body["payload"])
				}
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"projectId":42,"state":"CONNECT.ADD_REPO","version":4}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"connect", "attach", "--repo", "owner/repo"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if step != 4 {
		t.Fatalf("requests=%d", step)
	}
}

func TestScopeDerivationReportsMissingCompilation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/vibe/projects/42/scope/derivation" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"no_compiled_documentation"}`)
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"define", "derive"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected refusal for absent compilation")
	}
}

func TestProjectMutationRequiresBoundedInput(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	root := preexecRoot(t, srv, 0)
	root.SetArgs([]string{"projects", "create"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected required input error")
	}
	if calls != 0 {
		t.Fatalf("invalid input reached API: %d calls", calls)
	}
}
