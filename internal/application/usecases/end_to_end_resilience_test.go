// end_to_end_resilience_test.go proves ADR-0011 end to end at the use-
// case level (not just inside the anthropic package): wiring the REAL
// breaker-wrapped anthropic.Reasoner (against a scripted always-failing
// httptest server, no live Anthropic API call) into
// usecases.FlowBalanceAdvisory in LLMOn mode, and asserting that once the
// breaker trips OPEN, Execute returns EXACTLY the same deterministic
// decision the EXISTING fallback mechanism already produces when the
// Reasoner is simply unavailable (mirrors flow_balance_llm_test.go's "on:
// reasoner error falls back to deterministic" case) — no new fallback
// behavior, no new output shape.
package usecases_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/llm/anthropic"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

func TestFlowBalanceAdvisory_BreakerOpen_FallsBackToDeterministic(t *testing.T) {
	// Always-failing Anthropic Messages API stand-in: every request 500s,
	// so the breaker trips after resilience.ReadyToTrip's 5 consecutive
	// failures (each Reason call already retried up to 3x internally).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`))
	}))
	defer srv.Close()

	reasoner, err := anthropic.New(anthropic.Config{
		APIKey:  "k",
		BaseURL: srv.URL,
		Timeout: 2 * time.Second,
	}, noopInvoker{})
	if err != nil {
		t.Fatal(err)
	}

	newUC := func() *usecases.FlowBalanceAdvisory {
		wes, wfm, fe := healthy()
		return &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, Reasoner: reasoner, LLMMode: policy.LLMOn}
	}

	// Deterministic reference: what the use case produces with NO
	// reasoner at all (LLMOff) — this is the "existing deterministic
	// fallback" output the breaker-OPEN path must match exactly.
	wes, wfm, fe := healthy()
	reference := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, LLMMode: policy.LLMOff}
	wantDecision, err := reference.Execute(context.Background(), "b", "s", "pick")
	if err != nil {
		t.Fatal(err)
	}

	// Drive enough failing calls to trip the breaker (5, per
	// resilience.ReadyToTrip's ConsecutiveFailures>=5 leg). Each of
	// these must ALREADY fall back to the deterministic decision (the
	// Reasoner erroring is not yet a breaker rejection, just an
	// ordinary Reason failure) -- Arbitrate treats any Reasoner error
	// identically.
	for i := 0; i < 5; i++ {
		got, err := newUC().Execute(context.Background(), "b", "s", "pick")
		if err != nil {
			t.Fatalf("call %d: Execute returned an error, want a fallback decision: %v", i, err)
		}
		if got.RecommendedAction != wantDecision.RecommendedAction || got.ProposedHeads != wantDecision.ProposedHeads {
			t.Fatalf("call %d: got %+v, want the deterministic decision %+v", i, got, wantDecision)
		}
	}

	// Now the breaker is OPEN: the next call short-circuits before ever
	// reaching the server, and must STILL produce the identical
	// deterministic decision -- proving breaker-OPEN routes through the
	// SAME existing fallback mechanism, not a new one.
	got, err := newUC().Execute(context.Background(), "b", "s", "pick")
	if err != nil {
		t.Fatalf("Execute while breaker OPEN returned an error, want a fallback decision: %v", err)
	}
	if got.RecommendedAction != wantDecision.RecommendedAction || got.ProposedHeads != wantDecision.ProposedHeads || got.Rationale != wantDecision.Rationale {
		t.Fatalf("breaker-OPEN decision = %+v, want the identical deterministic decision %+v", got, wantDecision)
	}
}

type noopInvoker struct{}

func (noopInvoker) Invoke(context.Context, string, string, map[string]any) (string, error) {
	return "", nil
}
