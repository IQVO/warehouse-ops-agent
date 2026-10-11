package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeNIPClient struct {
	detail ports.TransferDetail
	page   ports.TransferPage
	sim    ports.TransferSimulation
	err    error
}

func (f *fakeNIPClient) GetTransfer(context.Context, string) (ports.TransferDetail, error) {
	return f.detail, f.err
}
func (f *fakeNIPClient) ListTransfers(context.Context, ports.ListTransfersRequest) (ports.TransferPage, error) {
	return ports.TransferPage{}, errors.New("not used")
}
func (f *fakeNIPClient) FindStuckTransfers(context.Context, ports.FindStuckTransfersRequest) (ports.TransferPage, error) {
	return f.page, f.err
}
func (f *fakeNIPClient) SimulateTransferOptions(context.Context) (ports.TransferSimulation, error) {
	return f.sim, f.err
}

func newServerWithTransferWatch(t *testing.T, nip ports.NetworkInventoryPlanningClient) string {
	t.Helper()
	deps := inboundmcp.Deps{DailyBrief: &usecases.DailyBrief{}}
	if nip != nil {
		deps.TransferWatch = &usecases.TransferWatch{NIP: nip}
	}
	httpSrv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(deps)))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

var transferWatchTools = []string{"triage_stuck_transfers", "get_transfer_status", "explain_network_imbalance"}

func TestServer_TransferWatchTools_OnlyWhenWired(t *testing.T) {
	list := func(url string) map[string]*sdk.Tool {
		tools, err := connect(t, url).ListTools(context.Background(), nil)
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		byName := map[string]*sdk.Tool{}
		for _, tool := range tools.Tools {
			byName[tool.Name] = tool
		}
		return byName
	}

	for name := range list(newServerWithTransferWatch(t, nil)) {
		for _, tw := range transferWatchTools {
			if name == tw {
				t.Errorf("%s advertised although TransferWatch is not wired", tw)
			}
		}
	}

	wired := list(newServerWithTransferWatch(t, &fakeNIPClient{}))
	for _, tw := range transferWatchTools {
		tool, ok := wired[tw]
		if !ok {
			t.Errorf("%s not advertised although TransferWatch is wired", tw)
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be annotated read-only", tw)
		}
	}
}

func TestServer_TriageStuckTransfers_OverTheWire(t *testing.T) {
	nip := &fakeNIPClient{page: ports.TransferPage{Total: 1, Transfers: []ports.Transfer{
		{Id: "t1", State: "PICKED", SKU: "SKU-1", Quantity: 10, OriginSiteId: "WH1", DestinationSiteId: "WH2"},
	}}}
	session := connect(t, newServerWithTransferWatch(t, nip))

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "triage_stuck_transfers", Arguments: map[string]any{"olderThanMinutes": 30},
	})
	if err != nil || res.IsError {
		t.Fatalf("call: err=%v isError=%v content=%v", err, res != nil && res.IsError, res)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out struct {
		Total     int `json:"total"`
		Transfers []struct {
			Id     string `json:"id"`
			Triage struct {
				Cause string `json:"cause"`
			} `json:"triage"`
		} `json:"transfers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if out.Total != 1 || len(out.Transfers) != 1 || out.Transfers[0].Triage.Cause != "floor-work-not-progressing" {
		t.Fatalf("unexpected output: %s", raw)
	}
}

func TestServer_TransferWatchTools_ErrorsAreToolErrors(t *testing.T) {
	cases := map[string]struct {
		nip  *fakeNIPClient
		tool string
		args map[string]any
	}{
		"non-positive threshold": {nip: &fakeNIPClient{}, tool: "triage_stuck_transfers", args: map[string]any{"olderThanMinutes": 0}},
		"unknown transfer":       {nip: &fakeNIPClient{err: ports.ErrUpstreamNotFound}, tool: "get_transfer_status", args: map[string]any{"transferId": "nope"}},
		"fail-closed simulation": {nip: &fakeNIPClient{err: errors.New("read-models-incomplete")}, tool: "explain_network_imbalance", args: map[string]any{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			session := connect(t, newServerWithTransferWatch(t, c.nip))
			res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: c.tool, Arguments: c.args})
			if err != nil {
				t.Fatalf("transport error (want a tool-level error): %v", err)
			}
			if !res.IsError {
				t.Fatalf("%s: expected a tool-level error", name)
			}
		})
	}
}
