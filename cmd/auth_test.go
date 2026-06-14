package cmd

import (
	"strings"
	"testing"
)

// TestWindowsBrowserArgs verifies the argv used to launch a URL via cmd.exe on
// Windows. The OAuth authorize URL contains many `&`-separated query params; if
// they are not escaped to `^&`, cmd.exe truncates the URL at the first `&` and
// the browser opens a broken/incomplete login page.
func TestWindowsBrowserArgs(t *testing.T) {
	authorizeURL := "https://beta-auth.pyxcloud.io/realms/pyx/protocol/openid-connect/auth?client_id=pyxcloud-cli&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1234%2Fcallback&scope=openid+profile+email+offline_access&state=abc&code_challenge=xyz&code_challenge_method=S256"

	args := windowsBrowserArgs(authorizeURL)

	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}
	if args[0] != "/c" {
		t.Errorf("expected first arg %q, got %q", "/c", args[0])
	}
	if args[1] != "start" {
		t.Errorf("expected second arg %q, got %q", "start", args[1])
	}
	// Mandatory empty title arg: without it, a quoted URL would be taken as the
	// window title by `start`.
	if args[2] != "" {
		t.Errorf("expected empty title arg, got %q", args[2])
	}

	url := args[3]
	// Every `&` must have been escaped to `^&`.
	if strings.Contains(strings.ReplaceAll(url, "^&", ""), "&") {
		t.Errorf("found an unescaped & in URL arg: %q", url)
	}
	if want := strings.Count(authorizeURL, "&"); strings.Count(url, "^&") != want {
		t.Errorf("expected %d escaped &, got %d in %q", want, strings.Count(url, "^&"), url)
	}
	// The full URL must be preserved end-to-end (no truncation).
	if got := strings.ReplaceAll(url, "^&", "&"); got != authorizeURL {
		t.Errorf("URL not preserved after unescaping:\n got:  %q\n want: %q", got, authorizeURL)
	}
}

func TestWindowsBrowserArgsNoAmpersand(t *testing.T) {
	args := windowsBrowserArgs("https://example.com/path")
	if args[3] != "https://example.com/path" {
		t.Errorf("URL without & should be unchanged, got %q", args[3])
	}
}
