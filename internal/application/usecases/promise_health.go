// Package usecases: PromiseHealth (ADR 0024's A8 section) -- the daily
// brief's order-management promise-health line: get_promise_health over
// today so far, degraded to an explicit unavailable line when
// order-management cannot be read.
package usecases

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// PromiseHealth is the ADR 0024 read-only use case over order-management's
// get_promise_health tool. It asks for the window [start of today, now) --
// "today so far" in the agent's own time zone, the same day-bounds
// convention the inbound outlook uses -- and surfaces exactly what the
// tool returned; the tool has no site dimension, so neither does the
// section.
//
// A nil *PromiseHealth, or one with a nil OrderMgmt port, is "not
// configured" (ORDER_MANAGEMENT_MCP_ENDPOINT unset): Execute returns nil
// and the daily brief omits the section entirely. When configured, the
// section NEVER fails the brief: an unreachable order-management or a
// failed call degrades to an explicit unavailable line, never an error
// and never a silent all-clear.
type PromiseHealth struct {
	OrderMgmt ports.OrderManagementMCPClient
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Execute builds the promise-health section for today so far, or returns
// nil when the use case is not configured (see the type's doc comment).
func (uc *PromiseHealth) Execute(ctx context.Context, now time.Time) *policy.PromiseHealthSection {
	if uc == nil || uc.OrderMgmt == nil {
		return nil
	}
	from, _ := policy.DayBounds(now)
	section := &policy.PromiseHealthSection{From: from, To: now}

	reading, err := uc.OrderMgmt.GetPromiseHealth(ctx, ports.PromiseHealthQuery{From: from, To: now})
	if err != nil {
		section.Unavailable = append(section.Unavailable, "order-management get_promise_health: "+err.Error())
		return section
	}
	fillPromiseHealth(section, reading)
	return section
}

// fillPromiseHealth copies the published KPIs onto the section, exactly as
// read -- nothing is derived or defaulted here.
func fillPromiseHealth(section *policy.PromiseHealthSection, reading ports.PromiseHealth) {
	section.PromiseBasisCapability = reading.PromiseBasisCapability
	section.PromiseBasisLeadTime = reading.PromiseBasisLeadTime
	section.PromiseBasisNetwork = reading.PromiseBasisNetwork
	section.OrdersAllocatedTotal = reading.OrdersAllocatedTotal
	section.OrdersSplitShipment = reading.OrdersSplitShipment
	section.SplitShipmentRate = reading.SplitShipmentRate
	section.OrdersRepromised = reading.OrdersRepromised
	section.RepromiseRate = reading.RepromiseRate
	section.PromiseToCutoffGapSeconds = reading.PromiseToCutoffGapSeconds
}
