package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ArbitrationMetrics is the OTel implementation of ports.ArbitrationMetrics
// (ADR 0004). Two instruments:
//
//	ops_agent_llm_arbitrations_total{use_case,mode,source}
//	ops_agent_llm_agreement_total{use_case,mode,agree}   (only when a valid plan existed)
//
// The shadow-mode rollout gate is agreement_total{agree=true} over
// agreement_total for a use case.
type ArbitrationMetrics struct {
	arbitrations metric.Int64Counter
	agreement    metric.Int64Counter
}

// NewArbitrationMetrics registers the instruments on the global meter.
func NewArbitrationMetrics() (*ArbitrationMetrics, error) {
	meter := otel.Meter("warehouse-ops-agent/llm")
	arb, err := meter.Int64Counter("ops_agent_llm_arbitrations_total",
		metric.WithDescription("Decisions that went through policy.Arbitrate, by mode and winning source."),
		metric.WithUnit("{arbitration}"))
	if err != nil {
		return nil, err
	}
	agree, err := meter.Int64Counter("ops_agent_llm_agreement_total",
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
		attribute.String("use_case", useCase),
		attribute.String("mode", mode),
		attribute.String("source", source),
	))
	if agree != nil {
		m.agreement.Add(ctx, 1, metric.WithAttributes(
			attribute.String("use_case", useCase),
			attribute.String("mode", mode),
			attribute.Bool("agree", *agree),
		))
	}
}
