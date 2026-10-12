package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/telemetry"
)

// ADR-0013 Tier-2 names: the OTel source instrument names are dot-separated
// ("ops_agent.llm.arbitrations" / "ops_agent.llm.agreement"), not the
// pre-suffixed Prometheus series names ("ops_agent_llm_arbitrations_total" /
// "ops_agent_llm_agreement_total"). The second instrument stays singular
// ("agreement", not "agreements") so its exported Prometheus series name
// does not change — see arbitration_metrics.go's package doc comment.
const (
	arbitrationsInstrumentName = "ops_agent.llm.arbitrations"
	agreementsInstrumentName   = "ops_agent.llm.agreement"
)

func useManualReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	return reader
}

func metricByName(collected metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

func dataPoints(t *testing.T, m metricdata.Metrics) []metricdata.DataPoint[int64] {
	t.Helper()
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s is a %T, want an int64 counter", m.Name, m.Data)
	}
	return sum.DataPoints
}

func attrValue(dp metricdata.DataPoint[int64], key attribute.Key) (string, bool) {
	v, ok := dp.Attributes.Value(key)
	if !ok {
		return "", false
	}
	return v.String(), true
}

// assertAttr fails the test unless dp carries key=want.
func assertAttr(t *testing.T, instrument string, dp metricdata.DataPoint[int64], key attribute.Key, want string) {
	t.Helper()
	v, ok := attrValue(dp, key)
	if !ok || v != want {
		t.Fatalf("%s: %s = %q (found=%v), want %q", instrument, key, v, ok, want)
	}
}

// assertNoAttr fails the test if dp still carries the legacy attribute key.
func assertNoAttr(t *testing.T, instrument string, dp metricdata.DataPoint[int64], legacyKey attribute.Key) {
	t.Helper()
	if _, ok := attrValue(dp, legacyKey); ok {
		t.Fatalf("%s: the legacy %s attribute key must no longer be emitted", instrument, legacyKey)
	}
}

// soleDataPoint fetches the instrument and asserts it has exactly one data
// point, returning it.
func soleDataPoint(t *testing.T, collected metricdata.ResourceMetrics, instrument string) metricdata.DataPoint[int64] {
	t.Helper()
	m, found := metricByName(collected, instrument)
	if !found {
		t.Fatalf("instrument %q not found", instrument)
	}
	points := dataPoints(t, m)
	if len(points) != 1 {
		t.Fatalf("want 1 data point for %s, got %d", instrument, len(points))
	}
	return points[0]
}

// TestNewArbitrationMetrics_RegistersDotSeparatedInstrumentNames proves the
// OTel instrument names registered via meter.Int64Counter are the
// dot-separated ADR-0013 names, not the old pre-suffixed Prometheus names.
func TestNewArbitrationMetrics_RegistersDotSeparatedInstrumentNames(t *testing.T) {
	reader := useManualReader(t)

	m, err := telemetry.NewArbitrationMetrics()
	if err != nil {
		t.Fatalf("NewArbitrationMetrics: %v", err)
	}
	agree := true
	m.RecordArbitration(context.Background(), "flow_balance_advisory", "shadow", "deterministic", &agree)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if _, found := metricByName(collected, arbitrationsInstrumentName); !found {
		t.Fatalf("instrument %q not found; collected: %+v", arbitrationsInstrumentName, collected)
	}
	if _, found := metricByName(collected, agreementsInstrumentName); !found {
		t.Fatalf("instrument %q not found; collected: %+v", agreementsInstrumentName, collected)
	}

	// The legacy pre-suffixed names must no longer be registered.
	for _, legacy := range []string{"ops_agent_llm_arbitrations_total", "ops_agent_llm_agreement_total"} {
		if _, found := metricByName(collected, legacy); found {
			t.Fatalf("legacy instrument name %q must not be registered anymore", legacy)
		}
	}
}

// TestRecordArbitration_UsesDotSeparatedUseCaseAttribute proves the
// "use_case" attribute key was renamed to the dot-separated "use.case"
// (ADR-0013), on both instruments, while "mode", "source" and "agree" stay
// as they were (already single-word / compliant).
func TestRecordArbitration_UsesDotSeparatedUseCaseAttribute(t *testing.T) {
	reader := useManualReader(t)

	m, err := telemetry.NewArbitrationMetrics()
	if err != nil {
		t.Fatalf("NewArbitrationMetrics: %v", err)
	}
	agree := false
	m.RecordArbitration(context.Background(), "flow_balance_advisory", "on", "llm", &agree)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect: %v", err)
	}

	arbPoint := soleDataPoint(t, collected, arbitrationsInstrumentName)
	assertAttr(t, arbitrationsInstrumentName, arbPoint, "use.case", "flow_balance_advisory")
	assertNoAttr(t, arbitrationsInstrumentName, arbPoint, "use_case")
	assertAttr(t, arbitrationsInstrumentName, arbPoint, "mode", "on")
	assertAttr(t, arbitrationsInstrumentName, arbPoint, "source", "llm")

	agrPoint := soleDataPoint(t, collected, agreementsInstrumentName)
	assertAttr(t, agreementsInstrumentName, agrPoint, "use.case", "flow_balance_advisory")
	assertNoAttr(t, agreementsInstrumentName, agrPoint, "use_case")
	assertAttr(t, agreementsInstrumentName, agrPoint, "agree", "false")
}

// TestRecordArbitration_NilAgreeSkipsAgreementsInstrument keeps the existing
// "no valid plan -> no agreement data point" behaviour intact across the
// rename.
func TestRecordArbitration_NilAgreeSkipsAgreementsInstrument(t *testing.T) {
	reader := useManualReader(t)

	m, err := telemetry.NewArbitrationMetrics()
	if err != nil {
		t.Fatalf("NewArbitrationMetrics: %v", err)
	}
	m.RecordArbitration(context.Background(), "flow_balance_advisory", "off", "deterministic", nil)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if _, found := metricByName(collected, arbitrationsInstrumentName); !found {
		t.Fatalf("instrument %q not found", arbitrationsInstrumentName)
	}
	if agr, found := metricByName(collected, agreementsInstrumentName); found {
		if len(dataPoints(t, agr)) != 0 {
			t.Fatalf("%s: want no data points when agree is nil, got %+v", agreementsInstrumentName, agr)
		}
	}
}
