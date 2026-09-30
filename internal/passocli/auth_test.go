package passocli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExecuteEmitsOneJSONFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--json", "status"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if strings.Count(out.String(), "\n") != 1 || !strings.Contains(out.String(), `"code":"project_required"`) {
		t.Fatalf("unexpected JSON output %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr %q", errOut.String())
	}
}
