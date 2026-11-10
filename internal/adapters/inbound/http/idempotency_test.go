package http

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestValidIdempotencyKey_SpecBoundary pins the key the middleware accepts:
// 1-128 printable ASCII characters whose first and last character are
// visible (ADR 0030; the same rule as workforce-management ADR 0042).
func TestValidIdempotencyKey_SpecBoundary(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"too short: empty", "", false},
		{"shortest valid: one visible character", "a", true},
		{"longest valid: 128 characters", strings.Repeat("a", 128), true},
		{"too long: 129 characters", strings.Repeat("a", 129), false},
		{"valid: interior space", "a b", true},
		{"valid: UUID", "3f1d6a4e-9c0b-4a39-8f0f-5b2d1c7e9a10", true},
		{"valid: every visible ASCII edge", "!~", true},
		{"bad pattern: a lone space", " ", false},
		{"bad pattern: leading space", " abc", false},
		{"bad pattern: trailing space", "abc ", false},
		{"bad pattern: leading tab", "\tabc", false},
		{"bad pattern: trailing tab", "8G'S\t", false},
		{"bad pattern: interior tab", "tab\there", false},
		{"bad pattern: DEL", "a\x7fb", false},
		{"bad pattern: non-ASCII", "ç", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validIdempotencyKey(tc.key); got != tc.want {
				t.Fatalf("validIdempotencyKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// TestIdempotencyKeyWire_TrailingOWSIsStrippedBeforeTheMiddleware pins the
// HTTP/1.1 fact the spec pattern rests on: RFC 9112 frames a field line as
// `field-name ":" OWS field-value OWS` and RFC 9110 section 5.5 makes the
// recipient drop that OWS, so a key sent as "8G'S<TAB>" reaches the
// middleware as "8G'S". apis/openapi.yaml therefore allows trailing SP/HTAB
// after a valid key. If Go stops stripping it, this fails and the pattern and
// validIdempotencyKey must be revisited together.
func TestIdempotencyKeyWire_TrailingOWSIsStrippedBeforeTheMiddleware(t *testing.T) {
	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get(idempotencyKeyHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	for wire, want := range map[string]string{
		"8G'S\t":  "8G'S",
		"abc ":    "abc",
		"a b \t ": "a b",
	} {
		conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		raw := "POST /x HTTP/1.1\r\nHost: test\r\nContent-Length: 0\r\nConnection: close\r\n" +
			idempotencyKeyHeader + ": " + wire + "\r\n\r\n"
		if _, err := conn.Write([]byte(raw)); err != nil {
			t.Fatalf("write: %v", err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		_ = resp.Body.Close()
		_ = conn.Close()
		if got := <-seen; got != want {
			t.Fatalf("wire value %q reached the handler as %q, want %q", wire, got, want)
		}
		if !validIdempotencyKey(want) {
			t.Fatalf("the stripped value %q must be a valid key", want)
		}
	}
}
