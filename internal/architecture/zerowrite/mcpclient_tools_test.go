package zerowrite

import (
	"go/ast"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// writeToolPrefixes are the verb prefixes of tools that mutate upstream
// state. warehouse-planning's MCP server is READ+WRITE (register_*,
// create_*, publish_*, declare_*), so unlike the read-only upstreams the
// zero-write rule for it cannot lean on the server: the mcpclient package
// itself must never name such a tool (ADR 0013). The list is deliberately
// broader than planning's own verbs so it also guards the other contexts'
// write tools (assign_labor, revoke_reservation, ...). classify_ and record_
// cover product-master's REST-only writes (classify a product, record a
// measurement; ADR 0020) should they ever be published as tools; retire_
// covers its published retire_pack_level write (declare_pack_level is
// caught by declare_). book_,
// check_in_, open_, receive_ and close_ cover inbound-receiving's REST-only
// write verbs (ADR 0021) on the same terms.
var writeToolPrefixes = []string{
	"create_", "publish_", "register_", "declare_", "assign_", "release_",
	"revoke_", "update_", "delete_", "cancel_", "set_", "add_", "remove_",
	"submit_", "reserve_", "activate_", "deactivate_", "classify_", "record_",
	"book_", "check_in_", "open_", "receive_", "close_", "retire_",
}

// productMasterReadTools is the exact allow-list for the product-master
// client (ADR 0020): the one of its six published read tools the
// master-data-gaps use case consumes (it also publishes two pack-level
// write tools, which are never callable from here). A prefix scan alone would let a new
// non-prefixed verb through; this pins the whole set.
var productMasterReadTools = []string{"list_products"}

// TestProductMasterClientCallsOnlyPinnedTools scans product_master.go and
// fails unless it names exactly the one pinned read tool.
func TestProductMasterClientCallsOnlyPinnedTools(t *testing.T) {
	path := "../../adapters/outbound/mcpclient/product_master.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := calledToolNames(parseGoFile(t, path, src))
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(productMasterReadTools, ",") {
		t.Fatalf("%s calls %v; the product-master client may call exactly %v (read-only, ADR 0020)", path, names, productMasterReadTools)
	}
}

// inboundReceivingReadTools is the exact allow-list for the inbound-receiving
// client (ADR 0021): the three of its seven published read tools the inbound
// outlook consumes. inbound-receiving's
// writes (register/cancel an ASN, book/check in/cancel an appointment,
// open/receive/close a receipt) are REST-only and have no tool.
var inboundReceivingReadTools = []string{"list_appointments", "list_asns", "list_receipts"}

// TestInboundReceivingClientCallsOnlyPinnedTools scans inbound_receiving.go
// and fails unless it names exactly the three pinned read tools.
func TestInboundReceivingClientCallsOnlyPinnedTools(t *testing.T) {
	path := "../../adapters/outbound/mcpclient/inbound_receiving.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := calledToolNames(parseGoFile(t, path, src))
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(inboundReceivingReadTools, ",") {
		t.Fatalf("%s calls %v; the inbound-receiving client may call exactly %v (read-only, ADR 0021)", path, names, inboundReceivingReadTools)
	}
	for _, name := range names {
		if prefix := writePrefixOf(name); prefix != "" {
			t.Errorf("%s: tool %q starts with write verb %q", path, name, prefix)
		}
	}
}

// slottingOptimizationReadTools is the exact allow-list for the
// slotting-optimization client (ADR 0024): the two of its four published
// read tools the slot-plan watch consumes. get_forward_slots and
// get_sku_velocity are deliberately NOT pinned — wiring tools no use case
// calls is the bug class this change closes (gap A14).
var slottingOptimizationReadTools = []string{"get_slot_plan", "list_slot_plans"}

// TestSlottingOptimizationClientCallsOnlyPinnedTools scans
// slotting_optimization.go and fails unless it names exactly the two pinned
// read tools.
func TestSlottingOptimizationClientCallsOnlyPinnedTools(t *testing.T) {
	path := "../../adapters/outbound/mcpclient/slotting_optimization.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := calledToolNames(parseGoFile(t, path, src))
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(slottingOptimizationReadTools, ",") {
		t.Fatalf("%s calls %v; the slotting-optimization client may call exactly %v (read-only, ADR 0024)", path, names, slottingOptimizationReadTools)
	}
	for _, name := range names {
		if prefix := writePrefixOf(name); prefix != "" {
			t.Errorf("%s: tool %q starts with write verb %q", path, name, prefix)
		}
	}
}

// returnsProcessingReadTools is the exact allow-list for the
// returns-processing client: all three of its published read tools
// (the whole surface; returns-processing's writes are REST-only).
var returnsProcessingReadTools = []string{"get_disposition_policy", "get_return", "list_returns_by_order"}

// TestReturnsProcessingClientCallsOnlyPinnedTools scans
// returns_processing.go and fails unless it names exactly the three pinned
// read tools.
func TestReturnsProcessingClientCallsOnlyPinnedTools(t *testing.T) {
	path := "../../adapters/outbound/mcpclient/returns_processing.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := calledToolNames(parseGoFile(t, path, src))
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(returnsProcessingReadTools, ",") {
		t.Fatalf("%s calls %v; the returns-processing client may call exactly %v (read-only)", path, names, returnsProcessingReadTools)
	}
	for _, name := range names {
		if prefix := writePrefixOf(name); prefix != "" {
			t.Errorf("%s: tool %q starts with write verb %q", path, name, prefix)
		}
	}
}

// exceptionManagementReadTools is the exact allow-list for the
// exception-management client: all four of its published read tools
// (the whole surface; exception-management's writes are REST-only).
var exceptionManagementReadTools = []string{"get_exception", "get_exception_policies", "get_sla_report", "list_open_exceptions"}

// TestExceptionManagementClientCallsOnlyPinnedTools scans
// exception_management.go and fails unless it names exactly the four pinned
// read tools.
func TestExceptionManagementClientCallsOnlyPinnedTools(t *testing.T) {
	path := "../../adapters/outbound/mcpclient/exception_management.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := calledToolNames(parseGoFile(t, path, src))
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(exceptionManagementReadTools, ",") {
		t.Fatalf("%s calls %v; the exception-management client may call exactly %v (read-only)", path, names, exceptionManagementReadTools)
	}
	for _, name := range names {
		if prefix := writePrefixOf(name); prefix != "" {
			t.Errorf("%s: tool %q starts with write verb %q", path, name, prefix)
		}
	}
}

// --- the ADR-0025 clients (network fleet + two WMS ones) and the
// ADR-0026 process-path-management client -------
//
// Each is the exact allow-list for one of the clients wired per
// ADR 0025/0026: the whole of its upstream's published read surface (every
// one of these upstreams is a read-only server).

var networkFleetReadTools = map[string][]string{
	"customer_ordering.go":       {"get_order", "get_order_tracking", "get_ship_to"},
	"delivery_promise.go":        {"get_current_policy", "get_delivery_offer", "get_promise", "list_promise_books", "list_promises_by_order"},
	"last_mile_delivery.go":      {"get_route", "get_wave_stops", "list_waves"},
	"linehaul_transportation.go": {"get_departure", "list_departures"},
	"node_performance.go":        {"get_scorecard", "list_scorecards_by_grade"},
	"node_topology.go":           {"get_node", "get_region", "list_lanes", "list_nodes", "list_regions", "resolve_service_area"},
	"offer_availability.go":      {"browse_catalog", "get_catalog_item", "get_inventory_feed", "list_listings"},
	"order_sourcing.go":          {"get_assignment", "list_assignments"},
	"shipping_and_tracking.go":   {"get_journey"},
	"vendor_connectivity.go":     {"get_inbound_shipment", "get_node_scorecard", "get_purchase_order", "get_transaction", "get_vendor", "get_vendor_node", "list_vendor_nodes", "poll_inbound_shipments", "poll_purchase_orders"},
	"task_interleaving.go":       {"get_hint", "get_site_policy", "get_station", "list_hints"},
	"pick_face_replenishment.go": {"get_policy", "get_replenishment", "list_pick_faces", "list_policies", "list_replenishments"},
	"process_path_management.go": {"get_cpt_schedule", "get_process_path", "list_process_paths"},
}

// TestNetworkFleetClientsCallOnlyPinnedTools scans each of the
// ADR-0025/0026 client files and fails unless it names exactly its pinned
// read tools, and only read-verb-free ones at that.
func TestNetworkFleetClientsCallOnlyPinnedTools(t *testing.T) {
	for file, want := range networkFleetReadTools {
		path := "../../adapters/outbound/mcpclient/" + file
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		names := calledToolNames(parseGoFile(t, path, src))
		sort.Strings(names)
		sortedWant := append([]string(nil), want...)
		sort.Strings(sortedWant)
		if strings.Join(names, ",") != strings.Join(sortedWant, ",") {
			t.Errorf("%s calls %v; this client may call exactly %v (read-only, ADR 0025)", path, names, sortedWant)
		}
		for _, name := range names {
			if prefix := writePrefixOf(name); prefix != "" {
				t.Errorf("%s: tool %q starts with write verb %q", path, name, prefix)
			}
		}
	}
}

// TestMCPClientsCallOnlyReadTools statically scans every non-test source
// file in the mcpclient package for `callTool(ctx, "<tool>", ...)` calls
// and fails when a tool-name literal starts with a write verb.
func TestMCPClientsCallOnlyReadTools(t *testing.T) {
	dir := "../../adapters/outbound/mcpclient"
	seen := 0
	for _, path := range goFilesIn(t, dir) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		file := parseGoFile(t, path, src)
		for _, name := range calledToolNames(file) {
			seen++
			if prefix := writePrefixOf(name); prefix != "" {
				t.Errorf("%s: calls tool %q (write verb %q) — outbound MCP clients are read-only (zero write capability, ADR-0004/v1, ADR 0013)", path, name, prefix)
			}
		}
	}
	if seen == 0 {
		t.Fatal("found zero callTool(...) tool-name literals — this test's AST walk is broken, or the clients moved; fix the scan before trusting it")
	}
}

// TestWritePrefixDetector pins the detector itself, so the scan above
// cannot silently stop recognising write tools.
func TestWritePrefixDetector(t *testing.T) {
	for _, name := range []string{"create_capacity_plan", "publish_capacity_plan", "register_process_path", "declare_station_standard", "assign_labor", "revoke_reservation", "classify_product", "record_measurement", "register_asn", "cancel_asn", "book_appointment", "check_in_appointment", "open_receipt", "receive_line", "close_receipt", "declare_pack_level", "retire_pack_level"} {
		if writePrefixOf(name) == "" {
			t.Errorf("%q must be flagged as a write tool", name)
		}
	}
	for _, name := range []string{"get_process_path_capacity", "get_capacity_plan", "get_storage_capacity", "list_station_standards", "find_claimable_work", "diagnose_stuck_tasks", "get_product", "list_products", "get_product_classification", "get_physical_profile", "get_pack_levels", "resolve_gtin", "list_asns", "get_asn", "list_appointments", "get_appointment", "list_receipts", "get_receipt", "list_docks"} {
		if p := writePrefixOf(name); p != "" {
			t.Errorf("%q is a read tool but was flagged by prefix %q", name, p)
		}
	}

	violating := `package x
func f(s *Session) { _ = s.callTool(nil, "create_capacity_plan", nil, nil) }`
	file := parseGoFile(t, "violating.go", []byte(violating))
	names := calledToolNames(file)
	if len(names) != 1 || writePrefixOf(names[0]) == "" {
		t.Fatalf("the scan must find and flag a write tool call in source, got %v", names)
	}
}

// calledToolNames returns the string-literal tool name (second argument) of
// every `<x>.callTool(ctx, "<name>", ...)` call in file.
func calledToolNames(file *ast.File) []string {
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "callTool" {
			return true
		}
		lit, ok := call.Args[1].(*ast.BasicLit)
		if !ok {
			return true
		}
		if name, err := strconv.Unquote(lit.Value); err == nil {
			names = append(names, name)
		}
		return true
	})
	return names
}

// writePrefixOf returns the write verb prefix name starts with, or "".
func writePrefixOf(name string) string {
	for _, p := range writeToolPrefixes {
		if strings.HasPrefix(name, p) {
			return p
		}
	}
	return ""
}
