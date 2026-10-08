package passocli

import (
	"encoding/json"
	"github.com/pyxcloud/pyxcloud-cli/internal/passocontract"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAcceptFindingsRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{`{}`, `{"fingerprints":[]}`, `{"fingerprints":[""]}`, `{"fingerprints":["fp"],"verdict":"pass"}`, `{"fingerprints":["fp"]} {}`} {
		if strictBoardInput("accept-findings", json.RawMessage(input)) == nil {
			t.Errorf("accepted invalid input %s", input)
		}
	}
}
func TestFindingsUseCanonicalSecurityRoutes(t *testing.T) {
	for name, path := range map[string]string{"findings": "/vibe/projects/{projectId}/security/ingest", "accept-findings": "/vibe/projects/{projectId}/security/ingest/accept"} {
		if passocontract.Operations["board-rest:"+name].Path != path {
			t.Errorf("missing canonical %s route", name)
		}
	}
}
func TestAcceptFindingsRejectsUnrequestedTask(t *testing.T) {
	input := map[string]json.RawMessage{"fingerprints": json.RawMessage(`["requested"]`)}
	if validateBoardResponse("board-rest:accept-findings", nil, input, json.RawMessage(`{"tasks":[{"fingerprint":"other","taskId":"task-real","created":true}]}`)) == nil {
		t.Fatal("accepted task for unrequested finding")
	}
}

func TestFindingsCommandsUseExactScopedHTTP(t *testing.T) {
	for _, action := range []string{"findings", "accept-findings"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				calls++
				want := "/vibe/projects/42/security/ingest"
				method := "GET"
				response := `{"run":null,"findings":[],"counts":{"total":0}}`
				if action == "accept-findings" {
					want += "/accept"
					method = "POST"
					response = `{"tasks":[{"fingerprint":"actual-finding","taskId":"actual-task","created":true}]}`
					b, _ := io.ReadAll(q.Body)
					if string(b) != `{"fingerprints":["actual-finding"]}` {
						t.Errorf("unexpected request body %s", b)
					}
				}
				if q.URL.Path != want || q.Method != method {
					t.Errorf("unexpected route %s %s", q.Method, q.URL.Path)
				}
				io.WriteString(w, response)
			}))
			defer server.Close()
			r := testRunner(t, server, t.TempDir())
			root := boardRoot(t, r, `{"fingerprints":["actual-finding"]}`)
			args := []string{"board", action}
			if action == "accept-findings" {
				args = append(args, "--input", "-")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("HTTP calls %d", calls)
			}
		})
	}
}
