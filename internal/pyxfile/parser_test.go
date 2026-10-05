package pyxfile

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/plan.golden.json")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

const validExample = `PYX 1
APP shopdemo
ENV prod
WHERE eu
PROVIDER aws REGION "EU West"
REPO github.com/acme/shop
ARCH:
  place prod:
    network public:
      expose: [80, 443]
      machines:
        web:
          size: 2cpu/8gb
          os: debian/12
    network closed:
      services:
        db:
          type: managed-database
          size: small
USE cache AS redis WITH maxmemory-policy AND tls
USE dns AS zone
`

func TestParseValid(t *testing.T) {
	plan, err := Parse(validExample)
	if err != nil {
		t.Fatalf("Parse(valid) = error %v", err)
	}
	if plan.SpecVersion != 1 || plan.App != "shopdemo" || plan.Env != "prod" {
		t.Errorf("bad header: %+v", plan)
	}
	if plan.DeployArea != "eu" || plan.Provider != "aws" || plan.Region != "EU West" {
		t.Errorf("bad WHERE/PROVIDER: %+v", plan)
	}
	if plan.Repo != "github.com/acme/shop" {
		t.Errorf("bad REPO: %q", plan.Repo)
	}
	if len(plan.Components) != 6 {
		t.Fatalf("want 6 components, got %d: %+v", len(plan.Components), plan.Components)
	}
	byName := map[string]Component{}
	for _, c := range plan.Components {
		byName[c.Name] = c
	}
	if c := byName["web"]; c.Type != "virtual-machine" || c.Size != "2cpu/8gb" || c.OS != "debian/12" ||
		c.Network != "public" || c.Place != "prod" {
		t.Errorf("bad web component: %+v", c)
	}
	if c := byName["db"]; c.Type != "managed-database" || c.Network != "closed" {
		t.Errorf("bad db component: %+v", c)
	}
	if c := byName["redis"]; c.Type != "cache" || len(c.Features) != 2 {
		t.Errorf("bad redis component: %+v", c)
	}
	if c := byName["zone"]; c.Type != "dns-zone" {
		t.Errorf("alias dns not resolved to dns-zone: %+v", c)
	}
	if c := byName["public"]; len(c.Expose) != 2 || c.Expose[0] != 80 || c.Expose[1] != 443 {
		t.Errorf("bad expose: %+v", c)
	}
}

func TestParseGoldenFile(t *testing.T) {
	in, err := os.ReadFile(filepath.Join("testdata", "valid.shopdemo.Pyxfile"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Parse(string(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "plan.golden.json")
	if *updateGolden {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"empty", "", "empty Pyxfile"},
		{"bad-first-directive", "FOO bar\nPYX 1\n", "first directive must be PYX 1"},
		{"missing-pyx-directive", "APP a\nENV b\nUSE cache AS c\n", "missing PYX 1 header"},
		{"bad-version", "PYX 2\nAPP a\nENV b\nUSE cache AS c\n", "unsupported spec version"},
		{"missing-env", "PYX 1\nAPP a\nUSE cache AS c\n", "missing ENV directive"},
		{"missing-app", "PYX 1\nENV b\nUSE cache AS c\n", "missing APP directive"},
		{"unknown-type", "PYX 1\nAPP a\nENV b\nUSE quantum-computer AS c\n", `unknown component type "quantum-computer"`},
		{"unknown-type-arch", "PYX 1\nAPP a\nENV b\nARCH:\n  place p:\n    network n:\n      services:\n        db:\n          type: mainframe\n", "unknown component type \"mainframe\""},
		{"use-without-as", "PYX 1\nAPP a\nENV b\nUSE cache redis\n", `USE requires "USE <type> AS <name>"`},
		{"service-missing-type", "PYX 1\nAPP a\nENV b\nARCH:\n  place p:\n    network n:\n      services:\n        db:\n          size: small\n", "missing its type"},
		{"no-components", "PYX 1\nAPP a\nENV b\n", "no components declared"},
		{"unknown-directive", "PYX 1\nAPP a\nENV b\nFROBNICATE x\n", "unknown directive"},
		{"unknown-arch-attr", "PYX 1\nAPP a\nENV b\nARCH:\n  place p:\n    network n:\n      machines:\n        web:\n          gpu: yes\n", "unknown ARCH attribute"},
		{"bad-expose", "PYX 1\nAPP a\nENV b\nARCH:\n  place p:\n    network n:\n      expose: [http]\n", "bad expose list"},
		{"machines-outside-network", "PYX 1\nAPP a\nENV b\nARCH:\n  machines:\n", "must be nested inside a network block"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.in)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestResolveType(t *testing.T) {
	for in, want := range map[string]string{
		"virtual-machine": "virtual-machine", "vm": "virtual-machine",
		"Managed-Database": "managed-database", "dns": "dns-zone",
		"cdn": "cdn-service", "waf": "waf-service", "queue": "managed-queue",
		"kubernetes": "managed-kubernetes", "serverless": "serverless-function",
		"secrets": "secrets-manager", "email": "email-service",
		"monitoring": "monitoring-service", "vm-volume": "vm-volume",
		"event-streaming": "event-streaming", "event-bus": "event-streaming",
	} {
		if got := ResolveType(in); got != want {
			t.Errorf("ResolveType(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ResolveType("quantum-computer"); got != "" {
		t.Errorf("ResolveType(unknown) = %q, want \"\"", got)
	}
}
