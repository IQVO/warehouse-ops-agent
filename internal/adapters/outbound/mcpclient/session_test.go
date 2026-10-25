package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSessionPinsLegacyProtocolVersion pins the handshake contract that
// broke live on 2026-10-25: every MCP server in this fleet is a STATEFUL
// Streamable HTTP server, and the go-sdk's default protocol (2026-07-28)
// is rejected by them unless a server/discover negotiation succeeds first
// -- a dance that failed inside the service mesh. The Session must send
// initialize with the pinned legacy version 2025-11-25 directly.
func TestSessionPinsLegacyProtocolVersion(t *testing.T) {
	var gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		var req struct {
			Method string `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Method == "initialize" {
			gotVersion = req.Params.ProtocolVersion
		}
		// A stateful-streamable-shaped answer: JSON over SSE.
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"" + req.Params.ProtocolVersion + "\",\"capabilities\":{},\"serverInfo\":{\"name\":\"t\",\"version\":\"0\"}}}\n\n"))
	}))
	defer srv.Close()

	s := New(Config{Name: "test-upstream", Endpoint: srv.URL, Timeout: 2 * time.Second})
	sess, err := s.connect(context.Background())
	if err != nil {
		t.Fatalf("connect against a stateful legacy server must succeed with the pinned version, got: %v", err)
	}
	_ = sess.Close()
	if gotVersion != "2025-11-25" {
		t.Fatalf("initialize protocolVersion = %q, want the pinned legacy \"2025-11-25\"", gotVersion)
	}
}
