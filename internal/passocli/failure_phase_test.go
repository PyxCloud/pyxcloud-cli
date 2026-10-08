package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClosedContextPhasesPreserveTimeoutCode(t *testing.T) {
	for _, phase := range []string{"credential_read", "refresh", "http_request"} {
		err := classify(passoauth.ContextPhase(phase, context.DeadlineExceeded))
		var exit *ExitError
		var diagnostic *phaseExitError
		if !errors.As(err, &exit) || exit.Code != "timeout" || ExitCode(err) != 20 || !errors.As(err, &diagnostic) || diagnostic.phase != phase {
			t.Fatalf("phase classification lost: %s", phase)
		}
	}
	if _, ok := passoauth.ContextPhase("secret arbitrary text", context.DeadlineExceeded).(*passoauth.PhaseError); ok {
		t.Fatal("unrecognized phase accepted")
	}
}

type cancelPhaseTransport struct{ cancel context.CancelFunc }

func (tr cancelPhaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.cancel()
	<-req.Context().Done()
	return nil, req.Context().Err()
}
func TestStatusHTTPPhaseReachesSafeStructuredOutput(t *testing.T) {
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut bytes.Buffer
	code := execute(ctx, []string{"--profile", "sandbox", "--project", "114", "--ledger", filepath.Join(t.TempDir(), "ledger.json"), "--json", "status"}, &out, &errOut, Options{
		Store:      &countedTokenStore{token: passoauth.Token{AccessToken: "private-fixture-value", ExpiresAt: time.Now().Add(time.Hour)}},
		HTTPClient: &http.Client{Transport: cancelPhaseTransport{cancel}},
	})
	var result Result
	if json.Unmarshal(out.Bytes(), &result) != nil || code != 20 || result.Code != "canceled" || result.FailurePhase != "status_http" {
		t.Fatalf("bad phase output: %s", out.String())
	}
	if strings.Contains(out.String(), "private-fixture-value") || strings.Contains(out.String(), "/vibe") || errOut.Len() != 0 {
		t.Fatal("private request content reached output")
	}
}

func TestRefreshCancellationReportsPhaseWithoutSaving(t *testing.T) {
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	store := &countedTokenStore{token: passoauth.Token{RefreshToken: "fixture"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	access := cachedTokenAccess(store, "staging", time.Now, func(context.Context, string) (passoauth.Token, error) {
		cancel()
		return passoauth.Token{}, context.Canceled
	})
	_, err := access(ctx)
	var phase *passoauth.PhaseError
	if !errors.As(err, &phase) || phase.Phase != "refresh" || !errors.Is(err, context.Canceled) || store.saves != 0 {
		t.Fatal("refresh cancellation phase or custody lost")
	}
}

func TestCredentialAccessRequiredIsActionableWithoutPrivateDetails(t *testing.T) {
	t.Setenv("PASSO_ACCESS_TOKEN", "")
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"--profile", "sandbox", "--project", "114", "--ledger", filepath.Join(t.TempDir(), "ledger.json"), "--json", "status"}, &out, &errOut, Options{Store: &countedTokenStore{err: passoauth.ErrCredentialAccessRequired}})
	var result Result
	if json.Unmarshal(out.Bytes(), &result) != nil || code != 10 || result.Code != "credential_access_required" {
		t.Fatalf("safe access code missing: %s", out.String())
	}
	action, ok := result.NextAction.(map[string]any)
	if !ok || action["key"] != "resolve_credential_access" || action["automaticRetry"] != false || errOut.Len() != 0 {
		t.Fatal("access recovery or retry contract lost")
	}
}
