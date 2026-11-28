package policy

import (
	"testing"
	"time"
)

func wesEdgeClock() time.Time {
	return time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
}

func openFact(requestID, siteID, sku, priority string, requestedAt time.Time) ReplenishmentRequestFact {
	return ReplenishmentRequestFact{RequestID: requestID, SiteID: siteID, SKU: sku, Priority: priority, RequestedAt: requestedAt}
}

func TestSummarizeOpenReplenishments_GroupsByPriorityAndFindsOldest(t *testing.T) {
	now := wesEdgeClock()
	facts := []ReplenishmentRequestFact{
		openFact("req-urgent-1", "WH1", "SKU-1", "urgent", now.Add(-30*time.Hour)),
		openFact("req-urgent-2", "WH2", "SKU-2", "urgent", now.Add(-5*time.Hour)),
		openFact("req-normal", "WH1", "SKU-3", "normal", now.Add(-1*time.Hour)),
	}
	side := SummarizeOpenReplenishments(facts, now)
	if side.Total != 3 {
		t.Fatalf("total = %d, want 3", side.Total)
	}
	if len(side.Open) != 2 || side.Open[0].Priority != "urgent" || side.Open[0].Count != 2 ||
		side.Open[1].Priority != "normal" || side.Open[1].Count != 1 {
		t.Fatalf("open counts = %+v, want urgent 2 then normal 1 (largest count first)", side.Open)
	}
	if side.Oldest == nil || side.Oldest.RequestID != "req-urgent-1" || side.Oldest.SiteID != "WH1" ||
		side.Oldest.SKU != "SKU-1" || side.Oldest.Priority != "urgent" || side.Oldest.Age != 30*time.Hour {
		t.Fatalf("the oldest pending request must carry id, site, sku, priority and age: %+v", side.Oldest)
	}
}

func TestSummarizeOpenReplenishments_OldestIsReplacedByOlderFact(t *testing.T) {
	now := wesEdgeClock()
	// The newer fact comes first, so the older fact must replace it — and
	// the newest fact, arriving last, must not.
	facts := []ReplenishmentRequestFact{
		openFact("req-newer", "WH1", "SKU-1", "urgent", now.Add(-2*time.Hour)),
		openFact("req-oldest", "WH1", "SKU-2", "normal", now.Add(-40*time.Hour)),
		openFact("req-newest", "WH1", "SKU-3", "normal", now.Add(-1*time.Hour)),
	}
	side := SummarizeOpenReplenishments(facts, now)
	if side.Oldest == nil || side.Oldest.RequestID != "req-oldest" || side.Oldest.Age != 40*time.Hour {
		t.Fatalf("the oldest pending request must be the 40h-old one, got %+v", side.Oldest)
	}
}

func TestSummarizeOpenReplenishments_ZeroTimeFactIsSkipped(t *testing.T) {
	now := wesEdgeClock()
	facts := []ReplenishmentRequestFact{
		openFact("req-broken", "WH1", "SKU-1", "urgent", time.Time{}),
		openFact("req-ok", "WH1", "SKU-2", "normal", now.Add(-3*time.Hour)),
	}
	side := SummarizeOpenReplenishments(facts, now)
	if side.Total != 1 || len(side.Open) != 1 || side.Open[0].Priority != "normal" || side.Open[0].Count != 1 {
		t.Fatalf("a zero-time fact must be skipped, got total %d counts %+v", side.Total, side.Open)
	}
	if side.Oldest == nil || side.Oldest.RequestID != "req-ok" {
		t.Fatalf("the zero-time fact must never be the oldest, got %+v", side.Oldest)
	}
}

func TestSummarizeOpenReplenishments_CountTieBreaksByPriorityName(t *testing.T) {
	now := wesEdgeClock()
	facts := []ReplenishmentRequestFact{
		openFact("req-1", "WH1", "SKU-1", "normal", now.Add(-1*time.Hour)),
		openFact("req-2", "WH1", "SKU-2", "high", now.Add(-2*time.Hour)),
	}
	side := SummarizeOpenReplenishments(facts, now)
	if len(side.Open) != 2 || side.Open[0].Priority != "high" || side.Open[1].Priority != "normal" {
		t.Fatalf("equal counts must break the tie by priority name (high before normal), got %+v", side.Open)
	}
}

func TestSummarizeOpenReplenishments_SortsScrambledCounts(t *testing.T) {
	now := wesEdgeClock()
	// Three groups, delivered in an order that already matches none of the
	// wanted one, so the insertion sort has to move elements.
	facts := []ReplenishmentRequestFact{
		openFact("req-b1", "WH1", "SKU-1", "normal", now.Add(-1*time.Hour)),
		openFact("req-c1", "WH1", "SKU-2", "low", now.Add(-2*time.Hour)),
		openFact("req-a1", "WH1", "SKU-3", "urgent", now.Add(-3*time.Hour)),
		openFact("req-a2", "WH1", "SKU-4", "urgent", now.Add(-4*time.Hour)),
		openFact("req-a3", "WH1", "SKU-5", "urgent", now.Add(-5*time.Hour)),
		openFact("req-c2", "WH1", "SKU-6", "low", now.Add(-6*time.Hour)),
	}
	side := SummarizeOpenReplenishments(facts, now)
	want := []string{"urgent", "low", "normal"}
	if len(side.Open) != len(want) {
		t.Fatalf("open counts = %+v, want three groups %+v", side.Open, want)
	}
	for i, priority := range want {
		if side.Open[i].Priority != priority {
			t.Fatalf("open counts = %+v, want %v (largest count first)", side.Open, want)
		}
	}
	if side.Open[0].Count != 3 || side.Open[1].Count != 2 || side.Open[2].Count != 1 {
		t.Fatalf("open counts = %+v, want counts 3, 2, 1", side.Open)
	}
	if side.Oldest == nil || side.Oldest.RequestID != "req-c2" {
		t.Fatalf("the oldest pending request must be the 6h-old one, got %+v", side.Oldest)
	}
}

func TestSummarizeOpenReplenishments_EmptyFactsYieldEmptySide(t *testing.T) {
	side := SummarizeOpenReplenishments(nil, wesEdgeClock())
	if side.Total != 0 || len(side.Open) != 0 || side.Oldest != nil {
		t.Fatalf("no facts must yield an empty side, got %+v", side)
	}
}
