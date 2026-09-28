// E4 — deterministic agent tool-selection evals.
//
// The T1/T2 use cases (e.g. DetectStrandedReservation) are the fleet's
// rule-based "agent": they decide WHICH sibling MCP tools to call, with
// WHICH arguments, in WHICH order, and what to recommend from the
// evidence. These evals prove that behavior end to end against REAL
// Streamable-HTTP MCP upstreams (scripted, recording stubs exposing the
// exact tool names from the fleet registry) wired through the REAL
// mcpclient adapters — no LLM, no flakiness: a plain `go test` inside the
// existing CI test job.
//
// Each scenario pins two things:
//  1. the TOOL SELECTION TRACE — the ordered sequence of tools/call names
//     with their exact argument payloads, proving the agent neither
//     over-fetches (calls tools it does not need) nor under-fetches
//     (skips the mandatory blast-radius read);
//  2. the DECISION — the policy's detected/action/rationale outcome.
package mcpclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/mcpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// callRecord is one recorded tools/call: the tool name and the exact
// argument object the agent sent over the wire.
type callRecord struct {
	Tool string
	Args map[string]any
}

// fleetStub is a scripted, recording Streamable-HTTP MCP upstream. It
// serves the fleet tool names the agent is allowed to select and records
// every call for the trace assertions.
type fleetStub struct {
	server *httptest.Server

	mu    sync.Mutex
	calls []callRecord
}

func (s *fleetStub) record(tool string, args map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, callRecord{Tool: tool, Args: args})
}

func (s *fleetStub) trace() []callRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]callRecord(nil), s.calls...)
}

// stubScenario scripts what each scripted tool returns.
type stubScenario struct {
	stuckTasks   ports.StuckTasksResult
	stuckErr     bool
	availability ports.Availability
	availErr     bool
	occupancy    ports.BinOccupancy
	occupancyErr bool
}

// newFulfillmentStub serves fulfillment-execution's diagnose_stuck_tasks
// exactly as the fleet registry names it.
func newFulfillmentStub(t *testing.T, scn stubScenario) *fleetStub {
	t.Helper()
	stub := &fleetStub{}
	server := mcp.NewServer(&mcp.Implementation{Name: "fulfillment-execution-eval-stub", Version: "0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "diagnose_stuck_tasks",
		Description: "eval stub",
	}, func(_ context.Context, req *mcp.CallToolRequest, in struct {
		WithinSeconds int `json:"withinSeconds"`
	}) (*mcp.CallToolResult, ports.StuckTasksResult, error) {
		args := map[string]any{"withinSeconds": float64(in.WithinSeconds)}
		stub.record("diagnose_stuck_tasks", args)
		if scn.stuckErr {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "upstream degraded"}},
			}, ports.StuckTasksResult{}, nil
		}
		return nil, scn.stuckTasks, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	stub.server = httptest.NewServer(handler)
	t.Cleanup(stub.server.Close)
	return stub
}

// newInventoryStub serves inventory-storage's check_availability and
// get_bin_occupancy exactly as the fleet registry names them.
func newInventoryStub(t *testing.T, scn stubScenario) *fleetStub {
	t.Helper()
	stub := &fleetStub{}
	server := mcp.NewServer(&mcp.Implementation{Name: "inventory-storage-eval-stub", Version: "0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "check_availability",
		Description: "eval stub",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		SKU string `json:"sku"`
	}) (*mcp.CallToolResult, ports.Availability, error) {
		stub.record("check_availability", map[string]any{"sku": in.SKU})
		if scn.availErr {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "upstream degraded"}},
			}, ports.Availability{}, nil
		}
		return nil, scn.availability, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_bin_occupancy",
		Description: "eval stub",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		BinId string `json:"binId"`
	}) (*mcp.CallToolResult, ports.BinOccupancy, error) {
		stub.record("get_bin_occupancy", map[string]any{"binId": in.BinId})
		if scn.occupancyErr {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "upstream degraded"}},
			}, ports.BinOccupancy{}, nil
		}
		return nil, scn.occupancy, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	stub.server = httptest.NewServer(handler)
	t.Cleanup(stub.server.Close)
	return stub
}

// runAgent wires the REAL use case over the REAL wire clients pointing at
// the two stubs and executes the stranded-reservation correlation.
func runAgent(t *testing.T, scn stubScenario, req usecases.StrandedReservationRequest) (policy.StrandedReservationException, []callRecord, []callRecord) {
	t.Helper()

	fulfillment := newFulfillmentStub(t, scn)
	inventory := newInventoryStub(t, scn)

	uc := usecases.DetectStrandedReservation{
		FulfillmentExecution: mcpclient.NewFulfillmentExecution(mcpclient.Config{Endpoint: fulfillment.server.URL}),
		InventoryStorage:     mcpclient.NewInventoryStorage(mcpclient.Config{Endpoint: inventory.server.URL}),
	}

	outcome, err := uc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("use case returned a hard error (it must degrade instead): %v", err)
	}
	return outcome, fulfillment.trace(), inventory.trace()
}

// assertTrace fails unless the recorded calls match the expected ordered
// (tool, args) sequence exactly — the tool-selection pin.
func assertTrace(t *testing.T, where string, got []callRecord, want []callRecord) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: expected %d tool calls, got %d (%s)", where, len(want), len(got), formatTrace(got))
	}
	for i, w := range want {
		if got[i].Tool != w.Tool {
			t.Fatalf("%s: call %d is %q, want %q (trace %s)", where, i, got[i].Tool, w.Tool, formatTrace(got))
		}
		gotJSON, _ := json.Marshal(got[i].Args)
		wantJSON, _ := json.Marshal(w.Args)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("%s: call %d (%s) args = %s, want %s", where, i, w.Tool, gotJSON, wantJSON)
		}
	}
}

func formatTrace(calls []callRecord) string {
	out := ""
	for _, c := range calls {
		args, _ := json.Marshal(c.Args)
		out += fmt.Sprintf("\n  %s %s", c.Tool, args)
	}
	return out
}

func correlatedScenario() stubScenario {
	return stubScenario{
		stuckTasks: ports.StuckTasksResult{
			Count: 1,
			Tasks: []ports.StuckTask{{
				TaskId:         "task-7",
				Type:           "PICK",
				LeaseStationId: "st-3",
				Reason:         "lease expired",
			}},
		},
		availability: ports.Availability{SKU: "SKU-A", Usable: 2},
		occupancy: ports.BinOccupancy{
			BinId: "BIN-1",
			Lines: []ports.BinOccupancyLine{
				{StockUnitId: "su-1", SKU: "SKU-A", OnHand: 10, Reserved: 4, Usable: 6, State: "RESERVED"},
			},
		},
	}
}

func fullRequest() usecases.StrandedReservationRequest {
	return usecases.StrandedReservationRequest{
		TaskType:           policy.TaskTypePick,
		WithinSeconds:      900,
		SKU:                "SKU-A",
		MinUsableThreshold: 5,
		ReservationId:      "res-42",
		BinId:              "BIN-1",
	}
}

// TestAgentEval_CorrelatedStrand_RecommendsRevoke is the golden path: the
// agent MUST read all three signals, in order, with exactly these
// arguments — and only then recommend the revoke.
func TestAgentEval_CorrelatedStrand_RecommendsRevoke(t *testing.T) {
	outcome, ffe, inv := runAgent(t, correlatedScenario(), fullRequest())

	assertTrace(t, "fulfillment", ffe, []callRecord{
		{Tool: "diagnose_stuck_tasks", Args: map[string]any{"withinSeconds": float64(900)}},
	})
	assertTrace(t, "inventory", inv, []callRecord{
		{Tool: "check_availability", Args: map[string]any{"sku": "SKU-A"}},
		{Tool: "get_bin_occupancy", Args: map[string]any{"binId": "BIN-1"}},
	})

	if !outcome.Detected {
		t.Fatalf("expected the correlation to be detected: %+v", outcome)
	}
	if outcome.Action != policy.ActionRevokeReservation {
		t.Fatalf("expected action %q, got %q (rationale: %s)", policy.ActionRevokeReservation, outcome.Action, outcome.Rationale)
	}
	if outcome.ReservationId != "res-42" {
		t.Fatalf("expected the recommendation to name res-42, got %q", outcome.ReservationId)
	}
	if outcome.BlastRadius == nil || outcome.BlastRadius.QuantityFreed != 4 {
		t.Fatalf("expected blast radius freeing 4, got %+v", outcome.BlastRadius)
	}
}

// TestAgentEval_HealthyStock_Holds: when usable stock is above the
// threshold the correlation dissolves and the agent holds. Pinned fact:
// the blast-radius read is EAGER — the use case reads get_bin_occupancy
// whenever a candidate reservation+bin are supplied, regardless of the
// correlation outcome (cheap read-only over-fetch by design). The
// invariant that matters: no write tool is ever called.
func TestAgentEval_HealthyStock_Holds(t *testing.T) {
	scn := correlatedScenario()
	scn.availability = ports.Availability{SKU: "SKU-A", Usable: 50}

	outcome, _, inv := runAgent(t, scn, fullRequest())

	assertTrace(t, "inventory", inv, []callRecord{
		{Tool: "check_availability", Args: map[string]any{"sku": "SKU-A"}},
		{Tool: "get_bin_occupancy", Args: map[string]any{"binId": "BIN-1"}},
	})
	if outcome.Action != policy.ActionHold {
		t.Fatalf("expected hold, got %q (rationale: %s)", outcome.Action, outcome.Rationale)
	}
}

// TestAgentEval_UpstreamDegraded_HoldsNeverPanics: a tool-level error from
// availability degrades to a typed hold — the call is still ATTEMPTED
// (visible in the trace), the use case never returns a hard error, and no
// write recommendation is made on partial evidence.
func TestAgentEval_UpstreamDegraded_HoldsNeverPanics(t *testing.T) {
	scn := correlatedScenario()
	scn.availErr = true

	outcome, _, inv := runAgent(t, scn, fullRequest())

	assertTrace(t, "inventory", inv, []callRecord{
		{Tool: "check_availability", Args: map[string]any{"sku": "SKU-A"}},
		{Tool: "get_bin_occupancy", Args: map[string]any{"binId": "BIN-1"}},
	})
	if outcome.Action != policy.ActionHold {
		t.Fatalf("degraded upstream must hold, got %q", outcome.Action)
	}
}

// TestAgentEval_NoCandidateReservation_NeverRecommendsWrite: a correlated
// shortfall with no candidate reservationId is detected for visibility but
// the agent MUST NOT recommend a write — and skips the blast-radius read
// entirely.
func TestAgentEval_NoCandidateReservation_NeverRecommendsWrite(t *testing.T) {
	req := fullRequest()
	req.ReservationId = ""
	req.BinId = ""

	outcome, _, inv := runAgent(t, correlatedScenario(), req)

	assertTrace(t, "inventory", inv, []callRecord{
		{Tool: "check_availability", Args: map[string]any{"sku": "SKU-A"}},
	})
	if !outcome.Detected {
		t.Fatal("expected the shortfall to be detected for visibility")
	}
	if outcome.Action != policy.ActionHold {
		t.Fatalf("without a candidate reservation the agent must hold, got %q", outcome.Action)
	}
}
