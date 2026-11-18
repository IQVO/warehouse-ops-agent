package mcpclient

import "time"

// Argument keys of the pinned read tools this package calls, exactly as
// each upstream's published input schema names them (the contract
// snapshot tests pin the full wire shape against the registries in
// testdata/). Fleet goconst rule, 2026-11-02: a literal used three or
// more times becomes a constant.
const (
	argOrderID       = "orderId"
	argNodeID        = "nodeId"
	argPathID        = "pathId"
	argShipmentID    = "shipmentId"
	argVendorID      = "vendorId"
	argCatalogItemID = "catalogItemId"
	argSiteID        = "siteId"
	argState         = "state"
	argTrailerID     = "trailerId"
	argFrom          = "from"
	argTo            = "to"
)

// formatInstant renders an instant the way every published tool takes one:
// UTC RFC 3339.
func formatInstant(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// setInstantIfSet sets an optional instant argument, omitting the zero time
// so the upstream applies its own default.
func setInstantIfSet(args map[string]any, key string, t time.Time) {
	if !t.IsZero() {
		args[key] = formatInstant(t)
	}
}
