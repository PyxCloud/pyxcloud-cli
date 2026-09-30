package main

import (
	"os"
	"path/filepath"
	"reflect"
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
