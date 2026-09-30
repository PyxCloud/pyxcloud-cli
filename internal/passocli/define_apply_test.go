package passocli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const derivedScope = `{"v":"1.0","candidateId":"compile-9","source":"documentation","compilationRevision":9,"result":{"goal":"demo"}}`
const derivationResponse = `{"scope_contract":` + derivedScope + `,"compilationRevision":9}`

func TestDefineApplyDerivesReadsAndTransitionsAuthoritativeContract(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		switch step {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/vibe/projects/42/scope/derivation" || r.ContentLength != 0 {
				t.Errorf("derive request: %s %s len=%d", r.Method, r.URL.Path, r.ContentLength)
			}
			io.WriteString(w, derivationResponse)
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/projects/42/state" {
				t.Errorf("read request: %s %s", r.Method, r.URL.Path)
			}
			io.WriteString(w, `{"projectId":42,"state":"BUILD.DEVELOP","version":17,"data":{}}`)
		case 3:
			if r.Method != http.MethodPost || r.URL.Path != "/projects/42/transitions" {
				t.Errorf("transition request: %s %s", r.Method, r.URL.Path)
			}
			var body struct {
				Event   string                     `json:"event"`
				Version int                        `json:"version"`
				Payload map[string]json.RawMessage `json:"payload"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Event != "scope.contract.derived" || body.Version != 17 || string(body.Payload["scope_contract"]) != derivedScope {
				t.Errorf("transition body %#v contract=%s", body, body.Payload["scope_contract"])
			}
			io.WriteString(w, `{"projectId":42,"state":"BUILD.SCOPE_ASSESSMENT","version":18,"data":{}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"define", "apply"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if step != 3 {
		t.Fatalf("requests=%d", step)
	}
}

func TestDefineApplySkipsWhenContractAlreadyObservedDownstream(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		switch step {
		case 1:
			io.WriteString(w, derivationResponse)
		case 2:
			io.WriteString(w, `{"projectId":42,"state":"SECURE.REVIEW","version":18,"data":{"scope_contract":{"result":{"goal":"demo"},"compilationRevision":9,"source":"documentation","candidateId":"compile-9","v":"1.0"}}}`)
		default:
			t.Errorf("unexpected POST after matching contract: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"define", "apply"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if step != 2 {
		t.Fatalf("requests=%d", step)
	}
}

func TestDefineApplyRefusesDifferentContractOutsideAllowedState(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		if step == 1 {
			io.WriteString(w, derivationResponse)
			return
		}
		io.WriteString(w, `{"projectId":42,"state":"SECURE.REVIEW","version":18,"data":{"scope_contract":{"v":"1.0","candidateId":"old","source":"documentation","compilationRevision":1}}}`)
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"define", "apply"})
	err := root.ExecuteContext(context.Background())
	if err == nil || ExitCode(err) != 30 || err.Error() != "define_state_conflict" {
		t.Fatalf("error=%v", err)
	}
	if step != 2 {
		t.Fatalf("requests=%d", step)
	}
}

func TestDefineApplyRejectsMissingOrMalformedDerivationWithoutTransition(t *testing.T) {
	for _, response := range []string{`{}`, `{"scope_contract":[],"compilationRevision":9}`, `{"scope_contract":{"v":"1.0","candidateId":"x","source":"documentation","compilationRevision":0},"compilationRevision":9}`, `{"scope_contract":{"v":"1.0","candidateId":"x","source":"documentation","compilationRevision":9},"compilationRevision":0}`} {
		t.Run(response, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, response) }))
			defer srv.Close()
			root := preexecRoot(t, srv, 42)
			root.SetArgs([]string{"define", "apply"})
			err := root.ExecuteContext(context.Background())
			if err == nil || ExitCode(err) != 30 {
				t.Fatalf("error=%v", err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestDefineApplyHasNoInputFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"define", "apply", "--input", "x"})
	if err := root.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("error=%v", err)
	}
}

func TestDefineApplyRequiresProjectBeforeNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	root := preexecRoot(t, srv, 0)
	root.SetArgs([]string{"define", "apply"})
	err := root.ExecuteContext(context.Background())
	if err == nil || err.Error() != "project_required" || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestDefineApplyPreservesBackendVersionConflict(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		switch step {
		case 1:
			io.WriteString(w, derivationResponse)
		case 2:
			io.WriteString(w, `{"projectId":42,"state":"BUILD.DEVELOP","version":17,"data":{}}`)
		case 3:
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"code":"version_conflict","message":"stale version"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	root := preexecRoot(t, srv, 42)
	root.SetArgs([]string{"define", "apply"})
	err := root.ExecuteContext(context.Background())
	if err == nil || ExitCode(err) != 20 || err.Error() != "version_conflict" {
		t.Fatalf("error=%v", err)
	}
	if step != 3 {
		t.Fatalf("requests=%d", step)
	}
}
