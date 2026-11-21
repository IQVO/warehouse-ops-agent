//go:build integration

package http_test

import (
	"context"
	nethttp "net/http"
	"sync"
	"testing"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
)

// The repo-specific half of the idempotency replay-class test (the verbatim
// template is idempotency_replay_classes_test.go; skeleton:
// warehouse-harness-template templates/idempotency/replay_classes_wiring_test.go.tmpl).
//
// This service has NO database: the act slice's Idempotency-Key store is the
// process-local memory store behind ports.IdempotencyStore (ADR 0030). The
// wiring therefore names no driver and boots no container:
//
//   - Middleware: the production middleware over the real memory store.
//   - Write/Committed: what the template's Write stands for — the aggregate
//     rows a command commits — is, on the act slice, the use case's
//     in-process commit (the approval nonce consumption): it happens
//     immediately when the command runs, is never bound to a request
//     transaction and is never rolled back by the middleware. The probe
//     records writes the same way and Committed reads them from outside the
//     request.
//   - KeyRows: the store itself — whether it still holds the key (0 after a
//     release, when nothing was stored under it).
func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	store := newIdempotencyStore(t)
	var mu sync.Mutex
	writes := map[string]int{}
	return replayOps{
		Catalogue: func() []problemEntry {
			out := []problemEntry{}
			for _, e := range inboundhttp.ReplayCatalogue() {
				out = append(out, problemEntry{Slug: e.Slug, Status: e.Status, Class: e.Class})
			}
			return out
		},
		Middleware: func(_ *testing.T, next nethttp.Handler) nethttp.Handler {
			return inboundhttp.RequireIdempotencyKey(store)(next)
		},
		Refuse: func(w nethttp.ResponseWriter, r *nethttp.Request, e problemEntry) {
			inboundhttp.RefuseProblem(w, r, e.Slug)
		},
		Write: func(_ context.Context, tag string) error {
			// No unit of work exists to join (no database): like the nonce
			// consumption it stands for, the write commits as it happens.
			mu.Lock()
			writes[tag]++
			mu.Unlock()
			return nil
		},
		Committed: func(t *testing.T, tag string) int {
			t.Helper()
			mu.Lock()
			defer mu.Unlock()
			return writes[tag]
		},
		KeyRows: func(t *testing.T, key string) int {
			t.Helper()
			if store.Held(key) {
				return 1
			}
			return 0
		},
	}
}
