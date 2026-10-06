// Package policy: runtime-signal severity classification (Phase 5 Task 5.3
// of the harness-coverage-expansion plan).
//
// This file adds the one pure decision this feature makes: given an error
// rate or a p99 latency sample, is it within normal range, a WARNING, or a
// CRITICAL burn? Gathering the raw samples from Prometheus/Loki is I/O and
// belongs one layer out (internal/application/usecases.RuntimeSignals);
// this file is the only place that decides what counts as "bad."
package policy

// SignalSeverity classifies one runtime signal against its threshold.
// Reuses this package's existing Severity type (see dailybrief.go) rather
// than introducing a parallel enum -- both describe the same concept
// ("how bad is this"), just for a different source of facts.
type SignalSeverity = Severity

const SeverityNormal SignalSeverity = "normal"

// ErrorRateThresholds are the fleet-wide default burn-rate boundaries for
// a service's 5xx-response-code fraction over the observed window.
// WARNING at 1%, CRITICAL at 5% — deliberately generous defaults for a
// study-project fleet with no real SLO negotiated yet; a future slice may
// make these per-service configurable rather than fleet-wide constants.
const (
	ErrorRateWarningThreshold  = 0.01
	ErrorRateCriticalThreshold = 0.05
)

// LatencyP99WarningMS / LatencyP99CriticalMS are the fleet-wide default p99
// latency boundaries, in milliseconds, for a service's request duration.
const (
	LatencyP99WarningMS  = 1000.0
	LatencyP99CriticalMS = 3000.0
)

// ClassifyErrorRate returns the severity of an observed error-rate
// fraction (errors / total requests, in [0,1]).
func ClassifyErrorRate(rate float64) SignalSeverity {
	if rate >= ErrorRateCriticalThreshold {
		return SeverityCritical
	}
	if rate >= ErrorRateWarningThreshold {
		return SeverityWarning
	}
	return SeverityNormal
}

// ClassifyLatencyP99 returns the severity of an observed p99 latency, in
// milliseconds.
func ClassifyLatencyP99(p99ms float64) SignalSeverity {
	if p99ms >= LatencyP99CriticalMS {
		return SeverityCritical
	}
	if p99ms >= LatencyP99WarningMS {
		return SeverityWarning
	}
	return SeverityNormal
}

// ServiceSignal is one service's runtime-health snapshot: its error-rate
// and latency severities, plus how many recent error-level log lines this
// agent observed for it (0 is a valid, common value — most services most
// of the time).
type ServiceSignal struct {
	ServiceName      string
	ErrorRate        float64
	ErrorRateSev     SignalSeverity
	LatencyP99MS     float64
	LatencyP99Sev    SignalSeverity
	RecentErrorLogs  int
	SampleWindowMins int
}

// Severity is the worst of this service's individual signal severities —
// the single value a caller sorts/filters the report by.
func (s ServiceSignal) Severity() SignalSeverity {
	sev := s.ErrorRateSev
	if worse(s.LatencyP99Sev, sev) {
		sev = s.LatencyP99Sev
	}
	if s.RecentErrorLogs > 0 && worse(SeverityWarning, sev) {
		sev = SeverityWarning
	}
	return sev
}

func worse(a, b SignalSeverity) bool {
	return rank(a) > rank(b)
}

func rank(s SignalSeverity) int {
	switch s {
	case SeverityCritical:
		return 2
	case SeverityWarning:
		return 1
	default:
		return 0
	}
}

// RuntimeSignalsReport is the full structured, LLM-consumable output: one
// ServiceSignal per monitored service, generated at a point in time.
// GeneratedAt and the per-service breakdown are all this type carries —
// it is a plain read model, no behaviour beyond the severity helpers
// above.
//
// It carries no serialisation tags: the wire shape of GET /runtime-signals
// is owned by the inbound HTTP adapter's runtimeSignalsDTO
// (internal/adapters/inbound/http/router.go), pinned by golden tests.
type RuntimeSignalsReport struct {
	GeneratedAt        string
	Services           []ServiceSignal
	UnavailableSources []string
}
