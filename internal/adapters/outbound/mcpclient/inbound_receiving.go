package mcpclient

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// InboundReceiving implements ports.InboundReceivingClient by calling
// inbound-receiving's published READ tools list_asns, list_appointments and
// list_receipts (ADR 0021). The other four published read tools (get_asn,
// get_appointment, get_receipt, list_docks) are deliberately not wired: no
// use case calls them.
//
// inbound-receiving's MCP server is read-only by its own ADR 0005; this
// client is additionally held read-only from this side: the zero-write
// fitness test (internal/architecture/zerowrite) scans this package's
// tool-name literals, and TestInboundReceivingClientCallsOnlyPinnedTools
// allows exactly these three names. The argument keys and result shapes are
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

// ListAsns calls list_asns. Zero query fields are omitted so
// inbound-receiving applies its own defaults.
func (c *InboundReceiving) ListAsns(ctx context.Context, query ports.InboundAsnListQuery) (ports.InboundAsnPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "state", query.State)
	var out ports.InboundAsnPage
	err := c.session.callTool(ctx, "list_asns", args, &out)
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

// ListReceipts calls list_receipts. Zero query fields are omitted.
func (c *InboundReceiving) ListReceipts(ctx context.Context, query ports.InboundReceiptListQuery) (ports.InboundReceiptPage, error) {
	args := pageArgs(query.Limit, query.Cursor)
	setIfNotEmpty(args, "asn_number", query.AsnNumber)
	setIfNotEmpty(args, "state", query.State)
	var out ports.InboundReceiptPage
	err := c.session.callTool(ctx, "list_receipts", args, &out)
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
