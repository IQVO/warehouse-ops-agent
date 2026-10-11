// Package ports (this file): the outbound port for the inbound-receiving
// bounded context (ADR 0021), the fleet's owner of the inbound dock side:
// advance ship notices (ASNs), carrier dock appointments and goods receipts.
//
// inbound-receiving's MCP server is READ-ONLY (its own ADR 0005: seven
// tools, every one annotated readOnlyHint, a write-verb sensor in its
// governance test). This port mirrors exactly those seven tools;
// inbound-receiving's writes (register/cancel an ASN, book/check in/cancel
// an appointment, open/receive/close a receipt) are REST-only there and have
// no method here. The zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient adapter
// ever names a write-verb tool, and the contract snapshot test in mcpclient
// pins the seven tools against inbound-receiving's published registry.
package ports

import (
	"context"
	"time"
)

// InboundReceivingClient is the outbound port for inbound-receiving's
// published read tools: get_asn, list_asns, get_appointment,
// list_appointments, get_receipt, list_receipts and list_docks.
type InboundReceivingClient interface {
	// GetAsn calls get_asn: one ASN by number.
	GetAsn(ctx context.Context, asnNumber string) (InboundAsn, error)
	// ListAsns calls list_asns: one page in ascending ASN-number order.
	ListAsns(ctx context.Context, query InboundAsnListQuery) (InboundAsnPage, error)
	// GetAppointment calls get_appointment: one dock appointment by id.
	GetAppointment(ctx context.Context, appointmentID string) (InboundAppointment, error)
	// ListAppointments calls list_appointments: one page in ascending
	// window-start order.
	ListAppointments(ctx context.Context, query InboundAppointmentListQuery) (InboundAppointmentPage, error)
	// GetReceipt calls get_receipt: one receipt by id.
	GetReceipt(ctx context.Context, receiptID string) (InboundReceipt, error)
	// ListReceipts calls list_receipts: one page in ascending id order.
	ListReceipts(ctx context.Context, query InboundReceiptListQuery) (InboundReceiptPage, error)
	// ListDocks calls list_docks: the inbound dock doors and the mode the
	// local copy runs in.
	ListDocks(ctx context.Context) (InboundDockList, error)
}

// InboundAsnListQuery is the argument set of list_asns. Every field is
// optional and is OMITTED from the call when zero, so inbound-receiving
// applies its own defaults (limit 100, first page, no filter); this client
// never invents one.
type InboundAsnListQuery struct {
	// Limit is the page size, 1..500 (inbound-receiving rejects anything
	// else with invalid-query). 0 omits it.
	Limit int
	// Cursor is the opaque next_cursor of the previous page.
	Cursor string
	// State keeps only ASNs in this state: Registered, Receiving, Closed
	// or Cancelled.
	State string
}

// InboundAppointmentListQuery is the argument set of list_appointments.
// Every field is optional and omitted when zero.
type InboundAppointmentListQuery struct {
	Limit  int
	Cursor string
	// Door keeps only appointments at this dock door code.
	Door string
	// State keeps only appointments in this state: Booked, CheckedIn,
	// Completed or Cancelled.
	State string
	// From keeps only appointments whose window ends after it; To keeps
	// only appointments whose window starts before it. The zero time omits
	// the bound; a set one is sent as an RFC 3339 instant in UTC.
	From time.Time
	To   time.Time
}

// InboundReceiptListQuery is the argument set of list_receipts. Every field
// is optional and omitted when zero.
type InboundReceiptListQuery struct {
	Limit  int
	Cursor string
	// AsnNumber keeps only receipts of this ASN.
	AsnNumber string
	// State keeps only receipts in this state: Open or Closed.
	State string
}

// --- inbound-receiving read-model DTOs --------------------------------------
//
// Field-for-field mirrors of inbound-receiving's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay the
// RFC 3339 strings the server publishes; the consuming use case parses them.
// The contract snapshot test in mcpclient checks every json tag below against
// the published output schemas in
// testdata/inbound_receiving_tools.golden.json.

// InboundAsnLine mirrors asnLineView.
type InboundAsnLine struct {
	LineNo      int    `json:"line_no"`
	Sku         string `json:"sku"`
	ExpectedQty int64  `json:"expected_qty"`
}

// InboundAsn mirrors asnView (get_asn, and each list_asns item).
// ExpectedArrival is empty when the supplier gave none; State is Registered,
// Receiving, Closed or Cancelled.
type InboundAsn struct {
	AsnNumber       string           `json:"asn_number"`
	SupplierRef     string           `json:"supplier_ref"`
	ExpectedArrival string           `json:"expected_arrival,omitempty"`
	State           string           `json:"state"`
	Lines           []InboundAsnLine `json:"lines"`
	Version         int64            `json:"version"`
}

// InboundAsnPage mirrors asnPageView. NextCursor is empty on the last page.
type InboundAsnPage struct {
	Items      []InboundAsn `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// InboundAppointment mirrors appointmentView (get_appointment, and each
// list_appointments item). WindowStart / WindowEnd are UTC instants; State is
// Booked, CheckedIn, Completed or Cancelled.
type InboundAppointment struct {
	AppointmentID string   `json:"appointment_id"`
	DoorCode      string   `json:"door_code"`
	Carrier       string   `json:"carrier"`
	WindowStart   string   `json:"window_start"`
	WindowEnd     string   `json:"window_end"`
	AsnNumbers    []string `json:"asn_numbers"`
	State         string   `json:"state"`
	Version       int64    `json:"version"`
}

// InboundAppointmentPage mirrors appointmentPageView.
type InboundAppointmentPage struct {
	Items      []InboundAppointment `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// InboundReceiptLine mirrors receiptLineView.
type InboundReceiptLine struct {
	LineNo          int    `json:"line_no"`
	Sku             string `json:"sku"`
	ExpectedQty     int64  `json:"expected_qty"`
	ReceivedGood    int64  `json:"received_good"`
	ReceivedDamaged int64  `json:"received_damaged"`
}

// InboundDiscrepancy mirrors discrepancyView. Kind is Short, Over or
// Damaged; the discrepancies are final once the receipt is Closed and
// provisional while it is Open (inbound-receiving's own judgement, never
// recomputed here).
type InboundDiscrepancy struct {
	LineNo      int    `json:"line_no"`
	Sku         string `json:"sku"`
	Kind        string `json:"kind"`
	ExpectedQty int64  `json:"expected_qty"`
	ReceivedQty int64  `json:"received_qty"`
	DamagedQty  int64  `json:"damaged_qty"`
}

// InboundReceipt mirrors receiptView (get_receipt, and each list_receipts
// item). AppointmentID and DoorCode are empty for a walk-in; ClosedAt is
// empty while the receipt is Open; State is Open or Closed.
type InboundReceipt struct {
	ReceiptID     string               `json:"receipt_id"`
	AsnNumber     string               `json:"asn_number"`
	AppointmentID string               `json:"appointment_id,omitempty"`
	DoorCode      string               `json:"door_code,omitempty"`
	State         string               `json:"state"`
	Lines         []InboundReceiptLine `json:"lines"`
	OpenedAt      string               `json:"opened_at"`
	ClosedAt      string               `json:"closed_at,omitempty"`
	Discrepancies []InboundDiscrepancy `json:"discrepancies"`
	Version       int64                `json:"version"`
}

// InboundReceiptPage mirrors receiptPageView.
type InboundReceiptPage struct {
	Items      []InboundReceipt `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// InboundDock mirrors dockView; DockFlow is Inbound or Both.
type InboundDock struct {
	DoorCode string `json:"door_code"`
	DockFlow string `json:"dock_flow"`
}

// InboundDockList mirrors dockListView. Mode is permissive (any door code can
// be booked, the list may be empty) or kafka (only the listed doors can be
// booked).
type InboundDockList struct {
	Mode  string        `json:"mode"`
	Items []InboundDock `json:"items"`
}
