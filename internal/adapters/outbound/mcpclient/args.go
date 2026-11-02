package mcpclient

// Argument keys of the pinned read tools this package calls, exactly as
// each upstream's published input schema names them (the contract
// snapshot tests pin the full wire shape against the registries in
// testdata/). Fleet goconst rule, 2026-11-02: a literal used three or
// more times becomes a constant.
const (
	argOrderID    = "orderId"
	argNodeID     = "nodeId"
	argPathID     = "pathId"
	argShipmentID = "shipmentId"
	argVendorID   = "vendorId"
)
