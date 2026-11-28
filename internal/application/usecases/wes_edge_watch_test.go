package usecases_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func wesEdgeNow() time.Time {
	return time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
}

// countWant is one expected (priority, count) pair.
type countWant struct {
	priority string
	count    int
}

// oldestWant is the expected oldest-pending request.
type oldestWant struct {
	requestID, siteID, sku, priority string
	age                              time.Duration
}

// assertOpenCounts checks the half's total and its priority groups.
func assertOpenCounts(t *testing.T, side *policy.ReplenishmentWatchSide, total int, want []countWant) {
	t.Helper()
	if side.Total != total {
		t.Fatalf("total = %d, want %d (every Requested request)", side.Total, total)
	}
	if len(side.Open) != len(want) {
		t.Fatalf("open counts = %+v, want %+v (largest count first, ties by priority name)", side.Open, want)
	}
	for i, w := range want {
		if side.Open[i].Priority != w.priority || side.Open[i].Count != w.count {
			t.Fatalf("open counts = %+v, want %+v", side.Open, want)
		}
	}
}

// assertOldest checks the oldest pending request's every field.
func assertOldest(t *testing.T, side *policy.ReplenishmentWatchSide, want oldestWant) {
	t.Helper()
	if side.Oldest == nil {
		t.Fatal("the oldest pending request must be reported")
	}
	got := side.Oldest
	if got.RequestID != want.requestID || got.SiteID != want.siteID || got.SKU != want.sku ||
		got.Priority != want.priority || got.Age != want.age {
		t.Fatalf("the oldest pending request must carry id, site, sku, priority and age: %+v, want %+v", got, want)
	}
}

// openRequest builds one Requested replenishment request for the PFR fake.
func openRequest(t *testing.T, requestID, siteID, sku, priority string, requestedAt time.Time) ports.ReplenishmentRequest {
	t.Helper()
	return ports.ReplenishmentRequest{
		RequestID: requestID, SiteID: siteID, SKU: sku, ForwardBin: "F1", SourceBin: "R1",
		Quantity: 10, Priority: priority, State: "Requested", PolicyMin: 5, PolicyMax: 50,
		RequestedAt: rfc3339(t, requestedAt), Version: 1,
	}
}

// hintPage builds one hint page item count for the TI fake.
func hintPage(n int) ports.HintPage {
	items := make([]ports.InterleavingHint, n)
	for i := range items {
		items[i] = ports.InterleavingHint{HintID: "hint", StationID: "st-1", SiteID: "WH1", State: "Active"}
	}
	return ports.HintPage{Items: items}
}

func TestWesEdgeWatch_NotConfigured_ReturnsNilSection(t *testing.T) {
	var uc *usecases.WesEdgeWatch
	if got := uc.Execute(context.Background(), wesEdgeNow()); got != nil {
		t.Fatalf("a nil use case must produce no section, got %+v", got)
	}
	if got := (&usecases.WesEdgeWatch{}).Execute(context.Background(), wesEdgeNow()); got != nil {
		t.Fatalf("two nil ports must produce no section, got %+v", got)
	}
	// One configured port still produces a section: the other half is
	// omitted, never fatal.
	oneSided := &usecases.WesEdgeWatch{PFR: &fakePickFaceReplenishment{}}
	got := oneSided.Execute(context.Background(), wesEdgeNow())
	if got == nil {
		t.Fatal("a configured pick-face-replenishment port must produce the section")
	}
	if got.Replenishment == nil {
		t.Fatal("the replenishment half must be present when its port is configured")
	}
	if got.Interleaving != nil {
		t.Fatalf("the interleaving half must be omitted when its port is nil, got %+v", got.Interleaving)
	}
}

func TestWesEdgeWatch_CountsOpenRequestsByPriorityAndAgesTheOldest(t *testing.T) {
	now := wesEdgeNow()
	oldest := now.Add(-30 * time.Hour)
	mid := now.Add(-5 * time.Hour)
	fresh := now.Add(-1 * time.Hour)
	pfr := &fakePickFaceReplenishment{
		pages: map[string]ports.ReplenishmentPage{"": {Items: []ports.ReplenishmentRequest{
			openRequest(t, "req-old", "WH1", "SKU-1", "urgent", oldest),
			openRequest(t, "req-urgent-2", "WH2", "SKU-2", "urgent", mid),
			openRequest(t, "req-high", "WH1", "SKU-3", "high", fresh),
			openRequest(t, "req-normal", "WH2", "SKU-4", "normal", fresh),
		}}},
	}
	uc := &usecases.WesEdgeWatch{PFR: pfr}

	side := uc.Execute(context.Background(), now).Replenishment
	if side == nil {
		t.Fatal("the replenishment half must be present")
	}
	assertOpenCounts(t, side, 4, []countWant{{"urgent", 2}, {"high", 1}, {"normal", 1}})
	assertOldest(t, side, oldestWant{requestID: "req-old", siteID: "WH1", sku: "SKU-1", priority: "urgent", age: 30 * time.Hour})
	if len(side.Unavailable) != 0 {
		t.Fatalf("a healthy scan must carry no unavailable lines, got %v", side.Unavailable)
	}
	if pfr.lastQuery.State != "Requested" || pfr.lastQuery.Limit != 500 {
		t.Fatalf("the scan must filter to the Requested state at the 500 page size, got %+v", pfr.lastQuery)
	}
}

func TestWesEdgeWatch_BrokenRequestedAtIsDroppedPerRequest(t *testing.T) {
	now := wesEdgeNow()
	broken := openRequest(t, "req-broken", "WH1", "SKU-1", "urgent", now.Add(-3*time.Hour))
	broken.RequestedAt = "" // a Requested request without requested_at breaks the contract
	pfr := &fakePickFaceReplenishment{
		pages: map[string]ports.ReplenishmentPage{"": {Items: []ports.ReplenishmentRequest{
			broken,
			openRequest(t, "req-ok", "WH1", "SKU-2", "normal", now.Add(-2*time.Hour)),
		}}},
	}
	uc := &usecases.WesEdgeWatch{PFR: pfr}

	side := uc.Execute(context.Background(), now).Replenishment
	if side.Total != 1 {
		t.Fatalf("the broken request must be dropped from the counts, got total %d", side.Total)
	}
	if len(side.Unavailable) != 1 || !strings.Contains(side.Unavailable[0], "requested_at of request req-broken") {
		t.Fatalf("a request without requested_at must degrade to one unavailable line naming it, got %v", side.Unavailable)
	}
	if side.Oldest == nil || side.Oldest.RequestID != "req-ok" {
		t.Fatalf("the healthy request must still be summarized, got %+v", side.Oldest)
	}
}

func TestWesEdgeWatch_UnreachablePFRDegradesToOneLine(t *testing.T) {
	pfr := &fakePickFaceReplenishment{listErr: errors.New("connect: connection refused")}
	uc := &usecases.WesEdgeWatch{PFR: pfr}

	side := uc.Execute(context.Background(), wesEdgeNow()).Replenishment
	if side == nil {
		t.Fatal("an unreachable upstream must still produce the half")
	}
	if len(side.Unavailable) != 1 || !strings.Contains(side.Unavailable[0], "list_replenishments") || !strings.Contains(side.Unavailable[0], "connection refused") {
		t.Fatalf("an unreachable pick-face-replenishment must surface as exactly one explicit unavailable line, got %v", side.Unavailable)
	}
	if side.Total != 0 || side.Oldest != nil || len(side.Open) != 0 {
		t.Fatalf("nothing can be summarized when the list call failed, got %+v", side)
	}
}

func TestWesEdgeWatch_PFRPageBoundIsReportedAsLowerBound(t *testing.T) {
	now := wesEdgeNow()
	// Pages that always hand back another cursor: the 10-page bound is hit.
	pages := map[string]ports.ReplenishmentPage{}
	for cursor := strings.Repeat("x", 0); len(cursor) < 10; cursor += "x" {
		pages[cursor] = ports.ReplenishmentPage{
			Items:      []ports.ReplenishmentRequest{openRequest(t, "req-"+cursor, "WH1", "SKU-1", "urgent", now.Add(-time.Hour))},
			NextCursor: cursor + "x",
		}
	}
	pfr := &fakePickFaceReplenishment{pages: pages}
	uc := &usecases.WesEdgeWatch{PFR: pfr}

	side := uc.Execute(context.Background(), now).Replenishment
	found := false
	for _, line := range side.Unavailable {
		if strings.Contains(line, "lower bound") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reaching the page bound must add an explicit lower-bound line, got %v", side.Unavailable)
	}
	if side.Total != 10 {
		t.Fatalf("total = %d, want the 10 requests of the 10 bounded pages", side.Total)
	}
}

func TestWesEdgeWatch_CountsHintLifecycleStatesInLifecycleOrder(t *testing.T) {
	ti := &fakeTaskInterleaving{pagesByState: map[string]ports.HintPage{
		"Active|":     hintPage(2),
		"Consumed|":   hintPage(1),
		"Expired|":    hintPage(0),
		"Superseded|": hintPage(3),
	}}
	uc := &usecases.WesEdgeWatch{Interleaving: ti}

	side := uc.Execute(context.Background(), wesEdgeNow()).Interleaving
	if side == nil {
		t.Fatal("the interleaving half must be present")
	}
	want := []string{"Active", "Consumed", "Expired", "Superseded"}
	if len(side.Counts) != 4 {
		t.Fatalf("every lifecycle state must be counted, got %+v", side.Counts)
	}
	for i, state := range want {
		if side.Counts[i].State != state {
			t.Fatalf("counts must follow the lifecycle order %v, got %+v", want, side.Counts)
		}
	}
	if side.Counts[0].Count != 2 || side.Counts[1].Count != 1 || side.Counts[2].Count != 0 || side.Counts[3].Count != 3 {
		t.Fatalf("counts must be each state's own scan count (a zero is a fact), got %+v", side.Counts)
	}
	if len(side.Unavailable) != 0 {
		t.Fatalf("a healthy scan must carry no unavailable lines, got %v", side.Unavailable)
	}
	states := map[string]bool{}
	for _, q := range ti.queries {
		states[q.State] = true
		if q.Limit != 500 {
			t.Fatalf("each scan must use the 500 page size, got %+v", q)
		}
	}
	for _, state := range want {
		if !states[state] {
			t.Fatalf("each lifecycle state must be scanned, missing %s (queries %+v)", state, ti.queries)
		}
	}
}

func TestWesEdgeWatch_UnreachableTIDegradesPerState(t *testing.T) {
	ti := &fakeTaskInterleaving{listErr: map[string]error{
		"Active":     errors.New("connect: connection refused"),
		"Consumed":   errors.New("connect: connection refused"),
		"Expired":    errors.New("connect: connection refused"),
		"Superseded": errors.New("connect: connection refused"),
	}}
	uc := &usecases.WesEdgeWatch{Interleaving: ti}

	side := uc.Execute(context.Background(), wesEdgeNow()).Interleaving
	if len(side.Unavailable) != 4 {
		t.Fatalf("a fully unreachable task-interleaving must surface one line per state, got %v", side.Unavailable)
	}
	for _, line := range side.Unavailable {
		if !strings.Contains(line, "list_hints") || !strings.Contains(line, "connection refused") {
			t.Fatalf("each line must name list_hints and the error, got %q", line)
		}
	}
	if len(side.Counts) != 0 {
		t.Fatalf("a failed state scan must leave no count entry (never zero), got %+v", side.Counts)
	}
}

func TestWesEdgeWatch_TIPageBoundCountIsLowerBound(t *testing.T) {
	// Active's scan always hands back another cursor: the 10-page bound is
	// hit; the other states answer one healthy page.
	pages := map[string]ports.HintPage{
		"Consumed|":   hintPage(1),
		"Expired|":    hintPage(0),
		"Superseded|": hintPage(0),
	}
	for cursor := ""; len(cursor) < 10; cursor += "x" {
		pages["Active|"+cursor] = ports.HintPage{Items: []ports.InterleavingHint{{HintID: "h", State: "Active"}}, NextCursor: cursor + "x"}
	}
	ti := &fakeTaskInterleaving{pagesByState: pages}
	uc := &usecases.WesEdgeWatch{Interleaving: ti}

	side := uc.Execute(context.Background(), wesEdgeNow()).Interleaving
	found := false
	for _, c := range side.Counts {
		if c.State == "Active" {
			found = true
			if c.Count != 10 {
				t.Fatalf("the bounded Active scan must still report its 10 items as a lower bound, got %d", c.Count)
			}
		}
	}
	if !found {
		t.Fatalf("the Active entry must be present (its count is a lower bound), got %+v", side.Counts)
	}
	lower := false
	for _, line := range side.Unavailable {
		if strings.Contains(line, "Active") && strings.Contains(line, "lower bound") {
			lower = true
		}
	}
	if !lower {
		t.Fatalf("the bounded state must carry a lower-bound line naming it, got %v", side.Unavailable)
	}
}

func TestWesEdgeWatch_EachHalfIsIndependentlyFailOpen(t *testing.T) {
	now := wesEdgeNow()
	// The replenishment half is dead; the interleaving half is healthy.
	pfr := &fakePickFaceReplenishment{listErr: errors.New("connect: connection refused")}
	ti := &fakeTaskInterleaving{pagesByState: map[string]ports.HintPage{"Active|": hintPage(2)}}
	uc := &usecases.WesEdgeWatch{PFR: pfr, Interleaving: ti}

	section := uc.Execute(context.Background(), now)
	if section.Replenishment == nil || len(section.Replenishment.Unavailable) != 1 {
		t.Fatalf("the dead half must degrade to one unavailable line, got %+v", section.Replenishment)
	}
	if section.Interleaving == nil || len(section.Interleaving.Counts) != 4 || section.Interleaving.Counts[0].Count != 2 {
		t.Fatalf("the healthy half must be fully reported, got %+v", section.Interleaving)
	}
}
