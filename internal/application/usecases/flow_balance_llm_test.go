package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeReasoner struct {
	plan  ports.Plan
	err   error
	brief ports.Brief
	calls int
}

func (f *fakeReasoner) Reason(_ context.Context, b ports.Brief) (ports.Plan, error) {
	f.calls++
	f.brief = b
	return f.plan, f.err
}

type fakeMetrics struct {
	useCase, mode, source string
	agree                 *bool
	calls                 int
}

func (m *fakeMetrics) RecordArbitration(_ context.Context, useCase, mode, source string, agree *bool) {
	m.calls++
	m.useCase, m.mode, m.source, m.agree = useCase, mode, source, agree
}

// healthy wires the three upstream fakes so the deterministic path yields
// assign_labor with 4 heads (same fixture as TestFlowBalanceAdvisory_Execute).
func healthy() (*fbFakeWes, *fbFakeWFM, *fbFakeFE) {
	return &fbFakeWes{recommendation: ports.RebalanceRecommendation{PathId: "pick", Action: "ReassignLabor", BacklogDepth: 120, WIP: 40}},
		&fbFakeWFM{gap: ports.StaffingGap{PathId: "pick", PlannedHeads: 10, ActiveHeads: 6, Understaffed: true}},
		&fbFakeFE{result: ports.StuckTasksResult{Count: 0}}
}

func TestFlowBalanceAdvisory_LLMArbitration(t *testing.T) {
	t.Run("mode off never calls the reasoner", func(t *testing.T) {
		wes, wfm, fe := healthy()
		r := &fakeReasoner{plan: ports.Plan{RecommendedAction: "hold", Rationale: "x"}}
		uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, Reasoner: r, LLMMode: policy.LLMOff}
		got, _ := uc.Execute(context.Background(), "b", "s", "pick")
		if r.calls != 0 || got.RecommendedAction != policy.ActionAssignLabor {
			t.Fatalf("calls=%d action=%s", r.calls, got.RecommendedAction)
		}
		if got.Source != policy.SourceDeterministic {
			t.Fatalf("Source = %q, want deterministic", got.Source)
		}
	})
	t.Run("zero-value mode with a reasoner wired is still off", func(t *testing.T) {
		assertZeroModeIsOff(t)
	})
	t.Run("shadow: reasoner consulted, deterministic returned, disagreement recorded", func(t *testing.T) {
		assertShadowArbitration(t)
	})
	t.Run("shadow: absent signals are stated as unavailable in the brief", func(t *testing.T) {
		assertAbsentSignalsStatedUnavailable(t)
	})
	t.Run("on: valid plan replaces action/heads/rationale, keeps evidence", func(t *testing.T) {
		assertOnAdoptsValidLLMPlan(t)
	})
	t.Run("on: reasoner error falls back to deterministic", func(t *testing.T) {
		assertOnFallsBackOnReasonerError(t)
	})
	t.Run("on: out-of-vocabulary plan falls back", func(t *testing.T) {
		assertOnFallsBackOnInvalidLLMPlan(t)
	})
}

// assertZeroModeIsOff checks that an unwired LLMMode (the zero value)
// behaves as off even with a reasoner present.
func assertZeroModeIsOff(t *testing.T) {
	t.Helper()
	wes, wfm, fe := healthy()
	r := &fakeReasoner{}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, Reasoner: r}
	_, _ = uc.Execute(context.Background(), "b", "s", "pick")
	if r.calls != 0 {
		t.Fatal("zero LLMMode must behave as off")
	}
}

// assertShadowArbitration checks the full shadow-mode contract: the
// deterministic decision is returned unchanged, the reasoner is consulted
// exactly once, the disagreement is recorded via metrics, and the brief
// carries the vocabulary, the facts by source, and the tools.
func assertShadowArbitration(t *testing.T) {
	t.Helper()
	wes, wfm, fe := healthy()
	r := &fakeReasoner{plan: ports.Plan{RecommendedAction: "hold", Rationale: "model says hold", Model: "m1"}}
	m := &fakeMetrics{}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, Reasoner: r, LLMMode: policy.LLMShadow, Metrics: m,
		ReasonerTools: []ports.ToolSpec{{Upstream: "workforce-management", Name: "get_staffing_gap"}}}
	got, err := uc.Execute(context.Background(), "b", "s", "pick")
	if err != nil {
		t.Fatal(err)
	}
	if got.RecommendedAction != policy.ActionAssignLabor || got.ProposedHeads != 4 || got.Rationale == "model says hold" {
		t.Fatalf("shadow must return the deterministic decision, got %+v", got)
	}
	if got.Source != policy.SourceDeterministic {
		t.Fatalf("Source = %q, want deterministic", got.Source)
	}
	if r.calls != 1 {
		t.Fatalf("reasoner calls = %d", r.calls)
	}
	if m.calls != 1 || m.useCase != "flow_balance_advisory" || m.mode != "shadow" || m.source != "deterministic" || m.agree == nil || *m.agree {
		t.Fatalf("metrics %+v", m)
	}
	assertShadowBrief(t, r)
}

// assertShadowBrief checks the brief half of the shadow-mode contract:
// the vocabulary, the facts by source, and the tools.
func assertShadowBrief(t *testing.T, r *fakeReasoner) {
	t.Helper()
	b := r.brief
	if b.UseCase != "flow_balance_advisory" || len(b.AllowedActions) != 3 || len(b.Tools) != 1 {
		t.Fatalf("brief %+v", b)
	}
	if _, ok := b.Facts["wes-work-planning.get_rebalance_recommendation"]; !ok {
		t.Fatalf("facts must be keyed by source: %v", b.Facts)
	}
}

// assertAbsentSignalsStatedUnavailable checks that a failed upstream read
// is stated as "unavailable" in the brief rather than omitted.
func assertAbsentSignalsStatedUnavailable(t *testing.T) {
	t.Helper()
	wes, _, fe := healthy()
	r := &fakeReasoner{plan: ports.Plan{RecommendedAction: "hold", Rationale: "x"}}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: &fbFakeWFM{err: errors.New("down")}, FE: fe, Reasoner: r, LLMMode: policy.LLMShadow}
	_, _ = uc.Execute(context.Background(), "b", "s", "pick")
	if r.brief.Facts["workforce-management.get_staffing_gap"] != "unavailable" {
		t.Fatalf("facts %v", r.brief.Facts)
	}
}

// assertOnAdoptsValidLLMPlan checks on mode with a valid plan: the
// model's action/heads/rationale replace the deterministic ones, evidence
// is kept, and agreement is recorded.
func assertOnAdoptsValidLLMPlan(t *testing.T) {
	t.Helper()
	wes, wfm, fe := healthy()
	r := &fakeReasoner{plan: ports.Plan{RecommendedAction: "assign_labor", ProposedHeads: 2, Rationale: "two is enough"}}
	m := &fakeMetrics{}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, Reasoner: r, LLMMode: policy.LLMOn, Metrics: m}
	got, _ := uc.Execute(context.Background(), "b", "s", "pick")
	if got.RecommendedAction != policy.ActionAssignLabor || got.ProposedHeads != 2 || got.Rationale != "two is enough" || len(got.Evidence) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got.Source != policy.SourceLLM {
		t.Fatalf("Source = %q, want llm", got.Source)
	}
	if m.source != "llm" || m.agree == nil || !*m.agree {
		t.Fatalf("metrics %+v", m)
	}
}

// assertOnFallsBackOnReasonerError checks that a transport-level reasoner
// failure falls back to the deterministic decision.
func assertOnFallsBackOnReasonerError(t *testing.T) {
	t.Helper()
	wes, wfm, fe := healthy()
	r := &fakeReasoner{err: errors.New("anthropic: status 529")}
	m := &fakeMetrics{}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, Reasoner: r, LLMMode: policy.LLMOn, Metrics: m}
	got, err := uc.Execute(context.Background(), "b", "s", "pick")
	if err != nil || got.RecommendedAction != policy.ActionAssignLabor || got.ProposedHeads != 4 {
		t.Fatalf("fallback must be the deterministic decision, got %+v err=%v", got, err)
	}
	if got.Source != policy.SourceFallback {
		t.Fatalf("Source = %q, want fallback", got.Source)
	}
	if m.source != "fallback" || m.agree != nil {
		t.Fatalf("metrics %+v", m)
	}
}

// assertOnFallsBackOnInvalidLLMPlan checks that an out-of-vocabulary plan
// falls back to the deterministic decision.
func assertOnFallsBackOnInvalidLLMPlan(t *testing.T) {
	t.Helper()
	wes, wfm, fe := healthy()
	r := &fakeReasoner{plan: ports.Plan{RecommendedAction: "shut_down_the_warehouse", Rationale: "x"}}
	m := &fakeMetrics{}
	uc := &usecases.FlowBalanceAdvisory{Wes: wes, WFM: wfm, FE: fe, Reasoner: r, LLMMode: policy.LLMOn, Metrics: m}
	got, _ := uc.Execute(context.Background(), "b", "s", "pick")
	if got.RecommendedAction != policy.ActionAssignLabor || m.source != "fallback" {
		t.Fatalf("got %+v metrics %+v", got, m)
	}
}
