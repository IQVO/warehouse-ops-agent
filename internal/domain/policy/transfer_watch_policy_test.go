package policy_test

import (
	"strings"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

func TestTransferState_Terminal(t *testing.T) {
	terminal := map[policy.TransferState]bool{
		policy.TransferReceived: true, policy.TransferUnfulfillable: true, policy.TransferCancelled: true,
		policy.TransferDraft: false, policy.TransferProposed: false, policy.TransferApproved: false,
		policy.TransferAllocating: false, policy.TransferAllocated: false, policy.TransferPicked: false,
		policy.TransferInTransit: false, policy.TransferArrived: false,
		"SOMETHING_NEW": false,
	}
	for state, want := range terminal {
		if got := state.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", state, got, want)
		}
	}
}

func TestParseStuckTransferState(t *testing.T) {
	for _, ok := range []string{"DRAFT", "PROPOSED", "APPROVED", "ALLOCATING", "ALLOCATED", "PICKED", "IN_TRANSIT", "ARRIVED"} {
		got, err := policy.ParseStuckTransferState(ok)
		if err != nil || string(got) != ok {
			t.Errorf("ParseStuckTransferState(%q) = %q, %v; want it accepted unchanged", ok, got, err)
		}
	}
	for _, bad := range []string{"", "RECEIVED", "UNFULFILLABLE", "CANCELLED", "allocating", "FLOATING"} {
		_, err := policy.ParseStuckTransferState(bad)
		if err == nil {
			t.Errorf("ParseStuckTransferState(%q) accepted; want rejected", bad)
			continue
		}
		if !strings.Contains(err.Error(), "ALLOCATING") {
			t.Errorf("error for %q should list the valid states, got %v", bad, err)
		}
	}
}

func sig(state string) policy.TransferSignal {
	return policy.TransferSignal{
		Id: "t1", State: state, SKU: "SKU-1", Quantity: 10, OriginSiteId: "WH1", DestinationSiteId: "WH2",
	}
}

func TestTriageTransfer_ByState(t *testing.T) {
	cases := []struct {
		state string
		cause policy.TransferCause
		check string // substring the next check must contain
	}{
		{"ALLOCATING", policy.CauseInventoryReplyMissing, "inventory-storage"},
		{"ALLOCATED", policy.CauseFloorWorkNotProgressing, "WH1"},
		{"PICKED", policy.CauseFloorWorkNotProgressing, "WH1"},
		{"IN_TRANSIT", policy.CauseDestinationReceiptMissing, "WH2"},
		{"ARRIVED", policy.CauseDestinationReceiptMissing, "WH2"},
		{"DRAFT", policy.CauseUnclassified, "get_transfer"},
		{"PROPOSED", policy.CauseUnclassified, "get_transfer"},
		{"APPROVED", policy.CauseUnclassified, "get_transfer"},
		{"BRAND_NEW_STATE", policy.CauseUnclassified, "get_transfer"},
	}
	for _, c := range cases {
		got := policy.TriageTransfer(sig(c.state))
		if got.Cause != c.cause {
			t.Errorf("%s: cause = %s, want %s", c.state, got.Cause, c.cause)
		}
		if got.Summary == "" || !strings.Contains(got.NextCheck, c.check) {
			t.Errorf("%s: summary=%q nextCheck=%q; next check must mention %q", c.state, got.Summary, got.NextCheck, c.check)
		}
	}
}

func TestTriageTransfer_UnknownStateQuotesTheRawState(t *testing.T) {
	got := policy.TriageTransfer(sig("BRAND_NEW_STATE"))
	if !strings.Contains(got.Summary, `"BRAND_NEW_STATE"`) {
		t.Fatalf("summary must quote the raw state, got %q", got.Summary)
	}
}

func TestTriageTransfer_FinishedStatesHaveNothingToDo(t *testing.T) {
	for _, state := range []string{"RECEIVED", "UNFULFILLABLE", "CANCELLED"} {
		got := policy.TriageTransfer(sig(state))
		if got.Cause != policy.CauseNone || got.NextCheck != "" || !strings.Contains(got.Summary, state) {
			t.Errorf("%s: %+v; want CauseNone, no next check, state in the summary", state, got)
		}
	}
	rejected := sig("UNFULFILLABLE")
	rejected.RejectionReason = "insufficient-usable-stock"
	if got := policy.TriageTransfer(rejected); !strings.HasSuffix(got.Summary, ": insufficient-usable-stock") {
		t.Fatalf("a rejection reason must be appended to the summary, got %q", got.Summary)
	}
}

func TestTriageTransfer_MentionsTheReservationOnlyWhenThereIsOne(t *testing.T) {
	with := sig("ALLOCATED")
	with.ReservationId = "res-9"
	if got := policy.TriageTransfer(with); !strings.Contains(got.NextCheck, "(reservation res-9)") {
		t.Errorf("next check should name the reservation: %q", got.NextCheck)
	}
	if got := policy.TriageTransfer(sig("ALLOCATED")); strings.Contains(got.NextCheck, "reservation") {
		t.Errorf("no reservation id, so none should be mentioned: %q", got.NextCheck)
	}
}

func TestTallyCauses_OrdersByCountThenName(t *testing.T) {
	got := policy.TallyCauses([]policy.TransferCause{
		policy.CauseFloorWorkNotProgressing, policy.CauseInventoryReplyMissing, policy.CauseFloorWorkNotProgressing,
		policy.CauseDestinationReceiptMissing, policy.CauseInventoryReplyMissing, policy.CauseFloorWorkNotProgressing,
	})
	want := []policy.CauseCount{
		{Cause: policy.CauseFloorWorkNotProgressing, Count: 3},
		{Cause: policy.CauseInventoryReplyMissing, Count: 2},
		{Cause: policy.CauseDestinationReceiptMissing, Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	tie := policy.TallyCauses([]policy.TransferCause{policy.CauseUnclassified, policy.CauseFloorWorkNotProgressing})
	if tie[0].Cause != policy.CauseFloorWorkNotProgressing || tie[1].Cause != policy.CauseUnclassified {
		t.Errorf("equal counts must order by cause name, got %+v", tie)
	}
	if empty := policy.TallyCauses(nil); len(empty) != 0 {
		t.Errorf("no causes => empty tally, got %+v", empty)
	}
}

func site(name string, headroom int, origin, dest bool) policy.SiteSimulationSignal {
	return policy.SiteSimulationSignal{
		Site: name, OriginEnabled: origin, DestinationEnabled: dest,
		TotalDemand: 100, CapacityOverWindow: float64(100 + headroom), CapacityHeadroom: headroom,
		WindowStart: "2026-10-06T08:00:00Z", WindowEnd: "2026-10-06T16:00:00Z",
	}
}

func siteOrder(n policy.NetworkImbalance) []string {
	out := make([]string, 0, len(n.Sites))
	for _, s := range n.Sites {
		out = append(out, s.Site)
	}
	return out
}

func TestExplainImbalance_NoSites(t *testing.T) {
	got := policy.ExplainImbalance(true, "2026-10-06T10:00:00Z", nil)
	if got.Imbalanced || len(got.Sites) != 0 || !strings.Contains(got.Summary, "no sites") || got.NextCheck == "" {
		t.Fatalf("got %+v", got)
	}
	if !got.Advisory || got.AsOf != "2026-10-06T10:00:00Z" {
		t.Fatalf("advisory flag and as-of must be echoed: %+v", got)
	}
}

func TestExplainImbalance_Balanced(t *testing.T) {
	got := policy.ExplainImbalance(true, "x", []policy.SiteSimulationSignal{site("WH1", 0, true, true), site("WH2", 40, true, true)})
	if got.Imbalanced || !strings.Contains(got.Summary, "no imbalance") || !strings.Contains(got.Summary, "2 site(s)") {
		t.Fatalf("got %+v", got)
	}
	for _, s := range got.Sites {
		if s.Balance != policy.SiteCovered {
			t.Errorf("%s balance = %s, want covered (zero headroom is covered, not short)", s.Site, s.Balance)
		}
	}
}

func TestExplainImbalance_OrdersShortFirstMostShortFirstThenMostHeadroom(t *testing.T) {
	got := policy.ExplainImbalance(true, "x", []policy.SiteSimulationSignal{
		site("C", 10, true, true), site("B", -20, true, true), site("D", 50, true, true),
		site("A", -80, true, true), site("E", 50, true, true),
	})
	want := []string{"A", "B", "D", "E", "C"}
	if order := siteOrder(got); strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v (short: most short first; covered: most headroom first; ties by site)", order, want)
	}
	if !got.Imbalanced {
		t.Fatal("a short site means imbalanced")
	}
}

func TestExplainImbalance_NamesShortSitesAndDonors(t *testing.T) {
	got := policy.ExplainImbalance(true, "x", []policy.SiteSimulationSignal{
		site("WH1", 460, true, true), site("WH2", -100, true, true),
	})
	if !strings.Contains(got.Summary, "WH2 (-100)") || !strings.Contains(got.Summary, "WH1 (+460)") {
		t.Fatalf("summary should name the short site and the donor with signed headroom: %q", got.Summary)
	}
	if !strings.Contains(got.NextCheck, "from WH1 (+460) toward WH2 (-100)") || !strings.Contains(got.NextCheck, "proposes no quantity") {
		t.Fatalf("next check should frame it as the operator's decision: %q", got.NextCheck)
	}
}

func TestExplainImbalance_NoDonor(t *testing.T) {
	got := policy.ExplainImbalance(true, "x", []policy.SiteSimulationSignal{
		site("WH1", 460, false, true), // headroom but cannot originate
		site("WH2", -100, true, true),
	})
	if !strings.Contains(got.Summary, "no site with headroom is enabled to originate") {
		t.Fatalf("summary: %q", got.Summary)
	}
	if !strings.Contains(got.NextCheck, "can be enabled to originate") {
		t.Fatalf("next check: %q", got.NextCheck)
	}
}

func TestExplainImbalance_ShortSiteThatCannotReceive(t *testing.T) {
	got := policy.ExplainImbalance(true, "x", []policy.SiteSimulationSignal{
		site("WH1", 460, true, true),
		site("WH2", -100, true, false),
	})
	if !strings.Contains(got.NextCheck, "WH2 (-100) cannot receive") {
		t.Fatalf("next check should flag the disabled destination first: %q", got.NextCheck)
	}
	for _, s := range got.Sites {
		if s.Site == "WH2" && !strings.Contains(s.Explanation, "not enabled to receive") {
			t.Errorf("WH2 explanation: %q", s.Explanation)
		}
	}
}

func TestExplainImbalance_SiteExplanations(t *testing.T) {
	got := policy.ExplainImbalance(true, "x", []policy.SiteSimulationSignal{
		site("DONOR", 30, true, true), site("NOORIGIN", 30, false, true), site("EVEN", 0, true, true), site("SHORT", -5, true, true),
	})
	by := map[string]string{}
	for _, s := range got.Sites {
		by[s.Site] = s.Explanation
	}
	cases := map[string]string{
		"DONOR":    "covered with headroom 30",
		"NOORIGIN": "not enabled to originate",
		"EVEN":     "covered with headroom 0",
		"SHORT":    "short by 5",
	}
	for name, want := range cases {
		if !strings.Contains(by[name], want) {
			t.Errorf("%s explanation %q should contain %q", name, by[name], want)
		}
	}
	if !strings.Contains(by["DONOR"], "enabled to originate transfers") || strings.Contains(by["DONOR"], "not enabled") {
		t.Errorf("DONOR must be enabled to originate: %q", by["DONOR"])
	}
	if strings.Contains(by["EVEN"], "originate") {
		t.Errorf("zero headroom says nothing about originating: %q", by["EVEN"])
	}
}

// A site exactly at zero headroom is covered but has nothing to give: it must
// never be offered as a donor (kills the headroom > 0 boundary mutant).
func TestExplainImbalance_ZeroHeadroomSiteIsNotADonor(t *testing.T) {
	got := policy.ExplainImbalance(true, "x", []policy.SiteSimulationSignal{
		site("EVEN", 0, true, true),
		site("SHORT", -10, true, true),
	})
	if strings.Contains(got.Summary, "EVEN") {
		t.Fatalf("a zero-headroom site must not be named as a donor: %q", got.Summary)
	}
	if !strings.Contains(got.Summary, "no site with headroom is enabled to originate") {
		t.Fatalf("with no real donor the summary must say so: %q", got.Summary)
	}
}
