// Package zerowrite contains a fitness test that enforces
// warehouse-ops-agent's ADR-0004/v1 "zero write capability" guarantee as an
// executable check, rather than relying on code review alone. This agent's
// own MCP server exposes ReadOnlyHint:true tools by convention
// (internal/adapters/inbound/mcp/tools.go), and its outbound clients to the
// five upstream bounded contexts (internal/adapters/outbound/mcpclient,
// internal/adapters/outbound/restclient) are read-only today. This test
// fails loudly the moment either surface grows a mutating capability
// without a matching, deliberate update here — see
// docs/docs/mcp/governance-note.md for the two non-negotiable guardrails a
// future write-capable slice must satisfy (an authorization gate + explicit
// human confirmation before any write executes) before this test's
// assertions are relaxed.
package zerowrite

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mutatingHTTPMethods are the constant/literal spellings that would signal
// this agent issuing a write request to one of the five upstream bounded
// contexts' REST APIs. GET and HEAD are the only methods this agent is
// allowed to use against them.
var mutatingHTTPMethods = []string{
	"http.MethodPost", "http.MethodPut", "http.MethodPatch", "http.MethodDelete",
	`"POST"`, `"PUT"`, `"PATCH"`, `"DELETE"`,
}

// outboundClientDirs are the packages that talk to the five upstream
// bounded contexts. A mutating HTTP method literal appearing in any
// non-test .go file here means this agent has grown write capability.
var outboundClientDirs = []string{
	"../../adapters/outbound/restclient",
	"../../adapters/outbound/mcpclient",
}

// TestNoMutatingHTTPMethodInOutboundClients statically scans every non-test
// source file in this agent's outbound-to-bounded-context adapter packages
// for a mutating HTTP method literal or constant. It is a source-level
// check, not a runtime probe, deliberately mirroring
// TestNoDirectDependencyOnBoundedContexts's style in architecture_test.go:
// cheap, deterministic, and it fails the moment the offending line is
// written, before it ever reaches a running process.
func TestNoMutatingHTTPMethodInOutboundClients(t *testing.T) {
	for _, dir := range outboundClientDirs {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			for _, path := range goFilesIn(t, dir) {
				src, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				assertNoMutatingHTTPMethod(t, path, parseGoFile(t, path, src))
			}
		})
	}
}

// TestNoMutatingToolAnnotationInMCPServer statically scans this agent's own
// MCP tool registration file for a tool registered without
// ReadOnlyHint: true (or with it explicitly set false), which would mean
// this agent's own MCP server has grown a write-capable tool.
func TestNoMutatingToolAnnotationInMCPServer(t *testing.T) {
	path := "../../adapters/inbound/mcp/tools.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	toolCount, readOnlyTrueCount := countToolRegistrations(parseGoFile(t, path, src))
	if toolCount == 0 {
		t.Fatal("found zero mcp.Tool registrations — this test's AST walk is broken, or the file moved; fix the scan before trusting it")
	}

	// This agent's convention is a single `readOnly := true` local variable
	// reused by every tool literal (see registerTools in tools.go) rather
	// than a literal `true` per tool. Confirm that variable is never
	// reassigned to false anywhere in the file.
	assertReadOnlyNeverSetFalse(t, string(src))

	if readOnlyTrueCount != toolCount {
		t.Errorf("found %d mcp.Tool registration(s) but only %d ToolAnnotations{ReadOnlyHint: readOnly} reference(s) — every tool must set ReadOnlyHint via the shared readOnly variable (zero write capability, ADR-0004/v1)", toolCount, readOnlyTrueCount)
	}
}

// goFilesIn lists the non-test .go source files in dir, failing the test
// if the directory cannot be read.
func goFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	return paths
}

// parseGoFile parses one Go source file for the AST-level assertions
// below. Walking real AST literals/selectors rather than grepping raw
// text keeps a mutating method name mentioned only in a comment (e.g.
// explaining why it must NOT be used) from producing a false failure.
func parseGoFile(t *testing.T, path string, src []byte) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

// assertNoMutatingHTTPMethod fails the test if any expression in file
// names a mutating HTTP method, as a package selector or a string
// literal.
func assertNoMutatingHTTPMethod(t *testing.T, path string, file *ast.File) {
	t.Helper()
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			assertSelectorNotMutating(t, path, node)
		case *ast.BasicLit:
			assertLiteralNotMutating(t, path, node)
		}
		return true
	})
}

// assertSelectorNotMutating fails the test if node spells a mutating HTTP
// method constant such as http.MethodPost.
func assertSelectorNotMutating(t *testing.T, path string, node *ast.SelectorExpr) {
	t.Helper()
	ident, ok := node.X.(*ast.Ident)
	if !ok {
		return
	}
	sel := ident.Name + "." + node.Sel.Name
	for _, m := range mutatingHTTPMethods {
		if sel == m {
			t.Errorf("%s: found %q — outbound clients to the five bounded contexts must stay GET/HEAD-only (zero write capability, ADR-0004/v1)", path, sel)
		}
	}
}

// assertLiteralNotMutating fails the test if node is a mutating HTTP
// method string literal such as "POST".
func assertLiteralNotMutating(t *testing.T, path string, node *ast.BasicLit) {
	t.Helper()
	for _, m := range mutatingHTTPMethods {
		if node.Value == m {
			t.Errorf("%s: found %s — outbound clients to the five bounded contexts must stay GET/HEAD-only (zero write capability, ADR-0004/v1)", path, m)
		}
	}
}

// countToolRegistrations walks the tool-registration file and tallies both
// mcp.Tool composite literals and ToolAnnotations{ReadOnlyHint: …}
// references that set the hint true.
func countToolRegistrations(file *ast.File) (toolCount, readOnlyTrueCount int) {
	ast.Inspect(file, func(n ast.Node) bool {
		comp, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := comp.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		switch sel.Sel.Name {
		case "Tool":
			toolCount++
		case "ToolAnnotations":
			readOnlyTrueCount += countReadOnlyTrueHints(comp)
		}
		return true
	})
	return toolCount, readOnlyTrueCount
}

// countReadOnlyTrueHints counts ReadOnlyHint key/value pairs in one
// ToolAnnotations literal whose value is the shared `readOnly` variable
// or the literal `true`.
func countReadOnlyTrueHints(comp *ast.CompositeLit) int {
	count := 0
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "ReadOnlyHint" {
			continue
		}
		ident, ok := kv.Value.(*ast.Ident)
		if !ok {
			continue
		}
		// registerTools defines `readOnly := true` once and reuses the
		// variable for every tool — resolved by the never-false scan in
		// assertReadOnlyNeverSetFalse.
		if ident.Name == "readOnly" || ident.Name == "true" {
			count++
		}
	}
	return count
}

// assertReadOnlyNeverSetFalse confirms the shared readOnly convention
// variable is never assigned false anywhere in the file.
func assertReadOnlyNeverSetFalse(t *testing.T, src string) {
	t.Helper()
	if strings.Contains(src, "readOnly := false") || strings.Contains(src, "readOnly = false") {
		t.Fatal("found 'readOnly' set to false in tools.go — this agent's MCP tools must stay ReadOnlyHint:true (zero write capability, ADR-0004/v1)")
	}
}
