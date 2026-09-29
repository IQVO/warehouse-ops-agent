package policy_test

import (
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

func TestParseRebalanceAction(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    policy.RebalanceAction
		wantErr bool
	}{
		{name: "NoActionNeeded", raw: "NoActionNeeded", want: policy.RebalanceNoActionNeeded},
		{name: "ThrottleUpstream", raw: "ThrottleUpstream", want: policy.RebalanceThrottleUpstream},
		{name: "ReassignLabor", raw: "ReassignLabor", want: policy.RebalanceReassignLabor},
		{name: "empty string rejected", raw: "", wantErr: true},
		{name: "unknown enum rejected, never defaulted", raw: "DoSomethingUnexpected", wantErr: true},
		{name: "case-sensitive: lowercase rejected", raw: "reassignlabor", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := policy.ParseRebalanceAction(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRebalanceAction(%q): expected error, got %q", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRebalanceAction(%q): unexpected error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ParseRebalanceAction(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// wesSignal, wfmSignal, feSignal build the three upstream signal fixtures
// shared by the Decide table below.
func wesSignal(action policy.RebalanceAction, backlog, wip int) *policy.RebalanceSignal {
	return &policy.RebalanceSignal{
		Source:       "wes-work-planning.get_rebalance_recommendation",
		PathId:       "pick-a",
		Action:       action,
		BacklogDepth: backlog,
		WIP:          wip,
	}
}

func wfmSignal(planned, active int, understaffed bool) *policy.StaffingSignal {
	return &policy.StaffingSignal{
		Source:       "workforce-management.get_staffing_gap",
		PathId:       "pick-a",
		PlannedHeads: planned,
		ActiveHeads:  active,
		Understaffed: understaffed,
	}
}

func feSignal(count int, reasons ...string) *policy.StuckTasksSignal {
	return &policy.StuckTasksSignal{
		Source:  "fulfillment-execution.diagnose_stuck_tasks",
		Count:   count,
		Reasons: reasons,
	}
}

// decideCase is one row of the Decide correlation table.
type decideCase struct {
	name string
	wes  *policy.RebalanceSignal
	wfm  *policy.StaffingSignal
	fe   *policy.StuckTasksSignal

	wantAction   policy.RecommendedAction
	wantHeads    int
	wantPartial  bool
	wantMissing  []string
	wantEvidence int // number of evidence entries expected
}

func TestDecide(t *testing.T) {
	tests := []decideCase{
		{
			name:         "ReassignLabor + understaffed => assign_labor with the head gap",
			wes:          wesSignal(policy.RebalanceReassignLabor, 120, 40),
			wfm:          wfmSignal(10, 6, true),
			fe:           feSignal(0),
			wantAction:   policy.ActionAssignLabor,
			wantHeads:    4,
			wantEvidence: 3,
		},
		{
			name:         "ThrottleUpstream + understaffed => assign_labor too (same lever)",
			wes:          wesSignal(policy.RebalanceThrottleUpstream, 340, 0),
			wfm:          wfmSignal(8, 5, true),
			fe:           feSignal(0),
			wantAction:   policy.ActionAssignLabor,
			wantHeads:    3,
			wantEvidence: 3,
		},
		{
			name:         "gap floored at 1 head when planned<=active but flagged understaffed",
			wes:          wesSignal(policy.RebalanceReassignLabor, 50, 20),
			wfm:          wfmSignal(5, 5, true),
			fe:           feSignal(0),
			wantAction:   policy.ActionAssignLabor,
			wantHeads:    1,
			wantEvidence: 3,
		},
		{
			name:         "ReassignLabor + fully staffed + stuck tasks => hold, blocked claims not labor gap",
			wes:          wesSignal(policy.RebalanceReassignLabor, 90, 30),
			wfm:          wfmSignal(10, 10, false),
			fe:           feSignal(3, "lease-expired", "lease-expired", "capability-mismatch"),
			wantAction:   policy.FlowBalanceActionHold,
			wantEvidence: 3,
		},
		{
			name:         "ReassignLabor + fully staffed + no stuck tasks => hold, unsupported by any evidence",
			wes:          wesSignal(policy.RebalanceReassignLabor, 90, 30),
			wfm:          wfmSignal(10, 10, false),
			fe:           feSignal(0),
			wantAction:   policy.FlowBalanceActionHold,
			wantEvidence: 3,
		},
		{
			name:         "NoActionNeeded + no stuck tasks + fully staffed => release_next_work",
			wes:          wesSignal(policy.RebalanceNoActionNeeded, 5, 3),
			wfm:          wfmSignal(4, 4, false),
			fe:           feSignal(0),
			wantAction:   policy.ActionReleaseNextWork,
			wantEvidence: 3,
		},
		{
			name:         "NoActionNeeded + stuck tasks present => hold despite healthy backlog",
			wes:          wesSignal(policy.RebalanceNoActionNeeded, 5, 3),
			wfm:          wfmSignal(4, 4, false),
			fe:           feSignal(1, "lease-expired"),
			wantAction:   policy.FlowBalanceActionHold,
			wantEvidence: 3,
		},
		{
			name:         "NoActionNeeded + no stuck tasks + understaffed => hold, gap surfaced not acted on",
			wes:          wesSignal(policy.RebalanceNoActionNeeded, 5, 3),
			wfm:          wfmSignal(6, 3, true),
			fe:           feSignal(0),
			wantAction:   policy.FlowBalanceActionHold,
			wantEvidence: 3,
		},
		{
			name:         "wes missing => partial hold, wes is the anchor signal",
			wes:          nil,
			wfm:          wfmSignal(6, 3, true),
			fe:           feSignal(0),
			wantAction:   policy.FlowBalanceActionHold,
			wantPartial:  true,
			wantMissing:  []string{"wes-work-planning.get_rebalance_recommendation"},
			wantEvidence: 2,
		},
		{
			name:         "ReassignLabor + wfm missing => partial hold, cannot confirm labor gap",
			wes:          wesSignal(policy.RebalanceReassignLabor, 90, 30),
			wfm:          nil,
			fe:           feSignal(0),
			wantAction:   policy.FlowBalanceActionHold,
			wantPartial:  true,
			wantMissing:  []string{"workforce-management.get_staffing_gap"},
			wantEvidence: 2,
		},
		{
			name:         "ReassignLabor + fully staffed + fe missing => partial hold",
			wes:          wesSignal(policy.RebalanceReassignLabor, 90, 30),
			wfm:          wfmSignal(10, 10, false),
			fe:           nil,
			wantAction:   policy.FlowBalanceActionHold,
			wantPartial:  true,
			wantMissing:  []string{"fulfillment-execution.diagnose_stuck_tasks"},
			wantEvidence: 2,
		},
		{
			name:         "NoActionNeeded + fe missing => partial hold, cannot assume healthy",
			wes:          wesSignal(policy.RebalanceNoActionNeeded, 5, 3),
			wfm:          wfmSignal(4, 4, false),
			fe:           nil,
			wantAction:   policy.FlowBalanceActionHold,
			wantPartial:  true,
			wantMissing:  []string{"fulfillment-execution.diagnose_stuck_tasks"},
			wantEvidence: 2,
		},
		{
			name:         "NoActionNeeded + no stuck tasks + wfm missing => partial hold",
			wes:          wesSignal(policy.RebalanceNoActionNeeded, 5, 3),
			wfm:          nil,
			fe:           feSignal(0),
			wantAction:   policy.FlowBalanceActionHold,
			wantPartial:  true,
			wantMissing:  []string{"workforce-management.get_staffing_gap"},
			wantEvidence: 2,
		},
		{
			name:         "only wes available, everything else missing => partial hold",
			wes:          wesSignal(policy.RebalanceNoActionNeeded, 5, 3),
			wfm:          nil,
			fe:           nil,
			wantAction:   policy.FlowBalanceActionHold,
			wantPartial:  true,
			wantMissing:  []string{"workforce-management.get_staffing_gap", "fulfillment-execution.diagnose_stuck_tasks"},
			wantEvidence: 1,
		},
		{
			name:        "everything missing => partial hold, no evidence",
			wes:         nil,
			wfm:         nil,
			fe:          nil,
			wantAction:  policy.FlowBalanceActionHold,
			wantPartial: true,
			wantMissing: []string{
				"wes-work-planning.get_rebalance_recommendation",
				"workforce-management.get_staffing_gap",
				"fulfillment-execution.diagnose_stuck_tasks",
			},
			wantEvidence: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertDecideCase(t, tc)
		})
	}
}

// assertDecideCase runs one Decide table row and asserts the decision's
// full shape: action, heads, partial flag, rationale, evidence count,
// missing-signal list, and pathId.
func assertDecideCase(t *testing.T, tc decideCase) {
	t.Helper()
	got := policy.Decide("pick-a", tc.wes, tc.wfm, tc.fe)

	if got.RecommendedAction != tc.wantAction {
		t.Errorf("RecommendedAction = %q, want %q (rationale: %s)", got.RecommendedAction, tc.wantAction, got.Rationale)
	}
	if tc.wantAction == policy.ActionAssignLabor && got.ProposedHeads != tc.wantHeads {
		t.Errorf("ProposedHeads = %d, want %d", got.ProposedHeads, tc.wantHeads)
	}
	if got.Partial != tc.wantPartial {
		t.Errorf("Partial = %t, want %t", got.Partial, tc.wantPartial)
	}
	if got.Rationale == "" {
		t.Error("Rationale must never be empty — every decision needs a human-readable reason")
	}
	if len(got.Evidence) != tc.wantEvidence {
		t.Errorf("len(Evidence) = %d, want %d (%+v)", len(got.Evidence), tc.wantEvidence, got.Evidence)
	}
	if tc.wantMissing != nil {
		assertMissingSignals(t, got.MissingSignals, tc.wantMissing)
	}
	if got.PathId != "pick-a" {
		t.Errorf("PathId = %q, want %q", got.PathId, "pick-a")
	}
}

// assertMissingSignals compares a decision's MissingSignals against the
// expected list, element by element and in order.
func assertMissingSignals(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("MissingSignals = %v, want %v", got, want)
	}
	for i, s := range want {
		if got[i] != s {
			t.Errorf("MissingSignals[%d] = %q, want %q", i, got[i], s)
		}
	}
}

// TestDecide_EveryDecisionCarriesEvidenceWhenSignalsPresent enforces the
// "every decision MUST carry its evidence trail" guardrail directly: whenever
// a signal was supplied, its source must appear in the evidence trail.
func TestDecide_EveryDecisionCarriesEvidenceWhenSignalsPresent(t *testing.T) {
	wesSignal := &policy.RebalanceSignal{Source: "wes-work-planning.get_rebalance_recommendation", PathId: "pick-a", Action: policy.RebalanceNoActionNeeded}
	wfmSignal := &policy.StaffingSignal{Source: "workforce-management.get_staffing_gap", PathId: "pick-a"}
	feSignal := &policy.StuckTasksSignal{Source: "fulfillment-execution.diagnose_stuck_tasks"}

	got := policy.Decide("pick-a", wesSignal, wfmSignal, feSignal)

	sources := map[string]bool{}
	for _, e := range got.Evidence {
		sources[e.Source] = true
	}
	for _, want := range []string{wesSignal.Source, wfmSignal.Source, feSignal.Source} {
		if !sources[want] {
			t.Errorf("evidence trail missing source %q: %+v", want, got.Evidence)
		}
	}
}
