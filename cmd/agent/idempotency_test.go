package main

import (
	"context"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The composition root's key store honours the fleet guideline's minimum
// retention and replays a stored answer (ADR 0030).
func TestNewIdempotencyStore_ReplaysAndKeepsTheGuidelineRetention(t *testing.T) {
	if idempotencyRetention.Hours() < 24 {
		t.Fatalf("retention %s is below the fleet guideline's 24 h minimum", idempotencyRetention)
	}
	s, err := newIdempotencyStore()
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	run := func(context.Context) (ports.StoredResponse, error) {
		runs++
		return ports.StoredResponse{Status: 200, Body: []byte("ok")}, nil
	}
	if _, replayed, err := s.Execute(context.Background(), "k", "fp", run); err != nil || replayed {
		t.Fatalf("first: replayed=%v err=%v", replayed, err)
	}
	if _, replayed, err := s.Execute(context.Background(), "k", "fp", run); err != nil || !replayed || runs != 1 {
		t.Fatalf("second: replayed=%v err=%v runs=%d, want a replay", replayed, err, runs)
	}
}
