package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The test-side input structs mirror network-inventory-planning's published
// tool argument names (snake_case, internal/adapters/inbound/mcp/tools.go):
// if the client sends a different key the recorded input is empty and the
// assertions below fail.
type nipGetTransferTestIn struct {
	TransferID string `json:"transfer_id"`
}

type nipListTestIn struct {
	State string `json:"state,omitempty"`
	Site  string `json:"site,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type nipStuckTestIn struct {
	OlderThanMinutes int    `json:"older_than_minutes"`
	State            string `json:"state,omitempty"`
	Limit            int    `json:"limit,omitempty"`
}

type nipNoArgsTestIn struct{}

// nipUpstream is a real Streamable-HTTP MCP server publishing the four NIP
// read tools PLUS two decoy mutating tools that fail the test if ever invoked.
type nipUpstream struct {
	*httptest.Server
	mu        sync.Mutex
	lastGet   nipGetTransferTestIn
	lastList  nipListTestIn
	lastStuck nipStuckTestIn
	calls     []string
}

func (u *nipUpstream) record(tool string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
}

func toolErr(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func nipSampleTransfer(id, state string) ports.Transfer {
	return ports.Transfer{
		Id: id, State: state, SKU: "SKU-1", Quantity: 40, PickedQuantity: 10,
		OriginSiteId: "WH1", DestinationSiteId: "WH2", PolicyVersion: "v1", ReservationId: "res-9",
		ExpiresAt: "2026-10-08T00:00:00Z", CreatedAt: "2026-10-06T08:00:00Z", UpdatedAt: "2026-10-06T09:30:00Z", Version: 4,
	}
}

func newNIPTestUpstream(t *testing.T) *nipUpstream {
	t.Helper()
	up := &nipUpstream{}
	server := mcp.NewServer(&mcp.Implementation{Name: "nip-test", Version: "0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "get_transfer"}, func(_ context.Context, _ *mcp.CallToolRequest, in nipGetTransferTestIn) (*mcp.CallToolResult, ports.TransferDetail, error) {
		up.record("get_transfer")
		up.mu.Lock()
		up.lastGet = in
		up.mu.Unlock()
		if in.TransferID == "ghost" {
			return toolErr("transfer-not-found: transfer ghost not found"), ports.TransferDetail{}, nil
		}
		return nil, ports.TransferDetail{
			Transfer: nipSampleTransfer(in.TransferID, "ALLOCATING"),
			Audit: []ports.TransferAuditEntry{
				{Seq: 1, To: "DRAFT", Event: "TransferDrafted", Cause: "operator request", OccurredAt: "2026-10-06T08:00:00Z"},
				{Seq: 2, From: "DRAFT", To: "ALLOCATING", Event: "AllocationRequested", Cause: "approved", OccurredAt: "2026-10-06T09:30:00Z"},
			},
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "list_transfers"}, func(_ context.Context, _ *mcp.CallToolRequest, in nipListTestIn) (*mcp.CallToolResult, ports.TransferPage, error) {
		up.record("list_transfers")
		up.mu.Lock()
		up.lastList = in
		up.mu.Unlock()
		if in.State == "BOGUS" {
			return toolErr("invalid-query: unknown state \"BOGUS\""), ports.TransferPage{}, nil
		}
		return nil, ports.TransferPage{Transfers: []ports.Transfer{nipSampleTransfer("t-1", "PICKED")}, Total: 1}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "find_stuck_transfers"}, func(_ context.Context, _ *mcp.CallToolRequest, in nipStuckTestIn) (*mcp.CallToolResult, ports.TransferPage, error) {
		up.record("find_stuck_transfers")
		up.mu.Lock()
		up.lastStuck = in
		up.mu.Unlock()
		if in.OlderThanMinutes <= 0 {
			return toolErr("invalid-query: older_than_minutes must be positive"), ports.TransferPage{}, nil
		}
		return nil, ports.TransferPage{
			Transfers: []ports.Transfer{nipSampleTransfer("t-1", "ALLOCATING"), nipSampleTransfer("t-2", "IN_TRANSIT")},
			Total:     5,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "simulate_transfer_options"}, func(_ context.Context, _ *mcp.CallToolRequest, _ nipNoArgsTestIn) (*mcp.CallToolResult, ports.TransferSimulation, error) {
		up.record("simulate_transfer_options")
		return nil, ports.TransferSimulation{
			Advisory: true, AsOf: "2026-10-06T10:00:00Z",
			Sites: []ports.SiteSimulation{
				{Site: "WH1", OriginEnabled: true, DestinationEnabled: true, TotalDemand: 100, CapacityOverWindow: 140, CapacityHeadroom: 40, WindowStart: "2026-10-06T08:00:00Z", WindowEnd: "2026-10-06T16:00:00Z"},
				{Site: "WH2", OriginEnabled: false, DestinationEnabled: true, TotalDemand: 90, CapacityOverWindow: 60, CapacityHeadroom: -30, WindowStart: "2026-10-06T08:00:00Z", WindowEnd: "2026-10-06T16:00:00Z"},
			},
		}, nil
	})

	// Mutating tools the client must never reach (NIP publishes none; these
	// pin that the client does not invent one).
	for _, name := range []string{"approve_transfer", "cancel_transfer"} {
		name := name
		mcp.AddTool(server, &mcp.Tool{Name: name}, func(_ context.Context, _ *mcp.CallToolRequest, _ nipGetTransferTestIn) (*mcp.CallToolResult, ports.TransferDetail, error) {
			t.Errorf("mutating tool %s was invoked", name)
			return nil, ports.TransferDetail{}, nil
		})
	}

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	up.Server = httptest.NewServer(h)
	return up
}

func TestNetworkInventoryPlanning_GetTransfer(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	out, err := c.GetTransfer(context.Background(), "t-1")
	if err != nil {
		t.Fatalf("GetTransfer: %v", err)
	}
	if up.lastGet.TransferID != "t-1" {
		t.Fatalf("tool received %+v, want transfer_id t-1", up.lastGet)
	}
	assertSampleTransfer(t, out.Transfer)
	assertSampleAudit(t, out.Audit)
}

// assertSampleTransfer pins every field the client must carry across the wire
// for the fixture transfer. Split out of the test so each helper stays under
// the repo's cyclomatic-complexity gate without loosening it.
func assertSampleTransfer(t *testing.T, got ports.Transfer) {
	t.Helper()
	if got.Id != "t-1" || got.State != "ALLOCATING" || got.SKU != "SKU-1" || got.Quantity != 40 || got.PickedQuantity != 10 {
		t.Fatalf("unexpected transfer identity/quantities: %+v", got)
	}
	if got.OriginSiteId != "WH1" || got.DestinationSiteId != "WH2" || got.ReservationId != "res-9" {
		t.Fatalf("unexpected transfer sites/reservation: %+v", got)
	}
	if got.Version != 4 || got.UpdatedAt != "2026-10-06T09:30:00Z" {
		t.Fatalf("unexpected transfer version/timestamp: %+v", got)
	}
}

func assertSampleAudit(t *testing.T, audit []ports.TransferAuditEntry) {
	t.Helper()
	if len(audit) != 2 {
		t.Fatalf("unexpected audit trail length: %+v", audit)
	}
	if got := audit[1]; got.From != "DRAFT" || got.To != "ALLOCATING" || got.Cause != "approved" || got.Seq != 2 {
		t.Fatalf("unexpected audit trail: %+v", audit)
	}
}

func TestNetworkInventoryPlanning_GetTransfer_NotFound(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	_, err := c.GetTransfer(context.Background(), "ghost")
	if err == nil || !strings.Contains(err.Error(), "transfer-not-found") {
		t.Fatalf("a tool-level failure must surface with the upstream slug, got %v", err)
	}
	if !errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("transfer-not-found must satisfy ErrUpstreamNotFound, got %v", err)
	}
	if errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("not-found is not invalid input (ADR 0018), got %v", err)
	}
}

func TestNetworkInventoryPlanning_ListTransfers(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	out, err := c.ListTransfers(context.Background(), ports.ListTransfersRequest{State: "PICKED", Site: "WH1", Limit: 25})
	if err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}
	if up.lastList != (nipListTestIn{State: "PICKED", Site: "WH1", Limit: 25}) {
		t.Fatalf("tool received %+v", up.lastList)
	}
	if out.Total != 1 || len(out.Transfers) != 1 || out.Transfers[0].State != "PICKED" {
		t.Fatalf("unexpected page: %+v", out)
	}

	// Unset filters are omitted, never defaulted.
	if _, err := c.ListTransfers(context.Background(), ports.ListTransfersRequest{}); err != nil {
		t.Fatalf("ListTransfers (unfiltered): %v", err)
	}
	if up.lastList != (nipListTestIn{}) {
		t.Fatalf("empty filters must be omitted, got %+v", up.lastList)
	}

	_, err = c.ListTransfers(context.Background(), ports.ListTransfersRequest{State: "BOGUS"})
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) {
		t.Fatalf("invalid-query must classify as invalid input, got %v", err)
	}
}

func TestNetworkInventoryPlanning_FindStuckTransfers(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	out, err := c.FindStuckTransfers(context.Background(), ports.FindStuckTransfersRequest{OlderThanMinutes: 30, State: "ALLOCATING", Limit: 10})
	if err != nil {
		t.Fatalf("FindStuckTransfers: %v", err)
	}
	if up.lastStuck != (nipStuckTestIn{OlderThanMinutes: 30, State: "ALLOCATING", Limit: 10}) {
		t.Fatalf("tool received %+v", up.lastStuck)
	}
	if out.Total != 5 || len(out.Transfers) != 2 || out.Transfers[1].State != "IN_TRANSIT" {
		t.Fatalf("unexpected page: %+v", out)
	}

	// State and limit are omitted when unset; the threshold is always sent.
	if _, err := c.FindStuckTransfers(context.Background(), ports.FindStuckTransfersRequest{OlderThanMinutes: 15}); err != nil {
		t.Fatalf("FindStuckTransfers: %v", err)
	}
	if up.lastStuck != (nipStuckTestIn{OlderThanMinutes: 15}) {
		t.Fatalf("unset state/limit must be omitted, got %+v", up.lastStuck)
	}

	// A non-positive threshold is passed through unrepaired; NIP's
	// invalid-query rejection classifies as invalid input.
	_, err = c.FindStuckTransfers(context.Background(), ports.FindStuckTransfersRequest{OlderThanMinutes: 0})
	if !errors.Is(err, ports.ErrUpstreamInvalidInput) || !strings.Contains(err.Error(), "invalid-query") {
		t.Fatalf("a rejected threshold must surface as invalid input with the slug, got %v", err)
	}
}

func TestNetworkInventoryPlanning_SimulateTransferOptions(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	out, err := c.SimulateTransferOptions(context.Background())
	if err != nil {
		t.Fatalf("SimulateTransferOptions: %v", err)
	}
	if !out.Advisory || out.AsOf != "2026-10-06T10:00:00Z" || len(out.Sites) != 2 {
		t.Fatalf("unexpected simulation: %+v", out)
	}
	wh2 := out.Sites[1]
	if wh2.Site != "WH2" || wh2.OriginEnabled || !wh2.DestinationEnabled || wh2.TotalDemand != 90 || wh2.CapacityOverWindow != 60 ||
		wh2.CapacityHeadroom != -30 || wh2.WindowEnd != "2026-10-06T16:00:00Z" {
		t.Fatalf("unexpected site: %+v", wh2)
	}
}

func TestNetworkInventoryPlanning_OnlyReadToolsAreCalled(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	ctx := context.Background()
	_, _ = c.GetTransfer(ctx, "t-1")
	_, _ = c.ListTransfers(ctx, ports.ListTransfersRequest{})
	_, _ = c.FindStuckTransfers(ctx, ports.FindStuckTransfersRequest{OlderThanMinutes: 5})
	_, _ = c.SimulateTransferOptions(ctx)

	want := []string{"get_transfer", "list_transfers", "find_stuck_transfers", "simulate_transfer_options"}
	up.mu.Lock()
	defer up.mu.Unlock()
	if strings.Join(up.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("tools called = %v, want exactly %v", up.calls, want)
	}
}

func TestNetworkInventoryPlanning_UnreachableUpstream(t *testing.T) {
	up := newNIPTestUpstream(t)
	up.Close() // closed before use: every call must surface a connection error.

	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	ctx := context.Background()
	if _, err := c.GetTransfer(ctx, "t-1"); err == nil || errors.Is(err, ports.ErrUpstreamNotFound) {
		t.Fatalf("an unreachable upstream must surface as a plain error, got %v", err)
	}
	if _, err := c.ListTransfers(ctx, ports.ListTransfersRequest{}); err == nil {
		t.Fatal("an unreachable upstream must surface as an error")
	}
	if _, err := c.FindStuckTransfers(ctx, ports.FindStuckTransfersRequest{OlderThanMinutes: 5}); err == nil {
		t.Fatal("an unreachable upstream must surface as an error")
	}
	if _, err := c.SimulateTransferOptions(ctx); err == nil {
		t.Fatal("an unreachable upstream must surface as an error")
	}
}

func TestNetworkInventoryPlanning_RespectsCallerContext(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewNetworkInventoryPlanning(Config{Endpoint: up.URL})
	_, err := c.SimulateTransferOptions(ctx)
	if err == nil {
		t.Fatal("a cancelled context must abort the call")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected a cancellation error, got %v", err)
	}
}

func TestNetworkInventoryPlanning_NamesItselfInErrors(t *testing.T) {
	up := newNIPTestUpstream(t)
	defer up.Close()

	c := NewNetworkInventoryPlanning(Config{Name: "something-else", Endpoint: up.URL})
	_, err := c.GetTransfer(context.Background(), "ghost")
	if err == nil || !strings.HasPrefix(err.Error(), "network-inventory-planning: tool get_transfer reported an error: transfer-not-found:") {
		t.Fatalf("the client must name the upstream itself, got %v", err)
	}
}

func TestNetworkInventoryPlanning_ImplementsPort(t *testing.T) {
	var _ ports.NetworkInventoryPlanningClient = NewNetworkInventoryPlanning(Config{Endpoint: "http://example.invalid"})
}
