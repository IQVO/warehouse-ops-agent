package zerowrite

import (
	"go/ast"
	"os"
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
// write tools (assign_labor, revoke_reservation, ...).
var writeToolPrefixes = []string{
	"create_", "publish_", "register_", "declare_", "assign_", "release_",
	"revoke_", "update_", "delete_", "cancel_", "set_", "add_", "remove_",
	"submit_", "reserve_", "activate_", "deactivate_",
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
	for _, name := range []string{"create_capacity_plan", "publish_capacity_plan", "register_process_path", "declare_station_standard", "assign_labor", "revoke_reservation"} {
		if writePrefixOf(name) == "" {
			t.Errorf("%q must be flagged as a write tool", name)
		}
	}
	for _, name := range []string{"get_process_path_capacity", "get_capacity_plan", "get_storage_capacity", "list_station_standards", "find_claimable_work", "diagnose_stuck_tasks"} {
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
