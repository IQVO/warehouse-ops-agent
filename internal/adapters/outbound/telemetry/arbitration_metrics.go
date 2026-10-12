package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ArbitrationMetrics is the OTel implementation of ports.ArbitrationMetrics
// (ADR 0004). Two instruments, named per ADR-0013's Tier-2 convention
// (dot-separated OTel instrument names and attribute keys — these are the
// names passed to meter.Int64Counter, NOT pre-suffixed Prometheus series
// names):
//
//	ops_agent.llm.arbitrations{use.case,mode,source}
//	ops_agent.llm.agreement{use.case,mode,agree}   (only when a valid plan existed)
//
// The second instrument is deliberately named "agreement" (singular), not
// "agreements": the OTel Collector's Prometheus exporter converts "." to
// "_" and appends "_total" to a monotonic counter whose name does not
// already end in "_total", so the plural form would export as
// ops_agent_llm_agreements_total — a DIFFERENT Prometheus series name than
// today's ops_agent_llm_agreement_total, breaking any existing dashboard
// or alert. Keeping it singular exports byte-identically as
// ops_agent_llm_agreement_total{use_case,mode,agree}, and
// ops_agent.llm.arbitrations exports unchanged as
// ops_agent_llm_arbitrations_total{use_case,mode,source}. This is the
// same non-breaking source-hygiene transform wes-work-planning's ADR-0013
// documents for its own path_id -> path.id rename: renaming the OTel-side
// name/attribute key never changes the Prometheus-side series name/label,
// so no dashboard or alert built on the old literal names needs to
// change. Verified empirically with go.opentelemetry.io/otel/exporters/
// prometheus against both instruments before merging this rename.
//
// The shadow-mode rollout gate is agreement{agree=true} over agreement
// for a use case (equivalently, ops_agent_llm_agreement_total{agree="true"}
// over ops_agent_llm_agreement_total on the Prometheus side).
type ArbitrationMetrics struct {
	arbitrations metric.Int64Counter
	agreement    metric.Int64Counter
}

// NewArbitrationMetrics registers the instruments on the global meter.
func NewArbitrationMetrics() (*ArbitrationMetrics, error) {
	meter := otel.Meter("warehouse-ops-agent/llm")
	arb, err := meter.Int64Counter("ops_agent.llm.arbitrations",
		metric.WithDescription("Decisions that went through policy.Arbitrate, by mode and winning source."),
		metric.WithUnit("{arbitration}"))
	if err != nil {
		return nil, err
	}
	agree, err := meter.Int64Counter("ops_agent.llm.agreement",
		metric.WithDescription("Valid model plans compared with the deterministic decision, by agreement."),
		metric.WithUnit("{agreement}"))
	if err != nil {
		return nil, err
	}
	return &ArbitrationMetrics{arbitrations: arb, agreement: agree}, nil
}

// RecordArbitration implements ports.ArbitrationMetrics.
func (m *ArbitrationMetrics) RecordArbitration(ctx context.Context, useCase, mode, source string, agree *bool) {
	m.arbitrations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("use.case", useCase),
		attribute.String("mode", mode),
		attribute.String("source", source),
	))
	if agree != nil {
		m.agreement.Add(ctx, 1, metric.WithAttributes(
			attribute.String("use.case", useCase),
			attribute.String("mode", mode),
			attribute.Bool("agree", *agree),
		))
	}
}
