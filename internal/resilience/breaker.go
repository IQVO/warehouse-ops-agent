// Package resilience holds the small pieces of circuit-breaker/timeout
// policy shared by outbound dependency calls in this service (plan
// section 2.6 / ADR-0011): the shared trip condition and the
// Prometheus-gauge wiring contract. It mirrors the SAME shared-package
// shape order-management's ADR-0025 established
// (internal/resilience/{breaker,deadline}.go there) — this is a fresh,
// repo-local package, not an import from order-management: no repo in
// this fleet imports another repo's Go packages (bounded-context
// isolation, enforced by internal/architecture's
// TestNoDirectDependencyOnBoundedContexts).
//
// One gobreaker.CircuitBreaker instance is constructed PER downstream
// dependency (never one global breaker). Today that is exactly one call
// site — internal/adapters/outbound/llm/anthropic's wrapping of the
// Anthropic Messages API call — but the package is written the same
// general way order-management's is, so a second outbound dependency
// this agent grows later (e.g. a direct REST call not proxied through
// MCP) can reuse it without re-deriving the tuning.
package resilience

import (
	"time"

	gobreaker "github.com/sony/gobreaker/v2"
)

// Shared breaker tuning (plan section 2.6, mirroring order-management's
// ADR-0025 constants):
//
//   - DefaultMaxRequests: only 1 probe request is let through per
//     half-open cycle, so a still-broken dependency is confirmed broken
//     again with minimal extra load (and, for a paid LLM API, minimal
//     extra cost).
//   - DefaultInterval: the closed-state window gobreaker uses to reset
//     its rolling Counts. Without this, a failure streak from long ago
//     could combine with a fresh one to trip the breaker on stale
//     history.
//   - DefaultCooldown: how long the breaker stays open before allowing a
//     half-open probe.
const (
	DefaultMaxRequests = 1
	DefaultInterval    = 30 * time.Second
	DefaultCooldown    = 30 * time.Second

	// minRequestVolumeForErrorRate guards ReadyToTrip's error-rate leg:
	// without a minimum sample size, one early failure (1 request, a
	// 100% error rate) would trip the breaker on its own — exactly what
	// the ConsecutiveFailures>=5 leg already exists to gate sensibly.
	// The error-rate leg only engages once there is enough traffic for
	// "over half failed" to mean something.
	minRequestVolumeForErrorRate = 10
)

// ReadyToTrip is the shared trip condition for every breaker in this
// service (plan section 2.6): open the breaker when either five
// consecutive requests have failed, or — given at least
// minRequestVolumeForErrorRate requests in the current closed-state
// window — more than half of them failed.
func ReadyToTrip(counts gobreaker.Counts) bool {
	if counts.ConsecutiveFailures >= 5 {
		return true
	}
	if counts.Requests < minRequestVolumeForErrorRate {
		return false
	}
	failureRate := float64(counts.TotalFailures) / float64(counts.Requests)
	return failureRate > 0.5
}

// StateRecorder receives a breaker's state transitions so they can be
// exposed as the Prometheus gauge circuit_breaker_state{dependency="..."}
// (see internal/adapters/outbound/telemetry.CircuitBreakerMetrics, the
// single implementation, reusing this service's existing OTel meter
// rather than standing up a second registry).
//
// state follows gobreaker.State's own numbering verbatim (0=closed,
// 1=half-open, 2=open), so RecordStateChange needs no translation table.
type StateRecorder interface {
	SetState(dependency string, state int64)
}

// RecordStateChange adapts a StateRecorder into the shape
// gobreaker.Settings.OnStateChange expects for dependency. A nil
// recorder is a documented no-op (mirrors this repo's nil-Logger/nil-
// Metrics convention elsewhere — e.g. ArbitrationMetrics), so a test
// that does not care about the metric never needs to construct one.
func RecordStateChange(dependency string, recorder StateRecorder) func(name string, from, to gobreaker.State) {
	return func(_ string, _, to gobreaker.State) {
		if recorder == nil {
			return
		}
		recorder.SetState(dependency, int64(to))
	}
}
