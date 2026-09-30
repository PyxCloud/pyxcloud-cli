package passocontract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
)

func TestInvokeUsesContractPathAndQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/vibe/projects/a%2Fb/journey" {
			t.Errorf("unexpected path %q", r.URL.EscapedPath())
		}
		if r.URL.Query().Get("fresh") != "true" {
			t.Errorf("unexpected query %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"stage":"live","projectId":7},"version":3}`))
	}))
	defer server.Close()
	client := passotransport.New(server.URL, nil)
	response, err := Invoke(context.Background(), client, "journey:getJourney", map[string]string{"projectId": "a/b"}, url.Values{"fresh": {"true"}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var envelope JourneyEnvelope
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data == nil || envelope.Data.ProjectId == nil || *envelope.Data.ProjectId != 7 || envelope.Data.Stage == nil || *envelope.Data.Stage != "live" {
		t.Fatalf("decoded unexpected journey: %+v", envelope)
	}
}

func TestInvokeRejectsUnknownAndInvalidParameters(t *testing.T) {
	client := passotransport.New("http://example.invalid", nil)
	for _, tc := range []struct {
		key    string
		params map[string]string
	}{
		{"journey:missing", nil},
		{"journey:getJourney", nil},
		{"journey:getJourney", map[string]string{"projectId": "1", "extra": "x"}},
	} {
		if _, err := Invoke(context.Background(), client, tc.key, tc.params, nil, nil, ""); err == nil {
			t.Errorf("expected local rejection for %s", tc.key)
		}
	}
}

func TestInvokeWithIfMatchPropagatesOnlyExplicitVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != "0" {
			t.Errorf("If-Match=%q", r.Header.Get("If-Match"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	version := int64(0)
	if _, err := InvokeWithIfMatch(context.Background(), passotransport.New(server.URL, nil), "projects:projectStateAdvance", map[string]string{"projectId": "42"}, nil, nil, "", &version); err != nil {
		t.Fatal(err)
	}
}
