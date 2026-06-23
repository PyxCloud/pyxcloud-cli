package board

import "testing"

func TestDecodeEnvelope_PlainJSON(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":2,"result":{"ok":true}}`)
	r, err := decodeEnvelope(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Error != nil || string(r.Result) != `{"ok":true}` {
		t.Errorf("unexpected result: %+v", r)
	}
}

func TestDecodeEnvelope_SSE(t *testing.T) {
	raw := []byte("event: message\n" +
		"data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"value\":42}}\n\n")
	r, err := decodeEnvelope(raw)
	if err != nil {
		t.Fatalf("decode sse: %v", err)
	}
	if string(r.Result) != `{"value":42}` {
		t.Errorf("sse result mismatch: %s", r.Result)
	}
}

func TestDecodeEnvelope_Error(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"bad"}}`)
	r, err := decodeEnvelope(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Error == nil || r.Error.Code != -32602 {
		t.Errorf("expected error envelope, got %+v", r)
	}
}

func TestDecodeEnvelope_NoPayload(t *testing.T) {
	if _, err := decodeEnvelope([]byte("garbage without data lines")); err == nil {
		t.Error("expected error for payload-less response")
	}
}

// TestResolveManifest_NilClientFallsBack proves graceful degradation: with no client (no live
// server) the CLI uses the vendored manifest and reports live=false.
func TestResolveManifest_NilClientFallsBack(t *testing.T) {
	actions, live := ResolveManifest(nil)
	if live {
		t.Error("nil client must report live=false")
	}
	if len(actions) != len(VendoredManifest()) {
		t.Errorf("fallback manifest size mismatch: %d", len(actions))
	}
}
