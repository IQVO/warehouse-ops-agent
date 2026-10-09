package main_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Fakes for the optional MCP contexts: product-master,
// network-inventory-planning and inbound-receiving.

// -------------------------------------------------------- product-master ---

type fakeProductMaster struct {
	down         bool
	products     []ports.Product
	generated    int
	rejectCursor string
	calls        int
	lastQuery    ports.ProductListQuery
}

func (f *fakeProductMaster) ListProducts(_ context.Context, q ports.ProductListQuery) (ports.ProductPage, error) {
	f.calls++
	f.lastQuery = q
	if f.down {
		return ports.ProductPage{}, errUnreachable
	}
	if f.rejectCursor != "" && q.Cursor == f.rejectCursor {
		return ports.ProductPage{}, fmt.Errorf("malformed-request: cursor %q is not a cursor this server issued: %w", q.Cursor, ports.ErrUpstreamInvalidInput)
	}
	if f.generated > 0 {
		return f.generatedPage(q.Cursor), nil
	}
	items := make([]ports.Product, 0, len(f.products))
	for _, p := range f.products {
		if q.Classified != nil && (p.Classification != nil) != *q.Classified {
			continue
		}
		items = append(items, p)
	}
	return ports.ProductPage{Items: items}, nil
}

// generatedPage serves f.generated pages of one unclassified product each,
// chained by "page-N" cursors, so a scan can be driven past its per-request
// bound without a five-thousand-product fixture.
func (f *fakeProductMaster) generatedPage(cursor string) ports.ProductPage {
	idx := 0
	if n, ok := strings.CutPrefix(cursor, "page-"); ok {
		idx, _ = strconv.Atoi(n)
	}
	page := ports.ProductPage{Items: []ports.Product{{Sku: fmt.Sprintf("SKU-%02d", idx+1), Description: "generated", Version: 1}}}
	if idx+1 < f.generated {
		page.NextCursor = fmt.Sprintf("page-%d", idx+1)
	}
	return page
}

func (f *fakeProductMaster) GetProduct(context.Context, string) (ports.Product, error) {
	return ports.Product{}, errNotUsed
}

func (f *fakeProductMaster) GetProductClassification(context.Context, string) (ports.ProductClassificationReading, error) {
	return ports.ProductClassificationReading{}, errNotUsed
}

func (f *fakeProductMaster) GetPhysicalProfile(context.Context, string) (ports.PhysicalProfileReading, error) {
	return ports.PhysicalProfileReading{}, errNotUsed
}

// ------------------------------------------- network-inventory-planning ----

type fakeNIP struct {
	down      bool
	transfers []ports.Transfer
	details   map[string]ports.TransferDetail
	sites     []ports.SiteSimulation
	calls     int
	lastStuck ports.FindStuckTransfersRequest
}

func (f *fakeNIP) GetTransfer(_ context.Context, id string) (ports.TransferDetail, error) {
	f.calls++
	if f.down {
		return ports.TransferDetail{}, errUnreachable
	}
	d, ok := f.details[id]
	if !ok {
		return ports.TransferDetail{}, fmt.Errorf("transfer-not-found: %s: %w", id, ports.ErrUpstreamNotFound)
	}
	return d, nil
}

func (f *fakeNIP) ListTransfers(context.Context, ports.ListTransfersRequest) (ports.TransferPage, error) {
	return ports.TransferPage{}, errNotUsed
}

func (f *fakeNIP) FindStuckTransfers(_ context.Context, req ports.FindStuckTransfersRequest) (ports.TransferPage, error) {
	f.calls++
	f.lastStuck = req
	if f.down {
		return ports.TransferPage{}, errUnreachable
	}
	out := make([]ports.Transfer, 0, len(f.transfers))
	for _, t := range f.transfers {
		if req.State == "" || t.State == req.State {
			out = append(out, t)
		}
	}
	return ports.TransferPage{Transfers: out, Total: len(out)}, nil
}

func (f *fakeNIP) SimulateTransferOptions(context.Context) (ports.TransferSimulation, error) {
	f.calls++
	if f.down {
		return ports.TransferSimulation{}, errUnreachable
	}
	return ports.TransferSimulation{Advisory: true, AsOf: "2026-01-01T08:00:00Z", Sites: f.sites}, nil
}

// ------------------------------------------------------ inbound-receiving --

// fakeInbound serves the three list tools the inbound outlook reads. down
// fails all of them; receiptsDown fails only list_receipts.
type fakeInbound struct {
	down         bool
	receiptsDown bool
	asns         []ports.InboundAsn
	appointments []ports.InboundAppointment
	receipts     []ports.InboundReceipt
}

func (f *fakeInbound) ListAsns(_ context.Context, q ports.InboundAsnListQuery) (ports.InboundAsnPage, error) {
	if f.down {
		return ports.InboundAsnPage{}, errUnreachable
	}
	var out []ports.InboundAsn
	for _, a := range f.asns {
		if q.State == "" || a.State == q.State {
			out = append(out, a)
		}
	}
	return ports.InboundAsnPage{Items: out}, nil
}

func (f *fakeInbound) ListAppointments(context.Context, ports.InboundAppointmentListQuery) (ports.InboundAppointmentPage, error) {
	if f.down {
		return ports.InboundAppointmentPage{}, errUnreachable
	}
	return ports.InboundAppointmentPage{Items: f.appointments}, nil
}

func (f *fakeInbound) ListReceipts(_ context.Context, q ports.InboundReceiptListQuery) (ports.InboundReceiptPage, error) {
	if f.down || f.receiptsDown {
		return ports.InboundReceiptPage{}, errUnreachable
	}
	var out []ports.InboundReceipt
	for _, r := range f.receipts {
		if q.State == "" || r.State == q.State {
			out = append(out, r)
		}
	}
	return ports.InboundReceiptPage{Items: out}, nil
}

func (f *fakeInbound) GetAsn(context.Context, string) (ports.InboundAsn, error) {
	return ports.InboundAsn{}, errNotUsed
}

func (f *fakeInbound) GetAppointment(context.Context, string) (ports.InboundAppointment, error) {
	return ports.InboundAppointment{}, errNotUsed
}

func (f *fakeInbound) GetReceipt(context.Context, string) (ports.InboundReceipt, error) {
	return ports.InboundReceipt{}, errNotUsed
}

func (f *fakeInbound) ListDocks(context.Context) (ports.InboundDockList, error) {
	return ports.InboundDockList{}, errNotUsed
}
