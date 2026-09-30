package passostate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingAndRejectsInvalidFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	got, err := Load(path)
	if err != nil || got.SchemaVersion != 1 || got.Operations == nil || len(got.Operations) != 0 {
		t.Fatalf("Load missing = %#v, %v", got, err)
	}

	for name, contents := range map[string]string{
		"malformed": `{`,
		"trailing":  `{} {}`,
		"version":   `{"schemaVersion":2}`,
		"project":   `{"schemaVersion":1,"projectId":-1}`,
		"label":     `{"schemaVersion":1,"versionLabel":"../main"}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(p, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(p)
			if err == nil || strings.Contains(err.Error(), contents) {
				t.Fatalf("Load error = %v; expected safe validation error", err)
			}
		})
	}
}

func TestValidVersionLabelsAreSingleSafeGitRefs(t *testing.T) {
	for _, label := range []string{"r1-6387061", "release/2026.09", "v2_rc1"} {
		if !ValidVersionLabel(label) {
			t.Errorf("ValidVersionLabel(%q)=false", label)
		}
	}
	for _, label := range []string{"", "../main", "r1-..evil", "r1 label", "r1@{x}", ".hidden", "r1.lock", "r1//x", "r1/"} {
		if ValidVersionLabel(label) {
			t.Errorf("ValidVersionLabel(%q)=true", label)
		}
	}
}

func TestSaveLoadAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private", "nested")
	path := filepath.Join(dir, "ledger.json")
	want := Ledger{SchemaVersion: 1, Profile: "p", ProjectID: 42, VersionID: "v", VersionLabel: "r1-6387061", VersionSequence: 23, ReleaseID: "r", RunID: "run", Cursor: "c", Operations: map[string]Operation{
		"op": {Key: "key", State: "done", UpdatedAt: time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)},
	}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Profile != want.Profile || got.ProjectID != want.ProjectID || got.VersionID != want.VersionID || got.VersionLabel != want.VersionLabel || got.VersionSequence != want.VersionSequence || got.Operations["op"] != want.Operations["op"] {
		t.Fatalf("Load after Save = %#v, %v", got, err)
	}
	checkMode(t, dir, 0700)
	checkMode(t, path, 0600)
}

func TestIdempotencyKeyCanonicalAndSensitiveToInputs(t *testing.T) {
	first, err := IdempotencyKey("p", 3, "build", json.RawMessage(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := IdempotencyKey("p", 3, "build", json.RawMessage(`{"a":1,"b":2}`))
	if err != nil || first != second {
		t.Fatalf("canonical keys differ: %q %q (%v)", first, second, err)
	}
	for _, tc := range []struct {
		profile string
		stage   string
		input   json.RawMessage
	}{{"q", "build", json.RawMessage(`{"a":1,"b":2}`)}, {"p", "deploy", json.RawMessage(`{"a":1,"b":2}`)}, {"p", "build", json.RawMessage(`{"a":2,"b":2}`)}} {
		key, err := IdempotencyKey(tc.profile, 3, tc.stage, tc.input)
		if err != nil || key == first {
			t.Fatalf("changed input yielded %q, %v", key, err)
		}
	}
	for _, raw := range []json.RawMessage{json.RawMessage(``), json.RawMessage(`{}`)} {
		key, err := IdempotencyKey("p", 3, "build", raw)
		if err != nil || key != firstForEmptyEquivalent(t) {
			t.Fatalf("empty input normalization: %q, %v", key, err)
		}
		break
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{} {}`)} {
		if _, err := IdempotencyKey("p", 3, "build", raw); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func firstForEmptyEquivalent(t *testing.T) string {
	t.Helper()
	got, err := IdempotencyKey("p", 3, "build", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWriteEvidenceUniquePrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	e := Evidence{SchemaVersion: 1, Profile: "p", ProjectID: 3, Stage: "build", Status: "ok", RequestID: "req", VersionID: "v", ReleaseID: "r", RunID: "run", StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	first, err := WriteEvidence(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteEvidence(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("evidence paths are not unique: %q", first)
	}
	checkMode(t, dir, 0700)
	checkMode(t, first, 0600)
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "payload") || strings.Contains(string(data), "credential") {
		t.Fatalf("evidence contains forbidden data: %s", data)
	}
}

func checkMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
