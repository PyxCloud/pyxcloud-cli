package passocli

import (
	"bytes"
	"context"
	"errors"
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

func TestRunSkipsMutationOnlyWhenBackendCheckIsTrue(t *testing.T) {
	gets, posts := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"status":"ready"}}`))
			return
		}
		posts++
		http.Error(w, "unexpected mutation", http.StatusInternalServerError)
	}))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, time.Second)
	plan := `{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:canonicalProjectStateRead","params":{"projectId":"${projectId}"},"query":{}},{"stage":"connect","operation":"projects:canonicalProjectTransition","params":{"projectId":"${projectId}"},"query":{},"input":{"to":"ready"},"bodyIdempotency":true,"check":{"operation":"projects:canonicalProjectStateRead","params":{"projectId":"${projectId}"},"query":{},"pointer":"/data/status","equals":"ready"}}]}`
	if err := executeRunPlan(t, plan, "connect", r, out); err != nil {
		t.Fatal(err)
	}
	if gets != 2 || posts != 0 {
		t.Fatalf("GETs=%d POSTs=%d; backend-complete mutation must be skipped", gets, posts)
	}
	if strings.Contains(out.String(), "status\":\"ready") || !strings.Contains(out.String(), `"status":"completed"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestRunPollsAfterAcceptedMutationUntilActualState(t *testing.T) {
	gets, posts := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			w.Header().Set("Content-Type", "application/json")
			state := "working"
			if gets >= 3 {
				state = "ready"
			}
			_, _ = w.Write([]byte(`{"data":{"status":"` + state + `"}}`))
			return
		}
		posts++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, time.Second)
	r.PollInterval = time.Millisecond
	plan := `{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:canonicalProjectTransition","params":{"projectId":"${projectId}"},"query":{},"input":{"to":"ready"},"bodyIdempotency":true,"check":{"operation":"projects:canonicalProjectStateRead","params":{"projectId":"${projectId}"},"query":{},"pointer":"/data/status","equals":"ready"}}]}`
	if err := executeRunPlan(t, plan, "connect", r, out); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || gets < 3 {
		t.Fatalf("GETs=%d POSTs=%d; 202 must be polled to observed state", gets, posts)
	}
}

func TestRunPreflightsLaterInvalidStepsBeforeAnyRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{}`)) }))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, time.Second)
	plan := `{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:projectRead","params":{"projectId":"${projectId}"},"query":{}},{"stage":"deploy","operation":"seal:deployAuthorize","params":{},"query":{}}]}`
	err := executeRunPlan(t, plan, "connect", r, out)
	if ExitCode(err) != 20 || calls != 0 {
		t.Fatalf("err=%v calls=%d; full plan must validate before network", err, calls)
	}
}

func TestRunPreflightsCheckOperationStageBeforeAnyRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"ready"}}`))
	}))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, time.Second)
	plan := `{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:canonicalProjectTransition","params":{"projectId":"${projectId}"},"query":{},"input":{"to":"ready"},"bodyIdempotency":true,"check":{"operation":"journeycontract:releaseEligibilityRead","params":{"projectId":"${projectId}"},"query":{},"pointer":"/data/status","equals":"ready"}}]}`
	err := executeRunPlan(t, plan, "connect", r, out)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != "operation_stage_mismatch" || calls != 0 {
		t.Fatalf("err=%v calls=%d; check operations from another stage must be rejected before network", err, calls)
	}
}

func TestRunStopsAtSelectedTarget(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":17}`))
	}))
	defer srv.Close()
	r, _ := testRunRuntime(t, srv.URL, time.Second)
	plan := `{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:projectRead","params":{"projectId":"${projectId}"},"query":{}},{"stage":"deploy","operation":"deployment:listDeployments","params":{"projectId":"${projectId}"},"query":{}}]}`
	if err := executeRunPlan(t, plan, "connect", r, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d; target must cut off later step", calls)
	}
}

func TestRunPointerAndCanonicalNumberComparison(t *testing.T) {
	got, ok := jsonPointer([]byte(`{"a":[{"b":1e0}]}`), "/a/0/b")
	if !ok {
		t.Fatal("pointer did not resolve")
	}
	a, _ := canonicalJSON(got)
	b, _ := canonicalJSON([]byte(`1.0`))
	if !bytes.Equal(a, b) {
		t.Fatalf("canonical numeric values differ: %s != %s", a, b)
	}
	if validPointer("/a/~2") {
		t.Fatal("invalid RFC6901 escape accepted")
	}
}

func testRunRuntime(t *testing.T, api string, timeout time.Duration) (*Runtime, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	out := &bytes.Buffer{}
	p := passoauth.Profile{Name: "sandbox", APIURL: api, ConsoleURL: "https://console.example"}
	client := passotransport.New(api, func(context.Context) (string, error) { return "test-token", nil })
	return &Runtime{Profile: p, Client: client, LedgerPath: filepath.Join(dir, "ledger.json"), EvidenceDir: filepath.Join(dir, "evidence"), ProjectID: 17, JSON: true, Out: out, Err: &bytes.Buffer{}, PollInterval: time.Millisecond, timeout: timeout}, out
}
func executeRunPlan(t *testing.T, plan, target string, r *Runtime, out *bytes.Buffer) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if e := os.WriteFile(path, []byte(plan), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := newRunCommand(func(*cobra.Command) (*Runtime, error) { return r, nil })
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--to", target, "--plan", path})
	return cmd.ExecuteContext(context.Background())
}

func TestRunDoesNotTrustLedgerWhenBackendCheckIsFalse(t *testing.T) {
	gets, posts := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"status":"working"}}`))
			return
		}
		posts++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, 200*time.Millisecond)
	r.Ledger.Operations = map[string]passostate.Operation{"projects:canonicalProjectTransition": {State: "completed"}}
	plan := `{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:canonicalProjectTransition","params":{"projectId":"${projectId}"},"query":{},"input":{"to":"ready"},"bodyIdempotency":true,"check":{"operation":"projects:canonicalProjectStateRead","params":{"projectId":"${projectId}"},"query":{},"pointer":"/data/status","equals":"ready"}}]}`
	err := executeRunPlan(t, plan, "connect", r, out)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != "run_timeout" || posts != 1 || gets < 1 {
		t.Fatalf("err=%v GETs=%d POSTs=%d", err, gets, posts)
	}
}

func TestRunHumanActionsHandoffWithoutPosting(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Errorf("human action POSTed: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ready":true}}`))
	}))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, time.Second)
	plan := `{"schemaVersion":1,"steps":[{"stage":"seal","operation":"seal:deployAuthorize","params":{"projectId":"${projectId}","releaseId":"${releaseId}"},"query":{}}]}`
	err := executeRunPlan(t, plan, "seal", r, out)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.ExitCode != 10 || posts != 0 || !strings.Contains(out.String(), `"status":"human_required"`) || !strings.Contains(out.String(), `/projects/17/deploy?release=`) {
		t.Fatalf("err=%v posts=%d output=%s", err, posts, out.String())
	}
}

func TestRunMalformedPlanMakesNoRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, time.Second)
	for _, plan := range []string{`{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:projectRead","params":{},"extra":1}]}`, `{"schemaVersion":1,"steps":[`, strings.Repeat(" ", maxInputBytes+1)} {
		err := executeRunPlan(t, plan, "connect", r, out)
		if ExitCode(err) != 20 || calls != 0 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	}
}

func TestRunUsesRuntimeScopeForPathAndManagedIdempotency(t *testing.T) {
	var path, idempotency string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			calls++
			w.Header().Set("Content-Type", "application/json")
			state := "working"
			if calls > 1 {
				state = "ready"
			}
			_, _ = w.Write([]byte(`{"data":{"status":"` + state + `"}}`))
			return
		}
		path = r.URL.Path
		idempotency = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer srv.Close()
	r, out := testRunRuntime(t, srv.URL, time.Second)
	plan := `{"schemaVersion":1,"steps":[{"stage":"connect","operation":"projects:canonicalProjectTransition","params":{"projectId":"${projectId}"},"query":{},"input":{"to":"ready"},"bodyIdempotency":true,"check":{"operation":"projects:canonicalProjectStateRead","params":{"projectId":"${projectId}"},"query":{},"pointer":"/data/status","equals":"ready"}}]}`
	r.PollInterval = time.Millisecond
	if err := executeRunPlan(t, plan, "connect", r, out); err != nil {
		t.Fatal(err)
	}
	if path != "/projects/17/transitions" || idempotency == "" {
		t.Fatalf("path=%q idempotency=%q", path, idempotency)
	}
}
