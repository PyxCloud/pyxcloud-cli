package passocli

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
)

type setupTripwireStore struct{}

func (setupTripwireStore) Load(string) (passoauth.Token, error) {
	panic("agent setup read credentials")
}
func (setupTripwireStore) Save(string, passoauth.Token) error {
	panic("agent setup saved credentials")
}
func (setupTripwireStore) Delete(string) error {
	panic("agent setup deleted credentials")
}

type setupTripwireTransport struct{}

func (setupTripwireTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("agent setup made HTTP request")
}

func executeSetup(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	cmd := New(Options{Out: &out, Err: &errOut, Store: setupTripwireStore{}, HTTPClient: &http.Client{Transport: setupTripwireTransport{}}})
	cmd.SetArgs(append([]string{"setup"}, args...))
	err := cmd.ExecuteContext(context.Background())
	if err != nil && errOut.Len() > 0 {
		t.Logf("stderr: %s", errOut.String())
	}
	return out.String(), err
}

func setupSkillPath(home string) string {
	return filepath.Join(home, ".agents", "skills", "passo")
}

func TestSetupCodexAndAgentsInstallOneSharedManagedSkill(t *testing.T) {
	home := t.TempDir()
	if _, err := executeSetup(t, home, "codex"); err != nil {
		t.Fatalf("setup codex: %v", err)
	}
	target := setupSkillPath(home)
	first, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeSetup(t, home, "agents"); err != nil {
		t.Fatalf("setup agents: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("codex and agents must share one installed skill: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "passo")); !os.IsNotExist(err) {
		t.Fatalf("created duplicate Codex skill path: %v", err)
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Join(target, "SKILL.md"), 0600}, {filepath.Join(target, ".managed-by-passo-cli"), 0600}, {target, 0700}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatalf("%s mode=%v err=%v, want %v", item.path, infoMode(info), err, item.mode)
		}
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}

func TestSetupDryRunReportsActionAndDoesNotCreateDirectoriesOrReadCredentials(t *testing.T) {
	home := t.TempDir()
	out, err := executeSetup(t, home, "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "install") || !strings.Contains(out, setupSkillPath(home)) {
		t.Fatalf("dry-run omitted action or target path: %q", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created installation directories: %v", err)
	}
	if _, err := executeSetup(t, home, "agents", "--dry-run", "--remove"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("remove dry-run created installation directories: %v", err)
	}
}

func TestSetupRefreshesOnlyManagedSkillAtomically(t *testing.T) {
	home := t.TempDir()
	if _, err := executeSetup(t, home, "codex"); err != nil {
		t.Fatal(err)
	}
	target := setupSkillPath(home)
	skillPath := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("user edit after install"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "local-note.txt"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSetup(t, home, "agents"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("assets", "passo", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("managed refresh did not restore embedded skill")
	}
	if note, err := os.ReadFile(filepath.Join(target, "local-note.txt")); err != nil || string(note) != "preserve" {
		t.Fatalf("managed refresh changed unrelated file: %q err=%v", note, err)
	}
}

func TestSetupRefusesUnmanagedDirectoryWithoutChangingIt(t *testing.T) {
	home := t.TempDir()
	target := setupSkillPath(home)
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("authored by user"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSetup(t, home, "codex"); err == nil {
		t.Fatal("overwrote unmanaged skill directory")
	}
	got, err := os.ReadFile(skillPath)
	if err != nil || string(got) != "authored by user" {
		t.Fatalf("unmanaged skill changed: %q err=%v", got, err)
	}
	if err := os.WriteFile(filepath.Join(target, passoSkillMarker), []byte("managed-by-someone-else\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSetup(t, home, "agents"); err == nil {
		t.Fatal("accepted ownership marker from another installer")
	}
	got, err = os.ReadFile(skillPath)
	if err != nil || string(got) != "authored by user" {
		t.Fatalf("skill with foreign marker changed: %q err=%v", got, err)
	}
}

func TestSetupRefusesSymlinkedParents(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSetup(t, home, "agents"); err == nil {
		t.Fatal("followed a symlinked installation parent")
	}
	if _, err := os.Lstat(filepath.Join(outside, "skills")); !os.IsNotExist(err) {
		t.Fatalf("wrote through symlink into external directory: %v", err)
	}
}

func TestSetupRemoveDeletesOnlyKnownManagedFilesAndRefusesUnknownFiles(t *testing.T) {
	home := t.TempDir()
	if _, err := executeSetup(t, home, "codex"); err != nil {
		t.Fatal(err)
	}
	target := setupSkillPath(home)
	unknown := filepath.Join(target, "user-file.txt")
	if err := os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSetup(t, home, "codex", "--remove"); err == nil {
		t.Fatal("removed managed directory containing unknown user file")
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatalf("unknown file was removed: %v", err)
	}
	if err := os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSetup(t, home, "agents", "--remove"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("managed installation remains after remove: %v", err)
	}
}
