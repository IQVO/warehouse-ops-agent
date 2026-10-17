// Package policy: promise-health section types (ADR 0024's A8 section).
//
// Pure value types for the daily brief's order-management promise-health
// line: order-management's get_promise_health KPIs for today so far. The
// reading is gathered one layer out
// (internal/application/usecases); this file only carries the shape.
package policy

import "time"

// PromiseHealthSection is the daily brief's order-management
// promise-health section: the promise KPIs order-management aggregated
// over [From, To). The fields are exactly what the tool published -- the
// promise basis distribution (capability / lead-time / network and their
// total), the re-promise and split-shipment counts with their rates, and
// the sample-weighted mean promise-to-cutoff gap in seconds. The tool has
// no site dimension, so neither does this section; Unavailable carries the
// explicit degradation line when order-management could not be read.
type PromiseHealthSection struct {
	From time.Time
	To   time.Time

	PromiseBasisCapability    int
	PromiseBasisLeadTime      int
	PromiseBasisNetwork       int
	OrdersAllocatedTotal      int
	OrdersSplitShipment       int
	SplitShipmentRate         float64
	OrdersRepromised          int
	RepromiseRate             float64
	PromiseToCutoffGapSeconds float64

	Unavailable []string
}
