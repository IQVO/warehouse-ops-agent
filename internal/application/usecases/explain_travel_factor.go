// Package usecases: ExplainTravelFactor (ADR 0009) — gathers a real
// travel-distance reading from facility-layout's estimate_travel_distance
// tool for two caller-supplied location codes, and hands it to the
// domain/policy travel-factor correlation rule to produce one advisory.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

const flSource = "facility-layout.estimate_travel_distance"

// ErrInvalidInput marks a use-case error caused by the caller's own input
// (e.g. a missing location code), as opposed to an upstream failure. The
// inbound HTTP adapter maps it to 400; every other error from Execute is an
// upstream degradation and maps to 502.
var ErrInvalidInput = errors.New("invalid input")

// ExplainTravelFactor is the ADR-0009 use case: given a pathId (for
// context/logging only — never used to resolve the location codes, which
// the caller must always supply explicitly) and two facility-layout
// location codes, it calls estimate_travel_distance and returns the
// resulting policy.TravelFactorCorrelation alongside the raw reading.
//
// This use case deliberately never infers, guesses, or resolves a
// location code on the caller's behalf: fulfillment-execution's Station
// aggregate has an optional locationCode field (see that repo's ADR
// 0024) but no published MCP tool surfaces it today, so there is no live
// wire path from "this stuck task/path" to "these two location codes" —
// see ADR 0009's Context for the full reasoning. A caller who does not
// already know both codes cannot use this use case, and it will not
// pretend otherwise.
type ExplainTravelFactor struct {
	Facility ports.FacilityLayoutClient

	// Logger receives a structured warning when the upstream call
	// fails, so a degraded (nil-correlation) result is observable
	// without forcing the caller to inspect the returned error.
	// Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// TravelFactorResult is ExplainTravelFactor's return value: the raw
// facility-layout reading (nil when the upstream call failed) and the
// resulting correlation (nil under the same condition —
// CorrelateTravelFactor never itself decides "no reading", only
// classifies a reading it is given).
type TravelFactorResult struct {
	Reading     *policy.TravelDistanceReading
	Correlation *policy.TravelFactorCorrelation
}

// Execute calls facility-layout's estimate_travel_distance for
// (fromLocationCode, toLocationCode) and correlates the result. A
// transport/call error (unreachable facility-layout, a malformed
// location code rejected by that context's own ParseLocationCode)
// degrades to a Result with both fields nil, mirroring every other
// upstream-unavailable degradation in this package (ADR-0004 fallback
// discipline) — the error is returned as well, for the caller to log or
// surface. It is never a use-case-level failure that should propagate as
// a 500: the HTTP adapter reports it as 502 (upstream degradation), while
// an error wrapping ErrInvalidInput (missing location code) is the
// caller's mistake and is reported as 400.
func (uc *ExplainTravelFactor) Execute(ctx context.Context, pathId, fromLocationCode, toLocationCode string) (TravelFactorResult, error) {
	logger := uc.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if uc.Facility == nil {
		return TravelFactorResult{}, nil
	}
	if fromLocationCode == "" || toLocationCode == "" {
		return TravelFactorResult{}, fmt.Errorf("%w: explain_travel_factor: fromLocationCode and toLocationCode are both required", ErrInvalidInput)
	}

	raw, err := uc.Facility.EstimateTravelDistance(ctx, fromLocationCode, toLocationCode)
	if err != nil {
		logger.Warn("explain_travel_factor: facility-layout unavailable",
			"pathId", sanitizeForLog(pathId), "from", sanitizeForLog(fromLocationCode), "to", sanitizeForLog(toLocationCode), "error", sanitizeForLog(err.Error()))
		if errors.Is(err, ports.ErrUpstreamInvalidInput) {
			// facility-layout rejected the codes with one of the fleet's
			// validation slugs (ADR 0018): the caller's input, not an
			// outage. The upstream error stays in the chain.
			return TravelFactorResult{}, fmt.Errorf("%w: explain_travel_factor: %w", ErrInvalidInput, err)
		}
		return TravelFactorResult{}, err
	}

	reading := &policy.TravelDistanceReading{
		Source:    flSource,
		From:      fromLocationCode,
		To:        toLocationCode,
		MetresM:   raw.MetresM,
		Estimated: raw.Estimated,
	}
	return TravelFactorResult{
		Reading:     reading,
		Correlation: policy.CorrelateTravelFactor(reading),
	}, nil
}
