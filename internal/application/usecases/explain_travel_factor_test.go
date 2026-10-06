package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func TestExplainTravelFactor_Execute(t *testing.T) {
	t.Run("a validation rejection from facility-layout is the caller's invalid input, not an outage", func(t *testing.T) {
		assertTravelFactorUpstreamRejectionIsInvalidInput(t)
	})
	t.Run("any other facility-layout rejection stays an upstream failure", func(t *testing.T) {
		assertTravelFactorOtherRejectionStaysUpstream(t)
	})
	t.Run("resolved reading correlates to a non-nil result", func(t *testing.T) {
		assertResolvedTravelFactorCorrelates(t, &fakeFacility{travel: ports.TravelDistance{MetresM: 90.0, Estimated: false}})
	})
	t.Run("negligible distance correlates accordingly", func(t *testing.T) {
		assertNegligibleTravelFactorResult(t, &fakeFacility{travel: ports.TravelDistance{MetresM: 5.0, Estimated: false}})
	})
	t.Run("facility-layout call error degrades to a zero-value result plus the error, never a panic", func(t *testing.T) {
		assertTravelFactorUpstreamError(t, errors.New("facility-layout unreachable"))
	})
	t.Run("nil Facility client degrades to a zero-value result, no error", func(t *testing.T) {
		assertTravelFactorNilClient(t)
	})
	t.Run("missing fromLocationCode or toLocationCode is rejected as untrusted input, never silently resolved", func(t *testing.T) {
		assertTravelFactorMissingLocationCodes(t)
	})
}

// executeTravelFactor runs the use case once against the given facility
// client over the standard fixture locations.
func executeTravelFactor(t *testing.T, facility ports.FacilityLayoutClient) (usecases.TravelFactorResult, error) {
	t.Helper()
	uc := &usecases.ExplainTravelFactor{Facility: facility}
	got, err := uc.Execute(context.Background(), "PICK-PATH-1", "WH1-STOR-AMB-A07-01-01-A", "WH1-STOR-AMB-A09-03-01-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return got, nil
}

// assertResolvedTravelFactorCorrelates checks the happy path: a resolved
// 90 m reading yields a non-nil Reading and a significant correlation.
func assertResolvedTravelFactorCorrelates(t *testing.T, facility ports.FacilityLayoutClient) {
	t.Helper()
	got, _ := executeTravelFactor(t, facility)
	if got.Reading == nil {
		t.Fatal("expected a non-nil Reading")
	}
	if got.Reading.MetresM != 90.0 {
		t.Errorf("Reading.MetresM = %v, want 90.0", got.Reading.MetresM)
	}
	if got.Correlation == nil {
		t.Fatal("expected a non-nil Correlation")
	}
	if got.Correlation.Kind != policy.TravelFactorOutcomeSignificant {
		t.Errorf("Correlation.Kind = %q, want %q", got.Correlation.Kind, policy.TravelFactorOutcomeSignificant)
	}
}

// assertNegligibleTravelFactorResult checks that a 5 m reading
// correlates as negligible.
func assertNegligibleTravelFactorResult(t *testing.T, facility ports.FacilityLayoutClient) {
	t.Helper()
	uc := &usecases.ExplainTravelFactor{Facility: facility}
	got, err := uc.Execute(context.Background(), "PICK-PATH-1", "WH1-STOR-AMB-A07-01-01-A", "WH1-STOR-AMB-A07-01-02-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Correlation == nil || got.Correlation.Kind != policy.TravelFactorOutcomeNegligible {
		t.Fatalf("expected TravelFactorOutcomeNegligible, got %+v", got.Correlation)
	}
}

// assertTravelFactorUpstreamRejectionIsInvalidInput checks the ADR 0018
// rule: facility-layout rejecting the call with a validation slug (the
// mcpclient wraps ports.ErrUpstreamInvalidInput) is classified as
// ErrInvalidInput, so the HTTP adapter answers 400, while the upstream
// error stays in the chain for logging.
func assertTravelFactorUpstreamRejectionIsInvalidInput(t *testing.T) {
	t.Helper()
	upstream := fmt.Errorf("facility-layout: tool estimate_travel_distance reported an error: malformed-location-code: bad: %w", ports.ErrUpstreamInvalidInput)
	uc := &usecases.ExplainTravelFactor{Facility: &fakeFacility{travelErr: upstream}}

	got, err := uc.Execute(context.Background(), "PICK-PATH-1", "BAD", "WH1-STOR-AMB-A09-03-01-A")
	if !errors.Is(err, usecases.ErrInvalidInput) {
		t.Fatalf("err = %v, want it to wrap ErrInvalidInput", err)
	}
	if !errors.Is(err, upstream) {
		t.Errorf("the upstream error must stay in the chain, got %v", err)
	}
	if !strings.Contains(err.Error(), "malformed-location-code") {
		t.Errorf("the upstream slug must stay visible to the caller, got %v", err)
	}
	if got.Reading != nil || got.Correlation != nil {
		t.Errorf("expected a zero-value result on error, got %+v", got)
	}
}

// assertTravelFactorOtherRejectionStaysUpstream checks the other half of the
// rule: not-found, internal-error, a legacy slug-less message or an outage
// never become invalid input.
func assertTravelFactorOtherRejectionStaysUpstream(t *testing.T) {
	t.Helper()
	for _, upstream := range []error{
		errors.New("facility-layout: tool estimate_travel_distance reported an error: site-not-found: nope"),
		errors.New("facility-layout: tool estimate_travel_distance reported an error: internal-error: boom"),
		errors.New("facility-layout: tool estimate_travel_distance reported an error: from and to are both required"),
		errors.New("facility-layout: connect: connection refused"),
	} {
		uc := &usecases.ExplainTravelFactor{Facility: &fakeFacility{travelErr: upstream}}
		_, err := uc.Execute(context.Background(), "PICK-PATH-1", "A", "B")
		if !errors.Is(err, upstream) {
			t.Errorf("err = %v, want %v", err, upstream)
		}
		if errors.Is(err, usecases.ErrInvalidInput) {
			t.Errorf("%v must not be classified as invalid input", upstream)
		}
	}
}

// assertTravelFactorUpstreamError checks the degrade path: a failed
// facility-layout call yields the error plus a zero-value result, never a
// panic.
func assertTravelFactorUpstreamError(t *testing.T, wantErr error) {
	t.Helper()
	facility := &fakeFacility{travelErr: wantErr}
	uc := &usecases.ExplainTravelFactor{Facility: facility}

	got, err := uc.Execute(context.Background(), "PICK-PATH-1", "WH1-STOR-AMB-A07-01-01-A", "WH1-STOR-AMB-A09-03-01-A")
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	if errors.Is(err, usecases.ErrInvalidInput) {
		t.Errorf("an upstream failure must not be classified as invalid input: %v", err)
	}
	if got.Reading != nil || got.Correlation != nil {
		t.Errorf("expected a zero-value result on error, got %+v", got)
	}
}

// assertTravelFactorNilClient checks that a nil Facility client degrades
// to a zero-value result with no error.
func assertTravelFactorNilClient(t *testing.T) {
	t.Helper()
	uc := &usecases.ExplainTravelFactor{Facility: nil}

	got, err := uc.Execute(context.Background(), "PICK-PATH-1", "WH1-STOR-AMB-A07-01-01-A", "WH1-STOR-AMB-A09-03-01-A")
	if err != nil {
		t.Fatalf("unexpected error for a nil client: %v", err)
	}
	if got.Reading != nil || got.Correlation != nil {
		t.Errorf("expected a zero-value result for a nil client, got %+v", got)
	}
}

// assertTravelFactorMissingLocationCodes checks the untrusted-input
// guardrail: an empty from/to location code is rejected, never silently
// resolved.
func assertTravelFactorMissingLocationCodes(t *testing.T) {
	t.Helper()
	facility := &fakeFacility{travel: ports.TravelDistance{MetresM: 90.0}}
	uc := &usecases.ExplainTravelFactor{Facility: facility}

	if _, err := uc.Execute(context.Background(), "PICK-PATH-1", "", "WH1-STOR-AMB-A09-03-01-A"); !errors.Is(err, usecases.ErrInvalidInput) {
		t.Errorf("empty fromLocationCode: err = %v, want ErrInvalidInput", err)
	}
	if _, err := uc.Execute(context.Background(), "PICK-PATH-1", "WH1-STOR-AMB-A07-01-01-A", ""); !errors.Is(err, usecases.ErrInvalidInput) {
		t.Errorf("empty toLocationCode: err = %v, want ErrInvalidInput", err)
	}
}
