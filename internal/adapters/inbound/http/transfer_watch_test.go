package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type stubNIP struct {
	detail ports.TransferDetail
	page   ports.TransferPage
	sim    ports.TransferSimulation
	err    error
}

func (s *stubNIP) GetTransfer(context.Context, string) (ports.TransferDetail, error) {
	return s.detail, s.err
}
func (s *stubNIP) ListTransfers(context.Context, ports.ListTransfersRequest) (ports.TransferPage, error) {
	return ports.TransferPage{}, errors.New("not used")
}
func (s *stubNIP) FindStuckTransfers(context.Context, ports.FindStuckTransfersRequest) (ports.TransferPage, error) {
	return s.page, s.err
}
func (s *stubNIP) SimulateTransferOptions(context.Context) (ports.TransferSimulation, error) {
	return s.sim, s.err
}

func nipRouter(nip ports.NetworkInventoryPlanningClient) http.Handler {
	h := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	if nip != nil {
		h.TransferWatch = &usecases.TransferWatch{NIP: nip}
	}
	return inboundhttp.NewRouter(h, "warehouse-ops-agent-test")
}

func get(t *testing.T, router http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestTransferWatchRoutesAnswer503WhenNotConfigured(t *testing.T) {
	router := nipRouter(nil)
	for _, target := range []string{
		"/transfer-watch/stuck?olderThanMinutes=30",
		"/transfer-watch/transfers/t1",
		"/transfer-watch/imbalance",
	} {
		if rec := get(t, router, target); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503; body %s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestGetStuckTransfers(t *testing.T) {
	nip := &stubNIP{page: ports.TransferPage{Total: 1, Transfers: []ports.Transfer{
		{Id: "t1", State: "ALLOCATING", SKU: "SKU-1", Quantity: 10, OriginSiteId: "WH1", DestinationSiteId: "WH2"},
	}}}
	rec := get(t, nipRouter(nip), "/transfer-watch/stuck?olderThanMinutes=30&state=ALLOCATING&limit=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Total     int `json:"total"`
		Transfers []struct {
			Id     string `json:"id"`
			Triage struct {
				Cause     string `json:"cause"`
				NextCheck string `json:"nextCheck"`
			} `json:"triage"`
		} `json:"transfers"`
		CauseCount []struct {
			Cause string `json:"cause"`
			Count int    `json:"count"`
		} `json:"causeCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 1 || len(body.Transfers) != 1 || body.Transfers[0].Triage.Cause != "inventory-reply-missing" ||
		body.Transfers[0].Triage.NextCheck == "" || len(body.CauseCount) != 1 || body.CauseCount[0].Count != 1 {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestGetStuckTransfersRejectsBadQuery(t *testing.T) {
	router := nipRouter(&stubNIP{})
	for _, target := range []string{
		"/transfer-watch/stuck",
		"/transfer-watch/stuck?olderThanMinutes=abc",
		"/transfer-watch/stuck?olderThanMinutes=0",
		"/transfer-watch/stuck?olderThanMinutes=5&limit=x",
		"/transfer-watch/stuck?olderThanMinutes=5&state=RECEIVED",
	} {
		if rec := get(t, router, target); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400; body %s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestGetTransferStatus(t *testing.T) {
	t.Run("200 with audit and triage", func(t *testing.T) {
		nip := &stubNIP{detail: ports.TransferDetail{
			Transfer: ports.Transfer{Id: "t1", State: "ARRIVED", SKU: "SKU-1", Quantity: 10, OriginSiteId: "WH1", DestinationSiteId: "WH2"},
			Audit:    []ports.TransferAuditEntry{{Seq: 1, To: "ARRIVED", Event: "TransferArrived"}},
		}}
		rec := get(t, nipRouter(nip), "/transfer-watch/transfers/t1")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Audit  []struct{ Event string } `json:"audit"`
			Triage struct{ Cause string }   `json:"triage"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Audit) != 1 || body.Triage.Cause != "destination-receipt-missing" {
			t.Fatalf("unexpected body: %s", rec.Body.String())
		}
	})
	t.Run("404 for an unknown transfer", func(t *testing.T) {
		rec := get(t, nipRouter(&stubNIP{err: ports.ErrUpstreamNotFound}), "/transfer-watch/transfers/nope")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
		}
	})
}

func TestGetNetworkImbalance(t *testing.T) {
	t.Run("200", func(t *testing.T) {
		nip := &stubNIP{sim: ports.TransferSimulation{Advisory: true, AsOf: "2026-10-07T00:00:00Z", Sites: []ports.SiteSimulation{
			{Site: "WH2", OriginEnabled: true, DestinationEnabled: true, TotalDemand: 400, CapacityHeadroom: -100},
		}}}
		rec := get(t, nipRouter(nip), "/transfer-watch/imbalance")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Imbalanced bool `json:"imbalanced"`
			Sites      []struct {
				Site    string `json:"site"`
				Balance string `json:"balance"`
			} `json:"sites"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !body.Imbalanced || len(body.Sites) != 1 || body.Sites[0].Balance != "short" {
			t.Fatalf("unexpected body: %s", rec.Body.String())
		}
	})
	t.Run("502 when NIP's simulation is fail-closed", func(t *testing.T) {
		rec := get(t, nipRouter(&stubNIP{err: errors.New("read-models-incomplete")}), "/transfer-watch/imbalance")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502; body %s", rec.Code, rec.Body.String())
		}
	})
}
