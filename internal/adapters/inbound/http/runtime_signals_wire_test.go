package http_test

import (
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// updateGolden rewrites testdata/*.golden.json from the current output.
// The goldens were first captured BEFORE the json tags were stripped from
// internal/domain/policy (Tier-2 item 1a), so a green run proves the wire
// format of GET /runtime-signals is byte-identical across that refactor.
var updateGolden = flag.Bool("update", false, "rewrite golden wire-format files")

// stubTelemetry answers the three PromQL shapes RuntimeSignals issues
// (request total, 5xx total, p99 latency) from per-service tables.
type stubTelemetry struct {
	total, errs, p99 map[string]float64
}

func (s stubTelemetry) InstantQuery(_ context.Context, q string) ([]ports.MetricSample, error) {
	for svc := range s.total {
		if !strings.Contains(q, `"`+svc+`"`) {
			continue
		}
		table := s.total
		switch {
		case strings.Contains(q, "histogram_quantile"):
			table = s.p99
		case strings.Contains(q, "response_code"):
			table = s.errs
		}
		return []ports.MetricSample{{Value: table[svc]}}, nil
	}
	return nil, nil
}

func (stubTelemetry) RangeQuery(context.Context, string, time.Time, time.Time, time.Duration) ([]ports.MetricSample, error) {
	return nil, nil
}

type stubLogs struct{ entries []ports.LogEntry }

func (s stubLogs) QueryErrorLines(context.Context, string, int64, int) ([]ports.LogEntry, error) {
	return s.entries, nil
}

func getRuntimeSignals(t *testing.T, uc *usecases.RuntimeSignals) (int, string) {
	t.Helper()
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{RuntimeSignals: uc}, "warehouse-ops-agent-test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtime-signals", nil))
	return rec.Code, rec.Body.String()
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // fixed testdata path
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("wire format drifted from %s\n got: %s\nwant: %s", path, got, want)
	}
}

func TestGetRuntimeSignals_WireFormatGolden(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }

	t.Run("all sources available, mixed severities", func(t *testing.T) {
		uc := &usecases.RuntimeSignals{
			Telemetry: stubTelemetry{
				total: map[string]float64{"svc-ok": 0, "svc-warn": 100, "svc-crit": 100, "svc-logs": 100},
				errs:  map[string]float64{"svc-warn": 2, "svc-crit": 10},
				p99:   map[string]float64{"svc-warn": 1500, "svc-crit": 4000, "svc-logs": 200},
			},
			Logs: stubLogs{entries: []ports.LogEntry{
				{Labels: map[string]string{"app": "svc-logs"}, Line: "boom"},
				{Labels: map[string]string{"container": "svc-logs"}, Line: "bang"},
			}},
			Services:      []string{"svc-ok", "svc-warn", "svc-crit", "svc-logs"},
			Namespace:     "warehouse-systems",
			WindowMinutes: 10,
			Now:           fixed,
		}
		code, body := getRuntimeSignals(t, uc)
		if code != http.StatusOK {
			t.Fatalf("status = %d, body %s", code, body)
		}
		assertGolden(t, "runtime_signals_full.golden.json", body)
	})

	t.Run("no sources wired lists unavailableSources", func(t *testing.T) {
		uc := &usecases.RuntimeSignals{
			Services:      []string{"svc-ok"},
			Namespace:     "warehouse-systems",
			WindowMinutes: 5,
			Now:           fixed,
		}
		code, body := getRuntimeSignals(t, uc)
		if code != http.StatusOK {
			t.Fatalf("status = %d, body %s", code, body)
		}
		assertGolden(t, "runtime_signals_degraded.golden.json", body)
	})
}
