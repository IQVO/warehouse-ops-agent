package mcpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type taskTypeUtilizationTestIn struct {
	TaskType      string `json:"taskType"`
	WindowSeconds int64  `json:"windowSeconds,omitempty"`
}

// newLaborPerformanceTestUpstream serves a real Streamable-HTTP MCP
// server exposing the labor-performance tool the agent calls,
// unauthenticated, so LaborPerformance is exercised over the wire exactly as it would be
// against labor-performance's cmd/labor's MCP adapter.
func newLaborPerformanceTestUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "labor-performance-test", Version: "0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "get_task_type_utilization"}, func(_ context.Context, _ *mcp.CallToolRequest, in taskTypeUtilizationTestIn) (*mcp.CallToolResult, ports.TaskTypeUtilization, error) {
		window := in.WindowSeconds
		if window <= 0 {
			window = 3600
		}
		if in.TaskType == "SLAM" {
			// No observations in the window: nil pct, never a fabricated 0.
			return nil, ports.TaskTypeUtilization{
				TaskType:      in.TaskType,
				WindowSeconds: window,
			}, nil
		}
		pct := 42.0
		return nil, ports.TaskTypeUtilization{
			TaskType:       in.TaskType,
			Associates:     3,
			WindowSeconds:  window,
			TaskSeconds:    1000,
			IdleSeconds:    1400,
			OpenGapSeconds: 30,
			UtilizationPct: &pct,
		}, nil
	})

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return httptest.NewServer(h)
}

func TestLaborPerformance_UnreachableUpstream(t *testing.T) {
	up := newLaborPerformanceTestUpstream(t)
	up.Close() // closed before use: any call must surface a connection error.

	c := NewLaborPerformance(Config{Endpoint: up.URL})
	if _, err := c.GetTaskTypeUtilization(context.Background(), "PICK", 0); err == nil {
		t.Fatal("an unreachable upstream endpoint must surface as an error")
	}
}

func TestLaborPerformance_GetTaskTypeUtilization(t *testing.T) {
	up := newLaborPerformanceTestUpstream(t)
	defer up.Close()

	c := NewLaborPerformance(Config{Endpoint: up.URL})
	out, err := c.GetTaskTypeUtilization(context.Background(), "PICK", 1800)
	if err != nil {
		t.Fatalf("GetTaskTypeUtilization: %v", err)
	}
	if out.TaskType != "PICK" || out.WindowSeconds != 1800 || out.Associates != 3 {
		t.Fatalf("unexpected utilization: %+v", out)
	}
	if out.UtilizationPct == nil || *out.UtilizationPct != 42.0 {
		t.Fatalf("unexpected utilizationPct: %+v", out.UtilizationPct)
	}
}

func TestLaborPerformance_GetTaskTypeUtilization_DefaultWindow(t *testing.T) {
	up := newLaborPerformanceTestUpstream(t)
	defer up.Close()

	c := NewLaborPerformance(Config{Endpoint: up.URL})
	out, err := c.GetTaskTypeUtilization(context.Background(), "PACK", 0)
	if err != nil {
		t.Fatalf("GetTaskTypeUtilization: %v", err)
	}
	if out.WindowSeconds != 3600 {
		t.Fatalf("expected the tool's own default window (3600) to apply, got %d", out.WindowSeconds)
	}
}

func TestLaborPerformance_GetTaskTypeUtilization_NilPctNeverCoercedToZero(t *testing.T) {
	up := newLaborPerformanceTestUpstream(t)
	defer up.Close()

	c := NewLaborPerformance(Config{Endpoint: up.URL})
	out, err := c.GetTaskTypeUtilization(context.Background(), "SLAM", 3600)
	if err != nil {
		t.Fatalf("GetTaskTypeUtilization: %v", err)
	}
	if out.UtilizationPct != nil {
		t.Fatalf("expected nil UtilizationPct when nothing was observed, got %v", *out.UtilizationPct)
	}
}

func TestLaborPerformance_ImplementsPort(t *testing.T) {
	var _ ports.LaborPerformanceClient = NewLaborPerformance(Config{Endpoint: "http://example.invalid"})
}
