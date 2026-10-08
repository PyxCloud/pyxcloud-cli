package passocli

import (
	"bytes"
	"encoding/json"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/spf13/cobra"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func boardRoot(t *testing.T, r *Runtime, input string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "passo", SilenceErrors: true, SilenceUsage: true}
	root.SetIn(strings.NewReader(input))
	r.JSON = true
	r.Out = &bytes.Buffer{}
	root.AddCommand(newBoardCommands(func(*cobra.Command) (*Runtime, error) { return r, nil }))
	return root
}
func TestBoardCompleteBlockedDoesNotCompleteLedger(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != "/vibe/projects/42/board/tasks/task-1/complete" {
			t.Error(q.URL.Path)
		}
		io.WriteString(w, `{"status":"blocked","code":"acceptance_required","missing":["review"],"failingChecks":[]}`)
	}))
	defer s.Close()
	r := testRunner(t, s, t.TempDir())
	root := boardRoot(t, r, `{"fenceToken":7,"usage":{"tokensIn":10,"tokensOut":2}}`)
	root.SetArgs([]string{"board", "complete", "task-1", "--input", "-"})
	e := root.Execute()
	if e == nil || ExitCode(e) == 0 {
		t.Fatal("blocked was reported successful")
	}
	l, _ := passostate.Load(r.LedgerPath)
	if l.Operations["board-rest:complete"].State != "blocked" {
		t.Fatalf("ledger %#v", l)
	}
}
func TestBoardExecuteRejectsUnownedInputsBeforeHTTP(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { calls++ }))
	defer s.Close()
	r := testRunner(t, s, t.TempDir())
	root := boardRoot(t, r, `{"commandId":"11111111-1111-4111-8111-111111111111","command":"echo fake"}`)
	root.SetArgs([]string{"board", "execute", "task-1", "--input", "-"})
	if root.Execute() == nil || calls != 0 {
		t.Fatal("server-owned input escaped")
	}
}
func TestBoardExecutionWaitUsesDurableReceiptAndScope(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		calls++
		if q.Method != "GET" || q.URL.Path != "/vibe/projects/42/board/executions/11111111-1111-4111-8111-111111111111" {
			t.Fatal(q.URL.Path)
		}
		state := "running"
		if calls > 1 {
			state = "succeeded"
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "11111111-1111-4111-8111-111111111111", "taskId": "task-1", "state": state})
	}))
	defer s.Close()
	r := testRunner(t, s, t.TempDir())
	r.PollInterval = time.Millisecond
	root := boardRoot(t, r, "")
	root.SetArgs([]string{"board", "execution", "11111111-1111-4111-8111-111111111111", "--task", "task-1", "--wait"})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	evidence, _ := filepath.Glob(r.EvidenceDir + "/*")
	if len(evidence) == 0 {
		t.Fatal("no read receipt")
	}
}
func TestBoardCanonicalRoutesBodiesAndCatalog(t *testing.T) {
	cases := []struct{ action, method, path, input, response string }{
		{"status", "GET", "/status", "", `{}`}, {"list", "GET", "/features", "", `{}`}, {"task", "GET", "/tasks/task-1", "", `{"taskId":"task-1"}`}, {"claim", "POST", "/tasks/task-1/claim", `{}`, `{"task":{"id":"task-1"},"lease":{"fenceToken":7}}`}, {"release", "POST", "/tasks/task-1/release", `{"fenceToken":7,"reason":"real reason"}`, `{"status":"released"}`}, {"resume", "POST", "/tasks/task-1/resume", `{"resumeNote":"real note"}`, `{"task":{"id":"task-1"}}`}, {"plan", "PUT", "/tasks/task-1/plan", `{"steps":[]}`, `{"taskId":"task-1","steps":[]}`}, {"execute", "POST", "/tasks/task-1/execute", `{"commandId":"11111111-1111-4111-8111-111111111111"}`, `{"id":"22222222-2222-4222-8222-222222222222","taskId":"task-1","commandId":"11111111-1111-4111-8111-111111111111","state":"queued"}`}, {"latest", "GET", "/tasks/task-1/execution", "", `{"taskId":"task-1"}`}, {"availability", "GET", "/tasks/task-1/execution-availability", "", `{"available":false}`}, {"verify", "POST", "/tasks/task-1/verify", `{"verdict":"fail","findings":"real finding"}`, `{"taskId":"task-1","verdict":"fail","recorded":true}`}, {"complete", "POST", "/tasks/task-1/complete", `{"fenceToken":7,"usage":{"tokensIn":1,"tokensOut":2},"evidence":[]}`, `{"status":"done","missing":[],"failingChecks":[]}`}}
	for _, c := range cases {
		t.Run(c.action, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method != c.method || q.URL.Path != "/vibe/projects/42/board"+c.path {
					t.Errorf("wrong route %s %s", q.Method, q.URL.Path)
				}
				if c.input != "" {
					b, _ := io.ReadAll(q.Body)
					var got, want any
					json.Unmarshal(b, &got)
					json.Unmarshal([]byte(c.input), &want)
					gb, _ := json.Marshal(got)
					wb, _ := json.Marshal(want)
					if string(gb) != string(wb) {
						t.Errorf("body %s wanted %s", gb, wb)
					}
					if q.Header.Get("Idempotency-Key") == "" {
						t.Error("no managed header")
					}
				}
				io.WriteString(w, c.response)
			}))
			defer s.Close()
			r := testRunner(t, s, t.TempDir())
			root := boardRoot(t, r, c.input)
			args := []string{"board", c.action}
			if c.action != "status" && c.action != "list" {
				args = append(args, "task-1")
			}
			if c.input != "" {
				args = append(args, "--input", "-")
			}
			root.SetArgs(args)
			if e := root.Execute(); e != nil {
				t.Fatal(e)
			}
			if c.input != "" {
				cmd, _, _ := root.Find(args)
				if !json.Valid([]byte(cmd.Annotations["inputSchema"])) || !json.Valid([]byte(cmd.Annotations["inputExample"])) {
					t.Fatal("not self describing")
				}
			}
		})
	}
}
func TestBoardExecutionFailClosedReceiptAndTimeout(t *testing.T) {
	for _, c := range []struct {
		name, body string
		wait       bool
		code       string
	}{{"foreign", `{"id":"11111111-1111-4111-8111-111111111111","taskId":"other","state":"succeeded"}`, false, "execution_scope_mismatch"}, {"unknown", `{"id":"11111111-1111-4111-8111-111111111111","taskId":"task-1","state":"imagined"}`, false, "unknown_execution_state"}, {"failed", `{"id":"11111111-1111-4111-8111-111111111111","taskId":"task-1","state":"failed"}`, false, "execution_failed"}, {"timeout", `{"id":"11111111-1111-4111-8111-111111111111","taskId":"task-1","state":"running"}`, true, "execution_wait_timeout"}} {
		t.Run(c.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { io.WriteString(w, c.body) }))
			defer s.Close()
			r := testRunner(t, s, t.TempDir())
			r.timeout = 25 * time.Millisecond
			r.PollInterval = time.Millisecond
			root := boardRoot(t, r, "")
			args := []string{"board", "execution", "11111111-1111-4111-8111-111111111111", "--task", "task-1"}
			if c.wait {
				args = append(args, "--wait")
			}
			root.SetArgs(args)
			e := root.Execute()
			if e == nil || e.Error() != c.code {
				t.Fatalf("%v wanted %s", e, c.code)
			}
		})
	}
}
func TestBoardInvalidCompletionNeverDurableDone(t *testing.T) {
	for _, body := range []string{`{"status":"done"}`, `{"status":"done","missing":["proof"],"failingChecks":[]}`, `{"status":"invented","missing":[],"failingChecks":[]}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { io.WriteString(w, body) }))
		r := testRunner(t, s, t.TempDir())
		root := boardRoot(t, r, `{"fenceToken":7,"usage":{"tokensIn":1,"tokensOut":2}}`)
		root.SetArgs([]string{"board", "complete", "task-1", "--input", "-"})
		if e := root.Execute(); e == nil || ExitCode(e) != 30 {
			t.Fatalf("%v", e)
		}
		l, _ := passostate.Load(r.LedgerPath)
		if l.Operations["board-rest:complete"].State != "uncertain" {
			t.Fatal(l)
		}
		s.Close()
	}
}
func TestBoardWrongTaskResponseCannotCompleteMutationLedger(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { io.WriteString(w, `{"taskId":"foreign","steps":[]}`) }))
	defer s.Close()
	r := testRunner(t, s, t.TempDir())
	root := boardRoot(t, r, `{"steps":[]}`)
	root.SetArgs([]string{"board", "plan", "task-1", "--input", "-"})
	e := root.Execute()
	if e == nil || ExitCode(e) != 30 {
		t.Fatalf("%v", e)
	}
	l, _ := passostate.Load(r.LedgerPath)
	if l.Operations["board-rest:plan"].State != "uncertain" {
		t.Fatal(l)
	}
}
func TestBoardEvidenceUsesScopedReadAndRefusalIsNotSuccess(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != "/vibe/projects/42/board/artifacts/11111111-1111-4111-8111-111111111111" {
			t.Fatal(q.URL.Path)
		}
		w.WriteHeader(404)
		io.WriteString(w, `{"error":"Not found"}`)
	}))
	defer s.Close()
	r := testRunner(t, s, t.TempDir())
	root := boardRoot(t, r, "")
	root.SetArgs([]string{"board", "evidence", "11111111-1111-4111-8111-111111111111"})
	if e := root.Execute(); e == nil {
		t.Fatal("refused read was successful")
	}
}
