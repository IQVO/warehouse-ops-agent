package policy_test

import (
	"strings"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

func floatPtr(f float64) *float64 { return &f }

// utilizationSignal builds a labor-performance get_task_type_utilization
// fixture.
func utilizationSignal(taskType string, taskSeconds, idleSeconds int64, pct *float64) *policy.UtilizationSignal {
	return &policy.UtilizationSignal{
		Source:         "labor-performance.get_task_type_utilization",
		TaskType:       taskType,
		WindowSeconds:  3600,
		TaskSeconds:    taskSeconds,
		IdleSeconds:    idleSeconds,
		UtilizationPct: pct,
	}
}

func TestCorrelateUtilization(t *testing.T) {
	t.Run("queue depth HIGH + idle share HIGH => claim/flow problem, points at FE diagnostics", func(t *testing.T) {
		// 40% utilization, idle share = 1200/(800+1200) = 60%
		assertClaimFlowProblem(t, utilizationSignal("PICK", 800, 1200, floatPtr(40.0)))
	})
	t.Run("queue depth LOW + idle share HIGH => starvation, WES-facing advisory text only", func(t *testing.T) {
		assertStarvationAdvisory(t, utilizationSignal("PACK", 800, 1200, floatPtr(40.0)))
	})
	t.Run("queue depth HIGH + idle share LOW => staffing gap confirmed, corroborated by utilization", func(t *testing.T) {
		// 90% utilization, idle share = 100/(900+100) = 10%
		assertStaffingGapConfirmed(t, utilizationSignal("SLAM", 900, 100, floatPtr(90.0)))
	})
	t.Run("nil utilization signal => nil correlation, never a crash", func(t *testing.T) {
		assertNilUtilizationCorrelation(t, "nil signal", 120, nil)
	})
	t.Run("nil UtilizationPct (nothing observed) => nil correlation, never treated as 0%", func(t *testing.T) {
		assertNilUtilizationCorrelation(t, "nil UtilizationPct", 120, utilizationSignal("PICK", 0, 0, nil))
	})
	t.Run("shallow queue + healthy utilization => no correlation, ordinary noise", func(t *testing.T) {
		assertNilUtilizationCorrelation(t, "shallow queue", 5, utilizationSignal("PICK", 900, 100, floatPtr(90.0)))
	})
	t.Run("low utilizationPct but idle share does not corroborate => no correlation", func(t *testing.T) {
		// utilizationPct 55% is "low" (<60) but idle share is only 45/(900+45)=~5%,
		// far under the 40% idle-share threshold -- the two metrics disagree.
		assertNilUtilizationCorrelation(t, "uncorroborated idle share", 120, utilizationSignal("PICK", 900, 45, floatPtr(55.0)))
	})
}

// assertClaimFlowProblem checks the deep-queue + high-idle-share branch:
// the advisory names a claim/flow problem and points at
// fulfillment-execution's diagnostics rather than staffing.
func assertClaimFlowProblem(t *testing.T, sig *policy.UtilizationSignal) {
	t.Helper()
	got := policy.CorrelateUtilization(120, sig)
	if got == nil {
		t.Fatal("expected a correlation, got nil")
	}
	if got.Kind != policy.UtilizationCorrelationClaimFlowProblem {
		t.Errorf("Kind = %q, want %q", got.Kind, policy.UtilizationCorrelationClaimFlowProblem)
	}
	if !strings.Contains(got.Rationale, "claim/flow problem") || !strings.Contains(got.Rationale, "diagnose_stuck_tasks") {
		t.Errorf("rationale should point at FE diagnostics, not staffing: %q", got.Rationale)
	}
}

// assertStarvationAdvisory checks the shallow-queue + high-idle-share
// branch: a WES-facing release-pacing advisory that is explicitly
// advisory-only.
func assertStarvationAdvisory(t *testing.T, sig *policy.UtilizationSignal) {
	t.Helper()
	got := policy.CorrelateUtilization(5, sig)
	if got == nil {
		t.Fatal("expected a correlation, got nil")
	}
	if got.Kind != policy.UtilizationCorrelationStarvation {
		t.Errorf("Kind = %q, want %q", got.Kind, policy.UtilizationCorrelationStarvation)
	}
	if !strings.Contains(got.Rationale, "release pacing") {
		t.Errorf("rationale should recommend WES release pacing: %q", got.Rationale)
	}
	if !strings.Contains(got.Rationale, "does not trigger a WES action") {
		t.Errorf("rationale must state this is advisory-only, no WES auto-trigger: %q", got.Rationale)
	}
}

// assertStaffingGapConfirmed checks the deep-queue + low-idle-share
// branch: the staffing gap is confirmed and the rationale cites the
// observed utilization percent.
func assertStaffingGapConfirmed(t *testing.T, sig *policy.UtilizationSignal) {
	t.Helper()
	got := policy.CorrelateUtilization(120, sig)
	if got == nil {
		t.Fatal("expected a correlation, got nil")
	}
	if got.Kind != policy.UtilizationCorrelationStaffingGapConfirmed {
		t.Errorf("Kind = %q, want %q", got.Kind, policy.UtilizationCorrelationStaffingGapConfirmed)
	}
	if !strings.Contains(got.Rationale, "staffing gap confirmed") {
		t.Errorf("rationale should explicitly say staffing gap confirmed: %q", got.Rationale)
	}
	if !strings.Contains(got.Rationale, "90.0%") {
		t.Errorf("rationale should cite the observed utilization percent: %q", got.Rationale)
	}
}

// assertNilUtilizationCorrelation checks one of the no-correlation
// degrade paths: whatever the reason, the result is nil, never a crash or
// a coerced advisory.
func assertNilUtilizationCorrelation(t *testing.T, reason string, queueDepth int, sig *policy.UtilizationSignal) {
	t.Helper()
	if got := policy.CorrelateUtilization(queueDepth, sig); got != nil {
		t.Errorf("%s: expected nil correlation, got %+v", reason, got)
	}
}
