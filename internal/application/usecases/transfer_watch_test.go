package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeNIP struct {
	detail ports.TransferDetail
	page   ports.TransferPage
	sim    ports.TransferSimulation
	err    error

	stuckReq ports.FindStuckTransfersRequest
	calls    []string
}

func (f *fakeNIP) GetTransfer(_ context.Context, id string) (ports.TransferDetail, error) {
	f.calls = append(f.calls, "get_transfer:"+id)
	return f.detail, f.err
}

func (f *fakeNIP) ListTransfers(context.Context, ports.ListTransfersRequest) (ports.TransferPage, error) {
	f.calls = append(f.calls, "list_transfers")
	return ports.TransferPage{}, errors.New("transfer watch must not call list_transfers")
}

func (f *fakeNIP) FindStuckTransfers(_ context.Context, req ports.FindStuckTransfersRequest) (ports.TransferPage, error) {
	f.calls = append(f.calls, "find_stuck_transfers")
	f.stuckReq = req
	return f.page, f.err
}

func (f *fakeNIP) SimulateTransferOptions(context.Context) (ports.TransferSimulation, error) {
	f.calls = append(f.calls, "simulate_transfer_options")
	return f.sim, f.err
}

func transfer(id, state string) ports.Transfer {
	return ports.Transfer{Id: id, State: state, SKU: "SKU-1", Quantity: 10, OriginSiteId: "WH1", DestinationSiteId: "WH2"}
}

func TestTransferWatchNotConfigured(t *testing.T) {
	var nilUC *usecases.TransferWatch
	for name, uc := range map[string]*usecases.TransferWatch{"nil use case": nilUC, "nil port": {}} {
		t.Run(name, func(t *testing.T) {
			stuck, err := uc.StuckTransfers(context.Background(), 30, "", 0)
			if err != nil || stuck.Total != 0 || len(stuck.Transfers) != 0 {
				t.Fatalf("StuckTransfers = %+v, %v; want zero value, nil", stuck, err)
			}
			status, err := uc.TransferStatus(context.Background(), "t1")
			if err != nil || status.Detail.Id != "" {
				t.Fatalf("TransferStatus = %+v, %v; want zero value, nil", status, err)
			}
			imb, err := uc.NetworkImbalance(context.Background())
			if err != nil || len(imb.Sites) != 0 {
				t.Fatalf("NetworkImbalance = %+v, %v; want zero value, nil", imb, err)
			}
		})
	}
}

func TestStuckTransfersTriagesEachByState(t *testing.T) {
	nip := &fakeNIP{page: ports.TransferPage{Total: 3, Transfers: []ports.Transfer{
		transfer("a", "ALLOCATING"), transfer("b", "PICKED"), transfer("c", "IN_TRANSIT"),
	}}}
	uc := &usecases.TransferWatch{NIP: nip}

	got, err := uc.StuckTransfers(context.Background(), 45, "", 20)
	if err != nil {
		t.Fatalf("StuckTransfers: %v", err)
	}
	if nip.stuckReq.OlderThanMinutes != 45 || nip.stuckReq.Limit != 20 || nip.stuckReq.State != "" {
		t.Fatalf("request = %+v, want 45 minutes, limit 20, no state", nip.stuckReq)
	}
	want := map[string]policy.TransferCause{
		"a": policy.CauseInventoryReplyMissing,
		"b": policy.CauseFloorWorkNotProgressing,
		"c": policy.CauseDestinationReceiptMissing,
	}
	if got.Total != 3 || len(got.Transfers) != 3 {
		t.Fatalf("total=%d shown=%d, want 3/3", got.Total, len(got.Transfers))
	}
	for _, tr := range got.Transfers {
		if tr.Triage.Cause != want[tr.Transfer.Id] {
			t.Errorf("transfer %s cause = %s, want %s", tr.Transfer.Id, tr.Triage.Cause, want[tr.Transfer.Id])
		}
		if tr.Triage.NextCheck == "" {
			t.Errorf("transfer %s has no next check", tr.Transfer.Id)
		}
	}
	if len(got.CauseCount) != 3 {
		t.Fatalf("cause tally = %+v, want 3 distinct causes", got.CauseCount)
	}
	if len(nip.calls) != 1 || nip.calls[0] != "find_stuck_transfers" {
		t.Fatalf("calls = %v, want exactly find_stuck_transfers", nip.calls)
	}
}

func TestStuckTransfersPassesNormalisedState(t *testing.T) {
	nip := &fakeNIP{}
	uc := &usecases.TransferWatch{NIP: nip}
	if _, err := uc.StuckTransfers(context.Background(), 10, "ALLOCATING", 0); err != nil {
		t.Fatalf("StuckTransfers: %v", err)
	}
	if nip.stuckReq.State != "ALLOCATING" {
		t.Fatalf("state sent = %q, want ALLOCATING", nip.stuckReq.State)
	}
}

func TestStuckTransfersRejectsBadInputBeforeCalling(t *testing.T) {
	cases := map[string]struct {
		minutes int
		state   string
		limit   int
	}{
		"zero minutes":     {minutes: 0},
		"negative minutes": {minutes: -5},
		"negative limit":   {minutes: 5, limit: -1},
		"limit too large":  {minutes: 5, limit: 201},
		"terminal state":   {minutes: 5, state: "RECEIVED"},
		"unknown state":    {minutes: 5, state: "FLOATING"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			nip := &fakeNIP{}
			uc := &usecases.TransferWatch{NIP: nip}
			_, err := uc.StuckTransfers(context.Background(), c.minutes, c.state, c.limit)
			if !errors.Is(err, usecases.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if len(nip.calls) != 0 {
				t.Fatalf("NIP was called %v despite invalid input", nip.calls)
			}
		})
	}
}

func TestStuckTransfersUpstreamErrors(t *testing.T) {
	t.Run("transport failure stays a plain upstream error", func(t *testing.T) {
		boom := errors.New("connection refused")
		uc := &usecases.TransferWatch{NIP: &fakeNIP{err: boom}}
		_, err := uc.StuckTransfers(context.Background(), 5, "", 0)
		if !errors.Is(err, boom) || errors.Is(err, usecases.ErrInvalidInput) {
			t.Fatalf("err = %v, want the upstream error, not ErrInvalidInput", err)
		}
	})
	t.Run("rejected input becomes the caller's invalid input", func(t *testing.T) {
		uc := &usecases.TransferWatch{NIP: &fakeNIP{err: ports.ErrUpstreamInvalidInput}}
		_, err := uc.StuckTransfers(context.Background(), 5, "", 0)
		if !errors.Is(err, usecases.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
}

func TestTransferStatus(t *testing.T) {
	t.Run("triages the transfer and keeps its audit trail", func(t *testing.T) {
		nip := &fakeNIP{detail: ports.TransferDetail{
			Transfer: transfer("t1", "ARRIVED"),
			Audit:    []ports.TransferAuditEntry{{Seq: 1, To: "ARRIVED", Event: "TransferArrived"}},
		}}
		got, err := (&usecases.TransferWatch{NIP: nip}).TransferStatus(context.Background(), "t1")
		if err != nil {
			t.Fatalf("TransferStatus: %v", err)
		}
		if got.Triage.Cause != policy.CauseDestinationReceiptMissing || len(got.Detail.Audit) != 1 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a finished transfer has nothing to triage", func(t *testing.T) {
		nip := &fakeNIP{detail: ports.TransferDetail{Transfer: transfer("t2", "RECEIVED")}}
		got, err := (&usecases.TransferWatch{NIP: nip}).TransferStatus(context.Background(), "t2")
		if err != nil || got.Triage.Cause != policy.CauseNone || got.Triage.NextCheck != "" {
			t.Fatalf("got %+v, %v; want CauseNone and no next check", got, err)
		}
	})
	t.Run("unknown id", func(t *testing.T) {
		uc := &usecases.TransferWatch{NIP: &fakeNIP{err: ports.ErrUpstreamNotFound}}
		_, err := uc.TransferStatus(context.Background(), "nope")
		if !errors.Is(err, usecases.ErrTransferNotFound) {
			t.Fatalf("err = %v, want ErrTransferNotFound", err)
		}
	})
	t.Run("empty id is invalid input and never reaches NIP", func(t *testing.T) {
		nip := &fakeNIP{}
		_, err := (&usecases.TransferWatch{NIP: nip}).TransferStatus(context.Background(), "")
		if !errors.Is(err, usecases.ErrInvalidInput) || len(nip.calls) != 0 {
			t.Fatalf("err = %v calls = %v", err, nip.calls)
		}
	})
}

func TestNetworkImbalance(t *testing.T) {
	t.Run("names the short site and the donor", func(t *testing.T) {
		nip := &fakeNIP{sim: ports.TransferSimulation{Advisory: true, AsOf: "2026-10-07T00:00:00Z", Sites: []ports.SiteSimulation{
			{Site: "WH1", OriginEnabled: true, DestinationEnabled: true, TotalDemand: 40, CapacityHeadroom: 460},
			{Site: "WH2", OriginEnabled: true, DestinationEnabled: true, TotalDemand: 400, CapacityHeadroom: -100},
		}}}
		got, err := (&usecases.TransferWatch{NIP: nip}).NetworkImbalance(context.Background())
		if err != nil {
			t.Fatalf("NetworkImbalance: %v", err)
		}
		if !got.Imbalanced || len(got.Sites) != 2 || got.Sites[0].Site != "WH2" || got.Sites[0].Balance != policy.SiteShort {
			t.Fatalf("got %+v; want WH2 short first", got)
		}
		if got.Sites[1].Balance != policy.SiteCovered {
			t.Fatalf("second site balance = %s, want covered", got.Sites[1].Balance)
		}
	})
	t.Run("a fail-closed simulation is an upstream error, never an empty reading", func(t *testing.T) {
		boom := errors.New("read-models-incomplete")
		_, err := (&usecases.TransferWatch{NIP: &fakeNIP{err: boom}}).NetworkImbalance(context.Background())
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the upstream error", err)
		}
	})
}
