package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeSiteStaffing struct {
	gaps map[string]ports.StaffingGap
}

func (f *fakeSiteStaffing) GetStaffingGapForSite(_ context.Context, _, _, pathId string) (ports.StaffingGap, error) {
	g, ok := f.gaps[pathId]
	if !ok {
		return ports.StaffingGap{}, errors.New("no such path")
	}
	return g, nil
}

func newOutlookServer(t *testing.T, gaps map[string]ports.StaffingGap) string {
	t.Helper()
	outlook, err := usecases.NewLaborReallocationOutlook(&fakeSiteStaffing{gaps: gaps})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{
		DailyBrief:               &usecases.DailyBrief{},
		LaborReallocationOutlook: outlook,
	})))
	t.Cleanup(srv.Close)
	return srv.URL
}

var outlookGaps = map[string]ports.StaffingGap{
	"pack": {PathId: "pack", PlannedHeads: 10, ActiveHeads: 7, Understaffed: true},
	"pick": {PathId: "pick", PlannedHeads: 5, ActiveHeads: 8},
}

const outlookArgs = `{"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1","fromPathId":"pick"},{"associateId":"a2","fromPathId":"pack"}]}`

func TestLaborReallocationOutlook_OnlyAdvertisedWhenWired(t *testing.T) {
	for _, tool := range listAllTools(t, inboundmcp.Deps{DailyBrief: &usecases.DailyBrief{}}) {
		if tool.Name == "labor_reallocation_outlook" {
			t.Fatal("the outlook tool is advertised without its use case")
		}
	}
	found := false
	for _, tool := range listAllTools(t, inboundmcp.Deps{DailyBrief: &usecases.DailyBrief{}, LaborReallocationOutlook: mustOutlook(t)}) {
		if tool.Name == "labor_reallocation_outlook" {
			found = true
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Error("the outlook tool must be annotated read-only")
			}
			if tool.Meta["com.warehouse/authority"] != "L2" {
				t.Errorf("authority = %v, want L2", tool.Meta["com.warehouse/authority"])
			}
		}
	}
	if !found {
		t.Fatal("the outlook tool is not advertised when wired")
	}
}

func mustOutlook(t *testing.T) *usecases.LaborReallocationOutlook {
	t.Helper()
	o, err := usecases.NewLaborReallocationOutlook(&fakeSiteStaffing{})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestLaborReallocationOutlook_ReturnsRankedMovesWithoutAToken(t *testing.T) {
	session := connect(t, newOutlookServer(t, outlookGaps))
	var args map[string]any
	_ = json.Unmarshal([]byte(outlookArgs), &args)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "labor_reallocation_outlook", Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Target  map[string]any `json:"target"`
		Moves   []map[string]any
		Skipped []map[string]any `json:"skipped"`
		Advis   string           `json:"advisory"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	moves, _ := generic["moves"].([]any)
	if len(moves) != 1 {
		t.Fatalf("moves = %v", generic["moves"])
	}
	m := moves[0].(map[string]any)
	if m["associateId"] != "a1" || m["fromPathId"] != "pick" || m["toPathId"] != "pack" || m["rank"] != float64(1) {
		t.Fatalf("move = %v", m)
	}
	if !strings.HasPrefix(m["nextCheck"].(string), "Read-only:") {
		t.Fatalf("nextCheck = %v", m["nextCheck"])
	}
	// No capability to act travels through this tool: no key of the output,
	// at any depth, names a token, a proposal id or an expiry.
	for _, key := range jsonKeys(generic) {
		switch strings.ToLower(key) {
		case "token", "proposalid", "expiresat", "approvedby":
			t.Errorf("the read-side output carries the key %q: %s", key, raw)
		}
	}
	if skipped, _ := generic["skipped"].([]any); len(skipped) != 1 {
		t.Fatalf("skipped = %v", generic["skipped"])
	}
	if !strings.Contains(generic["advisory"].(string), "no write path") {
		t.Fatalf("advisory = %v", generic["advisory"])
	}
}

func TestLaborReallocationOutlook_NotUnderstaffedIsAnEmptyAnswer(t *testing.T) {
	gaps := map[string]ports.StaffingGap{"pack": {PathId: "pack", PlannedHeads: 4, ActiveHeads: 4}, "pick": outlookGaps["pick"]}
	session := connect(t, newOutlookServer(t, gaps))
	var args map[string]any
	_ = json.Unmarshal([]byte(outlookArgs), &args)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "labor_reallocation_outlook", Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	generic := res.StructuredContent.(map[string]any)
	if moves, _ := generic["moves"].([]any); len(moves) != 0 {
		t.Fatalf("moves = %v, want none", moves)
	}
}

func TestLaborReallocationOutlook_RejectsBadInputAndUpstreamFailure(t *testing.T) {
	session := connect(t, newOutlookServer(t, outlookGaps))
	for name, args := range map[string]map[string]any{
		"no candidates": {"siteCode": "WH1", "shiftId": "S1", "toPathId": "pack", "candidates": []any{}},
		"no site":       {"shiftId": "S1", "toPathId": "pack", "candidates": []any{map[string]any{"associateId": "a1"}}},
		"unknown path":  {"siteCode": "WH1", "shiftId": "S1", "toPathId": "nope", "candidates": []any{map[string]any{"associateId": "a1"}}},
	} {
		res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "labor_reallocation_outlook", Arguments: args})
		if err == nil && (res == nil || !res.IsError) {
			t.Errorf("%s: want a tool error, got %+v", name, res)
		}
	}
}

func TestLaborReallocationOutlook_UnknownArgumentsAreRejected(t *testing.T) {
	session := connect(t, newOutlookServer(t, outlookGaps))
	args := map[string]any{"siteCode": "WH1", "shiftId": "S1", "toPathId": "pack", "approvedBy": "me",
		"candidates": []any{map[string]any{"associateId": "a1"}}}
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "labor_reallocation_outlook", Arguments: args})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("an approvedBy argument must be refused on the read side, got %+v", res)
	}
}

// jsonKeys lists every object key at any depth.
func jsonKeys(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			out = append(out, k)
			out = append(out, jsonKeys(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, jsonKeys(child)...)
		}
	}
	return out
}
