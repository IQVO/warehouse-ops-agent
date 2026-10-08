package mcpclient

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// InboundReceiving implements ports.InboundReceivingClient by calling
// inbound-receiving's seven published READ tools: get_asn, list_asns,
// get_appointment, list_appointments, get_receipt, list_receipts and
// list_docks (ADR 0021).
//
// inbound-receiving's MCP server is read-only by its own ADR 0005; this
// client is additionally held read-only from this side: the zero-write
// fitness test (internal/architecture/zerowrite) scans this package's
// tool-name literals, and TestInboundReceivingClientCallsOnlyPinnedTools
// allows exactly these seven names. The argument keys and result shapes are
// pinned against inbound-receiving's published registry in
// testdata/inbound_receiving_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// ADR 0018); the consuming use case decides what to do with it.
type InboundReceiving struct {
	session *Session
}

// NewInboundReceiving builds an InboundReceiving client for the given
// connection config.
func NewInboundReceiving(cfg Config) *InboundReceiving {
	cfg.Name = "inbound-receiving"
	return &InboundReceiving{session: New(cfg)}
}

var _ ports.InboundReceivingClient = (*InboundReceiving)(nil)

// GetAsn calls get_asn.
func (c *InboundReceiving) GetAsn(ctx context.Context, asnNumber string) (ports.InboundAsn, error) {
	var out ports.InboundAsn
	err := c.session.callTool(ctx, "get_asn", map[string]any{"asn_number": asnNumber}, &out)
	return out, err
}

// ListAsns calls list_asns. Zero query fields are omitted so
// inbound-receiving applies its own defaults.
func (c *InboundReceiving) ListAsns(ctx context.Context, query ports.InboundAsnListQuery) (ports.InboundAsnPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "state", query.State)
	var out ports.InboundAsnPage
	err := c.session.callTool(ctx, "list_asns", args, &out)
	return out, err
}

// GetAppointment calls get_appointment.
func (c *InboundReceiving) GetAppointment(ctx context.Context, appointmentID string) (ports.InboundAppointment, error) {
	var out ports.InboundAppointment
	err := c.session.callTool(ctx, "get_appointment", map[string]any{"appointment_id": appointmentID}, &out)
	return out, err
}

// ListAppointments calls list_appointments. Zero query fields (and zero
// times) are omitted; a set From/To is sent as an RFC 3339 instant in UTC.
func (c *InboundReceiving) ListAppointments(ctx context.Context, query ports.InboundAppointmentListQuery) (ports.InboundAppointmentPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "door", query.Door)
	setIfNotEmpty(args, "state", query.State)
	if !query.From.IsZero() {
		args["from"] = query.From.UTC().Format(time.RFC3339Nano)
	}
	if !query.To.IsZero() {
		args["to"] = query.To.UTC().Format(time.RFC3339Nano)
	}
	var out ports.InboundAppointmentPage
	err := c.session.callTool(ctx, "list_appointments", args, &out)
	return out, err
}

// GetReceipt calls get_receipt.
func (c *InboundReceiving) GetReceipt(ctx context.Context, receiptID string) (ports.InboundReceipt, error) {
	var out ports.InboundReceipt
	err := c.session.callTool(ctx, "get_receipt", map[string]any{"receipt_id": receiptID}, &out)
	return out, err
}

// ListReceipts calls list_receipts. Zero query fields are omitted.
func (c *InboundReceiving) ListReceipts(ctx context.Context, query ports.InboundReceiptListQuery) (ports.InboundReceiptPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "asn_number", query.AsnNumber)
	setIfNotEmpty(args, "state", query.State)
	var out ports.InboundReceiptPage
	err := c.session.callTool(ctx, "list_receipts", args, &out)
	return out, err
}

// ListDocks calls list_docks, which takes no arguments.
func (c *InboundReceiving) ListDocks(ctx context.Context) (ports.InboundDockList, error) {
	var out ports.InboundDockList
	err := c.session.callTool(ctx, "list_docks", map[string]any{}, &out)
	return out, err
}

// pageArgs starts a list call's argument map with the paging keys, omitting
// whichever is unset.
func pageArgs(limit int, cursor string) map[string]any {
	args := map[string]any{}
	if limit != 0 {
		args["limit"] = limit
	}
	setIfNotEmpty(args, "cursor", cursor)
	return args
}

func setIfNotEmpty(args map[string]any, key, value string) {
	if value != "" {
		args[key] = value
	}
}
