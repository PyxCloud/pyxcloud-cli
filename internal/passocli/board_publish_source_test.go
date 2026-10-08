package passocli

import (
	"encoding/json"
	"fmt"
	"github.com/pyxcloud/pyxcloud-cli/internal/passocontract"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublishSourceRejectsCallerAuthority(t *testing.T) {
	for _, b := range []string{`{}`, `{"versionLabel":""}`, `{"versionLabel":"../main"}`, `{"versionLabel":"r1-a7280bc","commitSha":"caller"}`, `{"versionLabel":"r1-a7280bc","versionLabel":"r2-a7280bc"}`, `{"versionLabel":"r1-a7280bc"} {}`} {
		if strictBoardInput("publish-source", json.RawMessage(b)) == nil {
			t.Errorf("accepted invalid publication input %s", b)
		}
	}
}
func TestPublishSourceCanonicalRoute(t *testing.T) {
	op := passocontract.Operations["board-rest:publish-source"]
	if op.Method != "POST" || op.Path != "/vibe/projects/{projectId}/board/tasks/{taskId}/publish-source" {
		t.Fatalf("publication route missing: %+v", op)
	}
}
func TestPublishSourceRejectsForeignOrIncompleteReceipt(t *testing.T) {
	input := map[string]json.RawMessage{"versionLabel": json.RawMessage(`"r1-a7280bc"`)}
	params := map[string]string{"projectId": "114", "taskId": "sec-real"}
	good := map[string]any{"projectId": 114, "taskId": "sec-real", "versionId": "11111111-1111-4111-8111-111111111111", "versionLabel": "r1-a7280bc", "repository": "amemifra/pharos", "ref": "refs/heads/passo/release/r1-a7280bc", "commitSha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "treeSha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "pinRevision": 1, "pinReplayed": false, "publicationReplayed": false, "discoveryRunId": "22222222-2222-4222-8222-222222222222", "discoveryState": "queued"}
	for key, value := range map[string]any{"projectId": 115, "taskId": "foreign", "versionLabel": "r2-a7280bc", "versionId": "invalid", "commitSha": "bad", "treeSha": "bad", "pinRevision": 0, "discoveryRunId": "bad", "discoveryState": "invented", "pinReplayed": "false", "repository": "../foreign"} {
		copy := map[string]any{}
		for k, v := range good {
			copy[k] = v
		}
		copy[key] = value
		b, _ := json.Marshal(copy)
		if validateBoardResponse("board-rest:publish-source", params, input, b) == nil {
			t.Errorf("accepted corrupt %s", key)
		}
	}
	for _, key := range []string{"projectId", "taskId", "pinReplayed", "publicationReplayed", "discoveryRunId", "ref"} {
		copy := map[string]any{}
		for k, v := range good {
			copy[k] = v
		}
		delete(copy, key)
		b, _ := json.Marshal(copy)
		if validateBoardResponse("board-rest:publish-source", params, input, b) == nil {
			t.Errorf("accepted missing %s", key)
		}
	}
	for _, ref := range []string{"refs/heads/main", "refs/heads/passo/release/r2-a7280bc", "refs/heads/foreign/release/r1-a7280bc"} {
		copy := map[string]any{}
		for k, v := range good {
			copy[k] = v
		}
		copy["ref"] = ref
		b, _ := json.Marshal(copy)
		if validateBoardResponse("board-rest:publish-source", params, input, b) == nil {
			t.Errorf("accepted foreign release ref %s", ref)
		}
	}
	b, _ := json.Marshal(good)
	if err := validateBoardResponse("board-rest:publish-source", params, input, b); err != nil {
		t.Fatal(err)
	}
}

func TestPublishSourceHTTPAndFailedCapture(t *testing.T) {
	for _, state := range []string{"queued", "succeeded", "failed", "cancelled", "stale"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				calls++
				if q.Method != "POST" || q.URL.Path != "/vibe/projects/42/board/tasks/sec-real/publish-source" {
					t.Errorf("wrong scoped route %s %s", q.Method, q.URL.Path)
				}
				raw, _ := io.ReadAll(q.Body)
				if string(raw) != `{"versionLabel":"r1-a7280bc"}` {
					t.Errorf("unexpected input %s", raw)
				}
				if state == "queued" {
					w.WriteHeader(202)
				}
				fmt.Fprintf(w, `{"projectId":42,"taskId":"sec-real","versionId":"11111111-1111-4111-8111-111111111111","versionLabel":"r1-a7280bc","repository":"amemifra/pharos","ref":"refs/heads/passo/release/r1-a7280bc","commitSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","treeSha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","pinRevision":1,"pinReplayed":false,"publicationReplayed":false,"discoveryRunId":"22222222-2222-4222-8222-222222222222","discoveryState":"%s"}`, state)
			}))
			defer server.Close()
			r := testRunner(t, server, t.TempDir())
			root := boardRoot(t, r, `{"versionLabel":"r1-a7280bc"}`)
			root.SetArgs([]string{"board", "publish-source", "sec-real", "--input", "-"})
			err := root.Execute()
			failed := state == "failed" || state == "cancelled" || state == "stale"
			if failed && (err == nil || ExitCode(err) != 20) {
				t.Fatalf("failed recapture accepted: %v", err)
			}
			if !failed && err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls %d", calls)
			}
		})
	}
}
