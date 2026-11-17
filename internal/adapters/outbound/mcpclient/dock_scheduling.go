package mcpclient

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// DockSchedulingReadTools is the exact set of dock-scheduling tools this
// agent may call or offer the model: the seven L1 read tools of the pinned
// registry, sorted. dock-scheduling's seven L3 write tools are deliberately
// absent (ADR 0032). TestDockScheduling_ReadToolsAreTheGoldenL1Set pins this
// list to the golden.
var DockSchedulingReadTools = []string{
	"get_appointment", "get_carrier", "get_dock_calendar", "get_door_availability",
	"list_appointments", "list_at_risk_appointments", "list_carriers",
}

// DockScheduling implements ports.DockSchedulingClient by calling
// dock-scheduling's seven published READ tools list_appointments,
// get_appointment, get_door_availability, get_dock_calendar, list_carriers,
// get_carrier and list_at_risk_appointments (every one L1, readOnlyHint).
//
// dock-scheduling's MCP server also publishes seven L3 write tools. This
// client never names any of them: the zero-write fitness test
// (internal/architecture/zerowrite) scans this file's tool-name literals
// against the exact read allow-list, and
// TestDockScheduling_CallsOnlyPinnedReadTools proves it on the wire. The
// argument keys and result shapes are pinned against dock-scheduling's
// published registry in testdata/dock_scheduling_tools.golden.json (ADR 0032).
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and no
// circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput, a
// *-not-found one ports.ErrUpstreamNotFound, ADR 0018). No use case
// consumes this client yet.
type DockScheduling struct {
	session *Session
}

// NewDockScheduling builds a DockScheduling client for the given connection
// config.
func NewDockScheduling(cfg Config) *DockScheduling {
	return &DockScheduling{session: NewNamedSession("dock-scheduling", cfg)}
}

// ListAppointments calls list_appointments; every zero filter is omitted.
func (c *DockScheduling) ListAppointments(ctx context.Context, q ports.DockAppointmentQuery) (ports.DockAppointmentPage, error) {
	args := pageArgs(q.Limit, q.Cursor)
	setIfNotEmpty(args, argSiteID, q.SiteID)
	setIfNotEmpty(args, "doorCode", q.DoorCode)
	setIfNotEmpty(args, argState, q.State)
	setIfNotEmpty(args, "direction", q.Direction)
	setIfNotEmpty(args, "carrierScac", q.CarrierSCAC)
	if q.AtRisk {
		args["atRisk"] = true
	}
	setInstantIfSet(args, argFrom, q.From)
	setInstantIfSet(args, argTo, q.To)
	var out ports.DockAppointmentPage
	err := c.session.callTool(ctx, "list_appointments", args, &out)
	return out, err
}

// GetAppointment calls get_appointment for one appointment id.
func (c *DockScheduling) GetAppointment(ctx context.Context, appointmentID string) (ports.DockAppointment, error) {
	var out ports.DockAppointment
	err := c.session.callTool(ctx, "get_appointment", map[string]any{"appointmentId": appointmentID}, &out)
	return out, err
}

// GetDoorAvailability calls get_door_availability for [from, to); a zero
// minutes is omitted so dock-scheduling applies the site's granularity.
func (c *DockScheduling) GetDoorAvailability(ctx context.Context, doorCode string, from, to time.Time, minutes int) (ports.DockDoorAvailability, error) {
	args := map[string]any{"doorCode": doorCode, argFrom: formatInstant(from), argTo: formatInstant(to)}
	if minutes > 0 {
		args["minutes"] = minutes
	}
	var out ports.DockDoorAvailability
	err := c.session.callTool(ctx, "get_door_availability", args, &out)
	return out, err
}

// GetDockCalendar calls get_dock_calendar for one site.
func (c *DockScheduling) GetDockCalendar(ctx context.Context, siteID string) (ports.DockCalendar, error) {
	var out ports.DockCalendar
	err := c.session.callTool(ctx, "get_dock_calendar", map[string]any{argSiteID: siteID}, &out)
	return out, err
}

// ListCarriers calls list_carriers; an empty status is omitted.
func (c *DockScheduling) ListCarriers(ctx context.Context, status string, limit int, cursor string) (ports.DockCarrierPage, error) {
	args := pageArgs(limit, cursor)
	setIfNotEmpty(args, "status", status)
	var out ports.DockCarrierPage
	err := c.session.callTool(ctx, "list_carriers", args, &out)
	return out, err
}

// GetCarrier calls get_carrier for one SCAC.
func (c *DockScheduling) GetCarrier(ctx context.Context, scac string) (ports.DockCarrier, error) {
	var out ports.DockCarrier
	err := c.session.callTool(ctx, "get_carrier", map[string]any{"scac": scac}, &out)
	return out, err
}

// ListAtRiskAppointments calls list_at_risk_appointments; an empty siteID
// is omitted.
func (c *DockScheduling) ListAtRiskAppointments(ctx context.Context, siteID string, limit int, cursor string) (ports.DockAppointmentPage, error) {
	args := pageArgs(limit, cursor)
	setIfNotEmpty(args, argSiteID, siteID)
	var out ports.DockAppointmentPage
	err := c.session.callTool(ctx, "list_at_risk_appointments", args, &out)
	return out, err
}
