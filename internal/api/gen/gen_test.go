package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const contractsDir = "../../../api/contracts"

// packageName maps a contract file name to its generated package name.
func packageName(contract string) string {
	base := strings.TrimSuffix(contract, ".openapi.json")
	return strings.NewReplacer("-", "", ".", "").Replace(base)
}

// TestEveryContractIsGeneratedOrListed fails when a vendored contract has no
// generated package (e.g. a new upstream contract after a sync) and is not
// listed in NotGenerated, or when a package/entry points at a contract that
// is no longer vendored. Local files only; no network.
func TestEveryContractIsGeneratedOrListed(t *testing.T) {
	contracts, err := filepath.Glob(filepath.Join(contractsDir, "*.openapi.json"))
	if err != nil || len(contracts) == 0 {
		t.Fatalf("no vendored contracts under %s (err=%v)", contractsDir, err)
	}
	vendored := map[string]bool{}
	for _, c := range contracts {
		name := filepath.Base(c)
		vendored[name] = true
		pkg := packageName(name)
		gen, err := os.ReadFile(filepath.Join(pkg, "generate.go"))
		_, skipped := NotGenerated[name]
		switch {
		case err == nil && skipped:
			t.Errorf("%s is listed in NotGenerated but has package %s", name, pkg)
		case err != nil && !skipped:
			t.Errorf("%s has no generated package %s and is not listed in NotGenerated", name, pkg)
		case err == nil && !strings.Contains(string(gen), "api/contracts/"+name):
			t.Errorf("%s/generate.go does not generate from api/contracts/%s", pkg, name)
		}
		if err == nil {
			if _, err := os.Stat(filepath.Join(pkg, pkg+".gen.go")); err != nil {
				t.Errorf("%s: generated file missing, run go generate ./internal/api/gen/...", pkg)
			}
		}
	}
	for name := range NotGenerated {
		if !vendored[name] {
			t.Errorf("NotGenerated lists %s, which is not vendored", name)
		}
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found := false
		for name := range vendored {
			if packageName(name) == e.Name() {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("package %s has no vendored contract", e.Name())
		}
	}
}
