package policy_test

import (
	"strings"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// travelReading builds a facility-layout estimate_travel_distance fixture.
func travelReading(metres float64, estimated bool) *policy.TravelDistanceReading {
	return &policy.TravelDistanceReading{
		Source:    "facility-layout.estimate_travel_distance",
		From:      "WH1-STOR-AMB-A07-01-01-A",
		To:        "WH1-STOR-AMB-A09-03-01-A",
		MetresM:   metres,
		Estimated: estimated,
	}
}

func TestCorrelateTravelFactor(t *testing.T) {
	t.Run("distance above threshold => significant, cites both location codes and the metres figure", func(t *testing.T) {
		assertSignificantTravelFactor(t, travelReading(85.5, false))
	})
	t.Run("distance at threshold => negligible (boundary is inclusive of the threshold itself)", func(t *testing.T) {
		assertNegligibleTravelFactor(t, travelReading(policy.TravelFactorDistanceThresholdMetres, false), false)
	})
	t.Run("distance below threshold => negligible", func(t *testing.T) {
		assertNegligibleTravelFactor(t, travelReading(12.0, false), true)
	})
	t.Run("estimated route adds the estimated caveat to the rationale", func(t *testing.T) {
		assertEstimatedTravelCaveat(t, travelReading(200.0, true))
	})
	t.Run("nil reading => nil correlation, never a crash", func(t *testing.T) {
		assertNilTravelCorrelation(t)
	})
}

// assertSignificantTravelFactor checks the above-threshold branch: the
// correlation is significant, cites both location codes and the observed
// metres figure, and carries no estimated caveat for a measured reading.
func assertSignificantTravelFactor(t *testing.T, reading *policy.TravelDistanceReading) {
	t.Helper()
	got := policy.CorrelateTravelFactor(reading)
	if got == nil {
		t.Fatal("expected a correlation, got nil")
	}
	if got.Kind != policy.TravelFactorOutcomeSignificant {
		t.Errorf("Kind = %q, want %q", got.Kind, policy.TravelFactorOutcomeSignificant)
	}
	if !strings.Contains(got.Rationale, "85.5m") {
		t.Errorf("rationale should cite the observed distance: %q", got.Rationale)
	}
	if !strings.Contains(got.Rationale, "WH1-STOR-AMB-A07-01-01-A") || !strings.Contains(got.Rationale, "WH1-STOR-AMB-A09-03-01-A") {
		t.Errorf("rationale should cite both location codes: %q", got.Rationale)
	}
	if strings.Contains(got.Rationale, "estimated from the travel graph") {
		t.Errorf("a measured (non-estimated) reading should not carry the estimated caveat: %q", got.Rationale)
	}
}

// assertNegligibleTravelFactor checks the at-/below-threshold branch;
// wantRedirect is true only for the strictly-below case, whose rationale
// redirects the caller to other evidence.
func assertNegligibleTravelFactor(t *testing.T, reading *policy.TravelDistanceReading, wantRedirect bool) {
	t.Helper()
	got := policy.CorrelateTravelFactor(reading)
	if got == nil {
		t.Fatal("expected a correlation, got nil")
	}
	if got.Kind != policy.TravelFactorOutcomeNegligible {
		t.Errorf("Kind = %q, want %q", got.Kind, policy.TravelFactorOutcomeNegligible)
	}
	if wantRedirect && !strings.Contains(got.Rationale, "unlikely to materially explain") {
		t.Errorf("negligible rationale should redirect the caller to other evidence: %q", got.Rationale)
	}
}

// assertEstimatedTravelCaveat checks that an estimated reading's
// rationale says so.
func assertEstimatedTravelCaveat(t *testing.T, reading *policy.TravelDistanceReading) {
	t.Helper()
	got := policy.CorrelateTravelFactor(reading)
	if got == nil {
		t.Fatal("expected a correlation, got nil")
	}
	if !strings.Contains(got.Rationale, "estimated from the travel graph") {
		t.Errorf("an estimated reading's rationale must say so: %q", got.Rationale)
	}
}

// assertNilTravelCorrelation checks the nil-reading degrade path.
func assertNilTravelCorrelation(t *testing.T) {
	t.Helper()
	if got := policy.CorrelateTravelFactor(nil); got != nil {
		t.Errorf("expected nil correlation for a nil reading, got %+v", got)
	}
}
