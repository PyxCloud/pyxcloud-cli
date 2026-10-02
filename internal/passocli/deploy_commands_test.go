package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
	"github.com/spf13/cobra"
)

func deployTestRuntime(t *testing.T, server *httptest.Server, out *bytes.Buffer) *Runtime {
	t.Helper()
	dir := t.TempDir()
	profile := passoauth.Profile{Name: "sandbox", APIURL: server.URL, ConsoleURL: "https://console.example/base"}
	return &Runtime{
		Profile:    profile,
		Client:     &passotransport.Client{BaseURL: server.URL, HTTPClient: server.Client(), AccessToken: func(context.Context) (string, error) { return "test", nil }},
		LedgerPath: filepath.Join(dir, "ledger.json"), EvidenceDir: filepath.Join(dir, "evidence"),
		ProjectID: 41, ReleaseID: "release /one", RunID: "run-7", Environment: "production", JSON: true,
		Out: out, Err: &bytes.Buffer{}, PollInterval: time.Millisecond, timeout: 100 * time.Millisecond,
		Ledger: passostate.Ledger{}, now: time.Now,
	}
}

func executeDeploy(t *testing.T, runtime *Runtime, args ...string) error {
	t.Helper()
	root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(runtime.Out)
	root.AddCommand(newDeployCommands(func(*cobra.Command) (*Runtime, error) { return runtime, nil })...)
	root.SetArgs(args)
	return root.ExecuteContext(context.Background())
}

func writeDeployInput(t *testing.T, raw string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSealAuthorizeReadsPreconditionsAndNeverPosts(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method != http.MethodGet {
			t.Errorf("unexpected request method %s", r.Method)
		}
		if r.URL.Path != "/vibe/projects/41/releases/release /one/deploy/preconditions" && !strings.Contains(r.URL.EscapedPath(), "release%20%2Fone") {
			t.Errorf("path=%q escaped=%q", r.URL.Path, r.URL.EscapedPath())
		}
		if r.URL.Query().Get("environment") != "production" {
			t.Errorf("query=%v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"data":{"passed":false,"blockers":["approval"]}}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	r := deployTestRuntime(t, srv, &out)
	err := executeDeploy(t, r, "seal", "authorize")
	if ExitCode(err) != 10 || err.Error() != "browser_authorization_required" {
		t.Fatalf("err=%v", err)
	}
	if strings.Join(methods, ",") != "GET" {
		t.Fatalf("methods=%v", methods)
	}
	var got Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "human_required" || got.Code != "browser_authorization_required" {
		t.Fatalf("result=%+v", got)
	}
	action, ok := got.NextAction.(map[string]any)
	if !ok || action["key"] != "authorize_deploy" || action["href"] != "https://console.example/base/projects/41/deploy?release=release+%2Fone" {
		t.Fatalf("nextAction=%v", got.NextAction)
	}
}

func TestDestroyRequiresLiteralConfirmationBeforePOST(t *testing.T) {
	var posts int
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.Method != http.MethodPost || r.URL.Path != "/projects/41/contract/deployments/run-7/terminate" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&posted)
		_, _ = w.Write([]byte(`{"data":{"runId":"run-7","state":"queued"}}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	r := deployTestRuntime(t, srv, &out)
	bad := writeDeployInput(t, `{"expectedVersion":4,"confirmation":"terminate"}`)
	if err := executeDeploy(t, r, "deploy", "destroy", "--input", bad); err == nil || err.Error() != "termination_confirmation_required" {
		t.Fatalf("err=%v", err)
	}
	if posts != 0 {
		t.Fatalf("POSTs before confirmation=%d", posts)
	}
	good := writeDeployInput(t, `{"expectedVersion":4,"confirmation":"TERMINATE"}`)
	if err := executeDeploy(t, r, "deploy", "destroy", "--input", good); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || posted["confirmation"] != "TERMINATE" || posted["idempotencyKey"] == nil {
		t.Fatalf("posts=%d body=%v", posts, posted)
	}
}

func TestCreateWaitObservesTerminalAndNeverFakesCompleted(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/projects/41/contract/deployments" {
				t.Errorf("POST path=%s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"data":{"runId":"run-9","state":"queued"}}`))
		case http.MethodGet:
			if r.URL.Path != "/projects/41/contract/deployments/run-9" {
				t.Errorf("GET path=%s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"data":{"runId":"run-9","state":"succeeded"}}`))
		default:
			t.Errorf("method=%s", r.Method)
		}
	}))
	defer srv.Close()
	var out bytes.Buffer
	r := deployTestRuntime(t, srv, &out)
	in := writeDeployInput(t, `{"releaseId":"rel","environmentId":"env","expectedVersion":3}`)
	if err := executeDeploy(t, r, "deploy", "create", "--input", in, "--wait"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("expected one emitted result: %s", out.String())
	}
	if strings.Contains(out.String(), `"status":"completed"`) || !strings.Contains(out.String(), `"status":"observed"`) {
		t.Fatalf("status was synthesized: %s", out.String())
	}
}

func TestCreateWaitDeadlineAndBackendErrorAreTyped(t *testing.T) {
	t.Run("deadline while polling", func(t *testing.T) {
		pollStarted := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"data":{"runId":"run-9","state":"queued"}}`))
				return
			}
			close(pollStarted)
			<-r.Context().Done()
		}))
		defer srv.Close()
		var out bytes.Buffer
		r := deployTestRuntime(t, srv, &out)
		r.timeout = 100 * time.Millisecond
		r.PollInterval = time.Millisecond
		in := writeDeployInput(t, `{"releaseId":"rel","environmentId":"env"}`)
		done := make(chan error, 1)
		go func() { done <- executeDeploy(t, r, "deploy", "create", "--input", in, "--wait") }()
		select {
		case <-pollStarted:
		case <-time.After(time.Second):
			t.Fatal("poll request did not start")
		}
		err := <-done
		if err == nil || ExitCode(err) != 20 || err.Error() != "deadline_exceeded" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("creation error is preserved", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("unexpected polling request %s", r.Method)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"deployment_unavailable"}}`))
		}))
		defer srv.Close()
		var out bytes.Buffer
		r := deployTestRuntime(t, srv, &out)
		in := writeDeployInput(t, `{"releaseId":"rel","environmentId":"env"}`)
		err := executeDeploy(t, r, "deploy", "create", "--input", in, "--wait")
		if err == nil || ExitCode(err) != 30 || err.Error() != "http_error" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("backend conflict", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"deployment_unavailable"}}`))
		}))
		defer srv.Close()
		var out bytes.Buffer
		r := deployTestRuntime(t, srv, &out)
		r.RunID = "r"
		_, err := r.Perform(context.Background(), "deploy", "journeycontract:managedDeploymentRead", map[string]string{"projectId": "41", "runId": "r"}, nil, nil, false)
		if err == nil || ExitCode(err) != 30 {
			t.Fatalf("expected typed backend conflict, got %v", err)
		}
	})
}
