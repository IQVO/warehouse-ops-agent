// Package usecases: RuntimeSignals (Phase 5 Task 5.3 of the
// harness-coverage-expansion plan) -- the runtime feedback loop this
// fleet's world-class observability stack (OTel/Prometheus/Loki/Grafana/
// Kiali/Jaeger) had zero wiring into an agent-facing signal before this.
package usecases

import (
	"context"
	"strconv"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// RuntimeSignals queries Prometheus (via the TelemetryReader port) for
// each monitored service's error rate and p99 latency over a rolling
// window, and Loki (via the LogReader port) for recent ERROR/FATAL log
// lines fleet-wide, then classifies each service's severity via
// internal/domain/policy. It NEVER mutates any upstream state -- this is
// a pure read-side/decision-support use case, same guardrail as every
// other use case in this repo (ADR-0001), and it is the ONLY use case
// that talks to the observability stack rather than the five bounded
// contexts' own MCP/REST surfaces.
//
// Every source failure degrades the report rather than failing the whole
// call: an unreachable Prometheus or Loki is recorded in
// UnavailableSources and that source's contribution to each ServiceSignal
// is simply omitted (zero-valued), exactly the "partial availability...
// degrade to a partial/typed result" guardrail DailyBrief already
// follows.
type RuntimeSignals struct {
	Telemetry     ports.TelemetryReader
	Logs          ports.LogReader
	Services      []string // the service names to report on, e.g. "order-management"
	Namespace     string   // the k8s namespace Loki's log query is scoped to
	WindowMinutes int      // the rolling window (minutes) for error-rate/latency queries
	LogLimit      int      // max error-log lines to fetch per QueryErrorLines call
	Now           func() time.Time
}

func (uc *RuntimeSignals) now() time.Time {
	if uc.Now != nil {
		return uc.Now()
	}
	return time.Now()
}

// Execute synthesizes the full runtime-signals report.
func (uc *RuntimeSignals) Execute(ctx context.Context) policy.RuntimeSignalsReport {
	report := policy.RuntimeSignalsReport{GeneratedAt: uc.now().UTC().Format(time.RFC3339)}

	windowMins := uc.WindowMinutes
	if windowMins <= 0 {
		windowMins = 10
	}
	logLimit := uc.LogLimit
	if logLimit <= 0 {
		logLimit = 50
	}

	var unavailable []string
	errorLogsByService, logsOK := uc.collectErrorLogs(ctx, windowMins, logLimit)
	if !logsOK {
		unavailable = append(unavailable, "loki")
	}

	telemetryAvailable := uc.Telemetry != nil
	if !telemetryAvailable {
		unavailable = append(unavailable, "prometheus")
	}

	signals, telemetryFailed := uc.collectSignals(ctx, errorLogsByService, windowMins, telemetryAvailable)
	if telemetryAvailable && telemetryFailed {
		unavailable = append(unavailable, "prometheus")
	}

	report.Services = signals
	report.UnavailableSources = unavailable
	return report
}

// collectErrorLogs fetches error-level log lines ONCE (fleet-wide, not
// per-service — Loki's own `container`/`app` labels let us attribute lines
// to a service after the fact) rather than issuing one query per service,
// which would multiply round-trips for no extra signal, and tallies them
// per service. ok is false when the LogReader port is unwired or the
// query failed; the caller then reports the source as unavailable and
// every service's RecentErrorLogs stays at its zero value, per Execute's
// degrade-not-fail contract.
func (uc *RuntimeSignals) collectErrorLogs(ctx context.Context, windowMins, logLimit int) (counts map[string]int, ok bool) {
	if uc.Logs == nil {
		return nil, false
	}
	entries, err := uc.Logs.QueryErrorLines(ctx, uc.Namespace, int64(windowMins*60), logLimit)
	if err != nil {
		return nil, false
	}
	counts = map[string]int{}
	for _, e := range entries {
		svc := e.Labels["app"]
		if svc == "" {
			svc = e.Labels["container"]
		}
		if svc != "" {
			counts[svc]++
		}
	}
	return counts, true
}

// collectSignals builds one ServiceSignal per monitored service, reading
// both Prometheus queries when the telemetry source is wired and
// classifying severities either way. The bool return reports whether any
// telemetry query failed, so Execute can mark prometheus unavailable after
// the loop while still emitting every service's (zero-valued) signal.
func (uc *RuntimeSignals) collectSignals(ctx context.Context, errorLogsByService map[string]int, windowMins int, telemetryAvailable bool) ([]policy.ServiceSignal, bool) {
	signals := make([]policy.ServiceSignal, 0, len(uc.Services))
	telemetryFailed := false
	for _, svc := range uc.Services {
		sig := policy.ServiceSignal{
			ServiceName:      svc,
			SampleWindowMins: windowMins,
			RecentErrorLogs:  errorLogsByService[svc],
		}

		if telemetryAvailable {
			var errRateOK, latencyOK bool
			sig.ErrorRate, errRateOK = uc.errorRate(ctx, svc, windowMins)
			sig.LatencyP99MS, latencyOK = uc.latencyP99(ctx, svc, windowMins)
			if !errRateOK || !latencyOK {
				telemetryFailed = true
			}
		}
		sig.ErrorRateSev = policy.ClassifyErrorRate(sig.ErrorRate)
		sig.LatencyP99Sev = policy.ClassifyLatencyP99(sig.LatencyP99MS)

		signals = append(signals, sig)
	}
	return signals, telemetryFailed
}

// errorRate computes the 5xx fraction of istio_requests_total for svc over
// the last windowMins. The bool return is false when EITHER underlying
// query failed (as opposed to succeeding with zero traffic, which is the
// common case for a quiet fleet and legitimately yields rate=0, ok=true) --
// callers use it only to decide whether to report the source as
// unavailable; the numeric value still defaults to 0 either way, so a
// single service's missing telemetry never blocks the rest of the report
// (see Execute's degrade-not-fail contract).
func (uc *RuntimeSignals) errorRate(ctx context.Context, service string, windowMins int) (float64, bool) {
	totalQ := ratioQuery(service, windowMins, "")
	errQ := ratioQuery(service, windowMins, `response_code=~"5.."`)

	total, totalOK := uc.sumFirst(ctx, totalQ)
	if total <= 0 {
		return 0, totalOK
	}
	errs, errsOK := uc.sumFirst(ctx, errQ)
	return errs / total, totalOK && errsOK
}

// latencyP99 reads histogram_quantile(0.99, ...) over
// istio_request_duration_milliseconds_bucket for svc over the last
// windowMins. Same degrade-to-zero-but-report-ok contract as errorRate.
func (uc *RuntimeSignals) latencyP99(ctx context.Context, service string, windowMins int) (float64, bool) {
	q := `histogram_quantile(0.99, sum by (le) (rate(istio_request_duration_milliseconds_bucket{destination_service_name="` +
		service + `"}[` + durationLiteral(windowMins) + `])))`
	return uc.sumFirst(ctx, q)
}

func (uc *RuntimeSignals) sumFirst(ctx context.Context, promQL string) (float64, bool) {
	samples, err := uc.Telemetry.InstantQuery(ctx, promQL)
	if err != nil {
		return 0, false
	}
	if len(samples) == 0 {
		return 0, true
	}
	var total float64
	for _, s := range samples {
		total += s.Value
	}
	return total, true
}

func ratioQuery(service string, windowMins int, extraLabel string) string {
	labels := `destination_service_name="` + service + `"`
	if extraLabel != "" {
		labels += "," + extraLabel
	}
	return `sum(increase(istio_requests_total{` + labels + `}[` + durationLiteral(windowMins) + `]))`
}

func durationLiteral(mins int) string {
	return strconv.Itoa(mins) + "m"
}
