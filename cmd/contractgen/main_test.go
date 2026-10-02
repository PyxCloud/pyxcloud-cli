package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGenerationDeterministic(t *testing.T) {
	entries := []opEntry{{Key: "journey:getJourney", Contract: "journey", ID: "getJourney", Method: "GET", Path: "/vibe/projects/{projectId}/journey", Params: []string{"projectId"}}}
	a, err := generateOps(entries)
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateOps(entries)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("operation generation is not deterministic")
	}
}

func TestCheckRejectsDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.go")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := output(path, []byte("new"), true); err == nil {
		t.Fatal("expected drift check to fail")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatal("check mode modified generated file")
	}
}

func TestResolveLocalParameterRefsAndGeneratePathParams(t *testing.T) {
	parameters := map[string]parameter{
		"ProjectId": {Name: "projectId", In: "path", Required: true},
		"Version":   {Name: "projectVersionId", In: "path", Required: true},
		"IfMatch":   {Name: "If-Match", In: "header", Required: true},
	}
	refs := []parameter{{Ref: "#/components/parameters/ProjectId"}, {Ref: "#/components/parameters/Version"}, {Ref: "#/components/parameters/IfMatch"}}
	resolved, err := resolveParameters(refs, parameters)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 3 || resolved[0].Name != "projectId" || resolved[0].In != "path" || !resolved[0].Required || resolved[2].Name != "If-Match" || resolved[2].In != "header" || !resolved[2].Required {
		t.Fatalf("resolved parameters=%#v", resolved)
	}
	entries := []opEntry{{Key: "documentation:read", Contract: "documentation", ID: "read", Method: "GET", Path: "/projects/{projectId}/versions/{projectVersionId}", Params: []string{resolved[0].Name, resolved[1].Name}}}
	generated, err := generateOps(entries)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `"documentation:read": {"projectId", "projectVersionId"}`) {
		t.Fatalf("path params missing from generated output:\n%s", generated)
	}
}

func TestResolveLocalParameterRefsFailsClosed(t *testing.T) {
	definitions := map[string]parameter{
		"A": {Ref: "#/components/parameters/B"},
		"B": {Ref: "#/components/parameters/A"},
	}
	for _, ref := range []string{"https://example.test/openapi.json#/parameters/ProjectId", "#/components/parameters/Missing", "#/components/parameters/A"} {
		if _, err := resolveParameters([]parameter{{Ref: ref}}, definitions); err == nil {
			t.Errorf("accepted invalid/cyclic reference %q", ref)
		}
	}
}
