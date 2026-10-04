package policy

import (
	"strings"
	"testing"
	"time"
)

var (
	outlookStart = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	outlookEnd   = outlookStart.Add(8 * time.Hour)
)

func healthyPathCapacity() PathCapacityFact {
	return PathCapacityFact{
		NormalizedRate: 120,
		NormalizedUnit: "ORDER",
		BottleneckStep: "PACK",
		Steps: []CapacityStepFact{
			{Step: "PICK", NormalizedRate: 300, BindingConstraint: "LABOR"},
			{Step: "PACK", NormalizedRate: 120, BindingConstraint: "STATION"},
		},
		Warnings: []string{"stations tallied without a declared standard: REBIN"},
	}
}

func TestSummarizeCapacityOutlook_ReportsRateBottleneckAndBindingConstraint(t *testing.T) {
	o := SummarizeCapacityOutlook("tote-path", outlookStart, outlookEnd, healthyPathCapacity())

	if o.OmittedReason != "" {
		t.Fatalf("a valid reading must not be omitted, got %q", o.OmittedReason)
	}
	if o.PlanningPathId != "tote-path" || !o.WindowStart.Equal(outlookStart) || !o.WindowEnd.Equal(outlookEnd) {
		t.Errorf("identity/window not carried through: %+v", o)
	}
	if o.NormalizedRate != 120 || o.BottleneckStep != "PACK" {
		t.Errorf("rate/bottleneck = %v/%q, want 120/PACK", o.NormalizedRate, o.BottleneckStep)
	}
	if o.BindingConstraint != "STATION" {
		t.Errorf("binding constraint = %q, want the BOTTLENECK step's (STATION), not the first step's", o.BindingConstraint)
	}
	if len(o.Steps) != 2 || len(o.Warnings) != 1 || !strings.Contains(o.Warnings[0], "REBIN") {
		t.Errorf("steps/warnings not carried through unchanged: %+v", o)
	}
}

func TestSummarizeCapacityOutlook_RejectsNonOrderUnit(t *testing.T) {
	fact := healthyPathCapacity()
	fact.NormalizedUnit = "UNIT"

	o := SummarizeCapacityOutlook("tote-path", outlookStart, outlookEnd, fact)

	if o.OmittedReason == "" || !strings.Contains(o.OmittedReason, "UNIT") {
		t.Fatalf("a non-ORDER unit must be rejected with a reason naming it, got %q", o.OmittedReason)
	}
	if o.NormalizedRate != 0 || o.BottleneckStep != "" || len(o.Steps) != 0 {
		t.Errorf("a rejected reading must not leak capacity figures: %+v", o)
	}
}

func TestSummarizeCapacityOutlook_RejectsEmptyUnit(t *testing.T) {
	fact := healthyPathCapacity()
	fact.NormalizedUnit = ""
	if o := SummarizeCapacityOutlook("p", outlookStart, outlookEnd, fact); o.OmittedReason == "" {
		t.Fatal("a missing unit must be rejected, never assumed to be ORDER")
	}
}

func TestSummarizeCapacityOutlook_RejectsNegativeRate(t *testing.T) {
	fact := healthyPathCapacity()
	fact.NormalizedRate = -1

	o := SummarizeCapacityOutlook("tote-path", outlookStart, outlookEnd, fact)

	if o.OmittedReason == "" || !strings.Contains(o.OmittedReason, "negative") {
		t.Fatalf("a negative rate must be rejected, not clamped: %+v", o)
	}
}

func TestSummarizeCapacityOutlook_ZeroRateIsAValidReading(t *testing.T) {
	fact := healthyPathCapacity()
	fact.NormalizedRate = 0

	o := SummarizeCapacityOutlook("tote-path", outlookStart, outlookEnd, fact)

	if o.OmittedReason != "" {
		t.Fatalf("a zero rate is a real (alarming) capacity, not an omission: %q", o.OmittedReason)
	}
}

func TestSummarizeCapacityOutlook_BottleneckMissingFromBreakdown_WarnsInsteadOfGuessing(t *testing.T) {
	fact := healthyPathCapacity()
	fact.BottleneckStep = "SLAM"

	o := SummarizeCapacityOutlook("tote-path", outlookStart, outlookEnd, fact)

	if o.OmittedReason != "" {
		t.Fatalf("the reading is still usable: %q", o.OmittedReason)
	}
	if o.BindingConstraint != "" {
		t.Errorf("binding constraint must stay empty, got %q", o.BindingConstraint)
	}
	if len(o.Warnings) != 2 || !strings.Contains(o.Warnings[1], "SLAM") {
		t.Errorf("expected planning's warning plus one naming the unknown bottleneck, got %v", o.Warnings)
	}
}

func TestSummarizeCapacityOutlook_DoesNotAliasInputWarnings(t *testing.T) {
	fact := healthyPathCapacity()
	fact.BottleneckStep = "SLAM"
	fact.Warnings = make([]string, 1, 4) // spare capacity: append would write into it.
	fact.Warnings[0] = "w"

	_ = SummarizeCapacityOutlook("p", outlookStart, outlookEnd, fact)

	if got := fact.Warnings[:2]; got[1] != "" {
		t.Fatalf("the input warnings' backing array was mutated: %v", got)
	}
}

func TestOmittedCapacityOutlook_CarriesOnlyIdentityWindowAndReason(t *testing.T) {
	o := OmittedCapacityOutlook("tote-path", outlookStart, outlookEnd, "why")

	if o.OmittedReason != "why" || o.PlanningPathId != "tote-path" || !o.WindowStart.Equal(outlookStart) || !o.WindowEnd.Equal(outlookEnd) {
		t.Fatalf("unexpected omitted outlook: %+v", o)
	}
	if o.NormalizedRate != 0 || o.BottleneckStep != "" || o.BindingConstraint != "" || o.Steps != nil || o.Warnings != nil {
		t.Fatalf("an omitted outlook must carry no capacity figures: %+v", o)
	}
}

func TestSynthesizePathBrief_CapacityOutlookDoesNotAffectExceptions(t *testing.T) {
	target := PathTarget{SiteCode: "WH1", PathId: "pick-zone-a"}
	backlog := &BacklogFact{OverAlarmThreshold: true}
	staffing := &StaffingFact{Understaffed: true}

	base := SynthesizePathBrief(target, backlog, staffing, nil, nil, nil)
	if base.CapacityOutlook != nil {
		t.Fatal("SynthesizePathBrief must leave CapacityOutlook nil; the use case sets it")
	}
	if len(base.Exceptions) != 1 {
		t.Fatalf("setup: expected 1 exception, got %d", len(base.Exceptions))
	}
}
