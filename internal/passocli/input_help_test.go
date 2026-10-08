package passocli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectCreationHelpAndCatalogDescribeActualInput(t *testing.T) {
	var out bytes.Buffer
	r := New(Options{Out: &out, Err: &out, Store: catalogTripwireStore{}})
	r.SetArgs([]string{"projects", "create", "--help"})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name", "description", `"name":"My project"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing%s:%s", want, out.String())
		}
	}
	c := buildCommandsCatalog(r)
	b, _ := json.Marshal(c)
	var doc struct {
		Commands []struct {
			Path    string          `json:"path"`
			Input   json.RawMessage `json:"inputSchema"`
			Example json.RawMessage `json:"inputExample"`
		} `json:"commands"`
	}
	json.Unmarshal(b, &doc)
	for _, cmd := range doc.Commands {
		if cmd.Path == "projects create" {
			if !bytes.Contains(cmd.Input, []byte(`"required":["name"]`)) || !bytes.Contains(cmd.Example, []byte(`"name":"My project"`)) {
				t.Fatalf("missing creation input%s", b)
			}
			return
		}
	}
	t.Fatal("missing command")
}
func TestDiscoveryAndDocumentationHelpDescribeBodySemantics(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{{[]string{"discover", "start", "--help"}, "No request body"}, {[]string{"docs", "generate", "--help"}, "runId"}} {
		var out bytes.Buffer
		r := New(Options{Out: &out, Err: &out, Store: catalogTripwireStore{}})
		r.SetArgs(tc.args)
		if e := r.Execute(); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatal(out.String())
		}
	}
}
