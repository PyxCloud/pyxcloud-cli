package passocli

import (
	"bytes"
	"context"
	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestScanBaselineTriggersBodylessServerPinnedRunAndWaitsExactRun(t *testing.T) {
	for _, mode := range []string{"completed", "foreign", "failed", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			var out bytes.Buffer
			posts, gets := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts++
					if r.URL.Path != "/vibe/projects/42/security/scan-baseline" || r.Header.Get("Idempotency-Key") == "" {
						t.Errorf("invalid trigger %s", r.URL.Path)
					}
					b, _ := io.ReadAll(r.Body)
					if len(b) != 0 {
						t.Error("client invented body/pins")
					}
					w.WriteHeader(202)
					io.WriteString(w, `{"runId":"scan-1","projectId":42}`)
					return
				}
				gets++
				if r.URL.Path != "/vibe/projects/42/versions/7/security/scan" {
					t.Errorf("wrong poll path %s", r.URL.Path)
				}
				switch mode {
				case "foreign":
					io.WriteString(w, `{"runId":"scan-other","projectId":42,"projectVersionId":7,"state":"COMPLETED"}`)
				case "failed":
					io.WriteString(w, `{"runId":"scan-1","projectId":42,"projectVersionId":7,"state":"FAILED"}`)
				case "timeout":
					io.WriteString(w, `{"runId":"scan-1","projectId":42,"projectVersionId":7,"state":"RUNNING"}`)
				default:
					io.WriteString(w, `{"runId":"scan-1","projectId":42,"projectVersionId":7,"state":"COMPLETED"}`)
				}
			}))
			defer srv.Close()
			p, _ := passoauth.ResolveProfile("sandbox")
			p.APIURL = srv.URL
			r := &Runtime{Profile: p, Client: passotransport.New(srv.URL, func(context.Context) (string, error) { return "fixture", nil }), ProjectID: 42, VersionSequence: 7, LedgerPath: filepath.Join(dir, "run.json"), EvidenceDir: filepath.Join(dir, "evidence"), Out: &out, JSON: true, timeout: time.Second, PollInterval: time.Millisecond}
			if mode == "timeout" {
				r.timeout = 80 * time.Millisecond
			}
			root := releaseTestRoot(r, nil, &out)
			root.SetArgs([]string{"secure", "scan-baseline", "--wait"})
			err := root.Execute()
			if (err == nil) != (mode == "completed") {
				t.Fatalf("mode%s err%v output%s", mode, err, out.String())
			}
			if posts != 1 || gets < 1 {
				t.Fatalf("calls%d/%d", posts, gets)
			}
			if mode == "completed" && !bytes.Contains(out.Bytes(), []byte(`"status":"completed"`)) {
				t.Fatalf("completion missing%s", out.String())
			}
		})
	}
}
