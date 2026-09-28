package policy

import (
	"errors"
	"testing"
)

func TestParseLLMMode(t *testing.T) {
	for raw, want := range map[string]LLMMode{"": LLMOff, "off": LLMOff, "shadow": LLMShadow, "on": LLMOn} {
		got, err := ParseLLMMode(raw)
		if err != nil || got != want {
			t.Fatalf("ParseLLMMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := ParseLLMMode("Shadow"); err == nil {
		t.Fatal("unknown/miscased mode must be rejected, never defaulted")
	}
}

func TestValidatePlan(t *testing.T) {
	cases := []struct {
		name string
		p    PlanProposal
		ok   bool
	}{
		{"hold", PlanProposal{FlowBalanceActionHold, 0, "r"}, true},
		{"assign with heads", PlanProposal{ActionAssignLabor, 3, "r"}, true},
		{"release", PlanProposal{ActionReleaseNextWork, 0, "r"}, true},
		{"unknown action", PlanProposal{"fire_everyone", 0, "r"}, false},
		{"heads on hold", PlanProposal{FlowBalanceActionHold, 2, "r"}, false},
		{"negative heads", PlanProposal{ActionAssignLabor, -1, "r"}, false},
		{"too many heads", PlanProposal{ActionAssignLabor, MaxProposedHeads + 1, "r"}, false},
		{"empty rationale", PlanProposal{FlowBalanceActionHold, 0, ""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePlan(c.p)
			if c.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !c.ok {
				if err == nil {
					t.Fatal("want invalid")
				}
				if !errors.Is(err, ErrInvalidPlan) {
					t.Fatalf("want ErrInvalidPlan, got %v", err)
				}
			}
		})
	}
}

// arbitrateDet builds the deterministic decision every Arbitrate subtest
// arbitrates over.
func arbitrateDet() Decision {
	return Decision{
		PathId:            "pick",
		RecommendedAction: FlowBalanceActionHold,
		Rationale:         "det",
		Evidence:          []FlowBalanceEvidenceEntry{{Source: "s", Detail: "d"}},
	}
}

// errArbitrateTimeout is the shared transport-error fixture.
var errArbitrateTimeout = errors.New("timeout")

func TestArbitrate(t *testing.T) {
	t.Run("off ignores everything", func(t *testing.T) {
		a := Arbitrate(arbitrateDet(), &PlanProposal{RecommendedAction: ActionAssignLabor, ProposedHeads: 2, Rationale: "llm"}, nil, LLMOff)
		if a.Source != SourceDeterministic || a.Agree != nil || a.Decision.RecommendedAction != FlowBalanceActionHold {
			t.Fatalf("unexpected %+v", a)
		}
	})
	t.Run("shadow keeps deterministic but records disagreement", func(t *testing.T) {
		assertShadowDisagreement(t, arbitrateDet(), ActionAssignLabor)
	})
	t.Run("shadow records agreement", func(t *testing.T) {
		assertShadowAgreement(t, arbitrateDet())
	})
	t.Run("on uses a valid plan, keeps evidence and pathId", func(t *testing.T) {
		assertOnAdoptsValidPlan(t, arbitrateDet())
	})
	t.Run("on falls back on transport error", func(t *testing.T) {
		assertFallback(t, Arbitrate(arbitrateDet(), nil, errArbitrateTimeout, LLMOn), "timeout")
	})
	t.Run("on falls back on invalid plan", func(t *testing.T) {
		a := Arbitrate(arbitrateDet(), &PlanProposal{RecommendedAction: "nope", Rationale: "llm"}, nil, LLMOn)
		if a.Source != SourceFallback || !errorsContains(a.Reason, "unrecognized action") {
			t.Fatalf("unexpected %+v", a)
		}
	})
	t.Run("on falls back on nil plan", func(t *testing.T) {
		if a := Arbitrate(arbitrateDet(), nil, nil, LLMOn); a.Source != SourceFallback {
			t.Fatalf("unexpected %+v", a)
		}
	})
	t.Run("shadow with error stays deterministic and reports reason", func(t *testing.T) {
		if a := Arbitrate(arbitrateDet(), nil, errArbitrateTimeout, LLMShadow); a.Source != SourceDeterministic || a.Reason == "" {
			t.Fatalf("unexpected %+v", a)
		}
	})
}

// assertShadowDisagreement checks that shadow mode returns the
// deterministic decision while recording the model's disagreement.
func assertShadowDisagreement(t *testing.T, det Decision, modelAction RecommendedAction) {
	t.Helper()
	a := Arbitrate(det, &PlanProposal{RecommendedAction: modelAction, ProposedHeads: 2, Rationale: "llm"}, nil, LLMShadow)
	if a.Source != SourceDeterministic || a.Agree == nil || *a.Agree || a.Decision.RecommendedAction != FlowBalanceActionHold {
		t.Fatalf("unexpected %+v", a)
	}
}

// assertShadowAgreement checks that shadow mode records agreement when
// the model echoes the deterministic action.
func assertShadowAgreement(t *testing.T, det Decision) {
	t.Helper()
	same := &PlanProposal{RecommendedAction: FlowBalanceActionHold, Rationale: "llm"}
	a := Arbitrate(det, same, nil, LLMShadow)
	if a.Agree == nil || !*a.Agree {
		t.Fatalf("unexpected %+v", a)
	}
}

// assertOnAdoptsValidPlan checks that on mode adopts the model's plan
// while keeping the deterministic pass's evidence and pathId.
func assertOnAdoptsValidPlan(t *testing.T, det Decision) {
	t.Helper()
	valid := &PlanProposal{RecommendedAction: ActionAssignLabor, ProposedHeads: 2, Rationale: "llm"}
	a := Arbitrate(det, valid, nil, LLMOn)
	if a.Source != SourceLLM || a.Decision.RecommendedAction != ActionAssignLabor || a.Decision.ProposedHeads != 2 || a.Decision.Rationale != "llm" {
		t.Fatalf("unexpected %+v", a)
	}
	if a.Decision.PathId != "pick" || len(a.Decision.Evidence) != 1 {
		t.Fatal("evidence/pathId must be preserved from the deterministic pass")
	}
}

// assertFallback checks that on mode fell back to the deterministic
// decision for the stated reason.
func assertFallback(t *testing.T, a Arbitration, wantReason string) {
	t.Helper()
	if a.Source != SourceFallback || a.Reason != wantReason || a.Decision.RecommendedAction != FlowBalanceActionHold {
		t.Fatalf("unexpected %+v", a)
	}
}

func errorsContains(s, sub string) bool { return len(s) >= len(sub) && contains(s, sub) }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
