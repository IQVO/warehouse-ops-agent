package zerowrite

// The act slice (ADR 0027) is the ONE deliberate relaxation of the v1
// zero-write guarantee, and this file is its executable boundary. The three
// guardrails the governance note demands before any write exists —
// an authorization/confirmation gate, a human in the loop, and a write that
// can only go through a published tool — are pinned here as static facts, so
// "the slice grew" cannot happen by accident, only by editing these
// assertions in a reviewed change:
//
//  1. exactly ONE write route pair exists, and it is the propose/approve pair;
//  2. the A2A SDK (the only write transport) lives in exactly one adapter;
//  3. the write port has one implementation and no sibling write verbs exist;
//  4. the use case behind the writes is reachable from the REST adapter and
//     the composition root ONLY — never from the MCP server (an MCP caller is
//     typically a model approving its own recommendation) and never from the
//     LLM reasoner (its only actuators are allow-listed read tools).
//
// TestNoMutatingHTTPMethodInOutboundClients and
// TestNoMutatingToolAnnotationInMCPServer in zerowrite_test.go are
// UNCHANGED: the upstream read clients and this agent's own MCP tools are
// still strictly read-only.

import (
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	a2aSDKImportPrefix = "github.com/a2aproject/a2a-go"
	a2aAdapterDir      = "../../adapters/outbound/a2a"
	httpAdapterDir     = "../../adapters/inbound/http"
)

// internalRoot / cmdRoot are relative to this package's directory.
var scanRoots = []string{"../..", "../../../cmd"}

// allowedWriteRoutes is the complete set of non-GET routes this agent may
// register. Anything else registering a Post/Put/Patch/Delete route fails.
var allowedWriteRoutes = []string{
	"/act/release-next-work/approvals",
	"/act/release-next-work/proposals",
}

// siblingWriteVerbs are the other levers named in the governance note. They
// stay recommendation-only: no declaration of them may exist anywhere.
var siblingWriteVerbs = []string{"AssignLabor", "RevokeReservation", "CompleteTask"}

// nonTestGoFiles walks the scan roots for non-test, non-generated .go files.
func nonTestGoFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, root := range scanRoots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(out) == 0 {
		t.Fatal("scanned zero source files — the walk is broken or the layout moved; fix the scan before trusting it")
	}
	return out
}

func parsed(t *testing.T, path string) *ast.File {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return parseGoFile(t, path, src)
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// importPaths returns the unquoted import paths of one parsed file.
func importPaths(f *ast.File) []string {
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		if v, err := strconv.Unquote(imp.Path.Value); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// writeRoutePattern returns the route pattern when n is a call registering a
// non-GET route (chi Post/Put/Patch/Delete, or Method/MethodFunc).
func writeRoutePattern(n ast.Node) (string, bool) {
	writeRegistrars := map[string]bool{"Post": true, "Put": true, "Patch": true, "Delete": true, "MethodFunc": true, "Method": true}
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !writeRegistrars[sel.Sel.Name] {
		return "", false
	}
	// Method/MethodFunc take (method, pattern, ...); Post etc. take (pattern, ...).
	idx := 0
	if strings.HasPrefix(sel.Sel.Name, "Method") {
		idx = 1
	}
	if len(call.Args) <= idx {
		return "", false
	}
	lit, ok := call.Args[idx].(*ast.BasicLit)
	if !ok {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

// (1) Only the propose/approve route pair may register a write route.
func TestOnlyTheActRoutesRegisterWriteMethods(t *testing.T) {
	var found []string
	for _, path := range goFilesIn(t, httpAdapterDir) {
		ast.Inspect(parsed(t, path), func(n ast.Node) bool {
			if v, ok := writeRoutePattern(n); ok {
				found = append(found, v)
			}
			return true
		})
	}
	sort.Strings(found)
	if strings.Join(found, ",") != strings.Join(allowedWriteRoutes, ",") {
		t.Fatalf("write routes registered = %v, want exactly %v (ADR 0027): a new write route needs its own governance decision, not a quiet addition", found, allowedWriteRoutes)
	}
}

// importsA2ASDK reports whether the file imports the A2A SDK.
func importsA2ASDK(f *ast.File) (string, bool) {
	for _, v := range importPaths(f) {
		if strings.HasPrefix(v, a2aSDKImportPrefix) {
			return v, true
		}
	}
	return "", false
}

// (2) The A2A SDK is the write transport; it may be imported by exactly one
// adapter package.
func TestA2ASDKIsConfinedToTheActAdapter(t *testing.T) {
	for _, path := range nonTestGoFiles(t) {
		if within(path, a2aAdapterDir) {
			continue
		}
		if v, bad := importsA2ASDK(parsed(t, path)); bad {
			t.Errorf("%s imports %s — only internal/adapters/outbound/a2a may (the act slice's single write transport)", path, v)
		}
	}
	// Positive control: the adapter really does import it, so this test
	// cannot pass vacuously after a rename.
	imported := false
	for _, path := range goFilesIn(t, a2aAdapterDir) {
		if _, ok := importsA2ASDK(parsed(t, path)); ok {
			imported = true
		}
	}
	if !imported {
		t.Fatal("the a2a adapter no longer imports the SDK — the scan above is vacuous; update the constants")
	}
}

// writeVerbsDeclaredBy returns the write-verb names a node declares, as a
// function or as an interface method.
func writeVerbsDeclaredBy(n ast.Node) []string {
	verbs := map[string]bool{"ReleaseNextWork": true}
	for _, v := range siblingWriteVerbs {
		verbs[v] = true
	}
	var out []string
	switch d := n.(type) {
	case *ast.FuncDecl:
		if verbs[d.Name.Name] {
			out = append(out, d.Name.Name)
		}
	case *ast.InterfaceType:
		for _, m := range d.Methods.List {
			for _, name := range m.Names {
				if verbs[name.Name] {
					out = append(out, name.Name)
				}
			}
		}
	}
	return out
}

// declaredWriteVerbs maps each write verb to the files that declare it.
func declaredWriteVerbs(t *testing.T) map[string][]string {
	declared := map[string][]string{}
	for _, path := range nonTestGoFiles(t) {
		ast.Inspect(parsed(t, path), func(n ast.Node) bool {
			for _, v := range writeVerbsDeclaredBy(n) {
				declared[v] = append(declared[v], path)
			}
			return true
		})
	}
	return declared
}

// (3) One write port, one implementation, no sibling write verbs.
func TestWriteVerbsAreLimitedToTheReleasePort(t *testing.T) {
	declared := declaredWriteVerbs(t)
	for _, v := range siblingWriteVerbs {
		if files := declared[v]; len(files) > 0 {
			t.Errorf("%s is declared in %v — it must stay recommendation-only (governance note)", v, files)
		}
	}
	for _, f := range declared["ReleaseNextWork"] {
		okPort := filepath.Base(f) == "work_release.go" && within(f, "../../ports")
		if !okPort && !within(f, a2aAdapterDir) {
			t.Errorf("ReleaseNextWork declared in %s — only the port (ports/work_release.go) and the a2a adapter may", f)
		}
	}
	if len(declared["ReleaseNextWork"]) != 2 {
		t.Errorf("ReleaseNextWork declarations = %v, want exactly the port and its one adapter", declared["ReleaseNextWork"])
	}
}

// goFilesUnder lists the non-test .go files of a guarded path, which is
// either one file or a directory tree.
func goFilesUnder(t *testing.T, g string) []string {
	t.Helper()
	info, err := os.Stat(g)
	if err != nil {
		t.Fatalf("guarded path %s missing: %v — update the list", g, err)
	}
	if !info.IsDir() {
		return []string{g}
	}
	var files []string
	walkErr := filepath.Walk(g, func(p string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
		return e
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", g, walkErr)
	}
	return files
}

// identName returns the identifier or selected name of n, or "".
func identName(n ast.Node) string {
	switch x := n.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// assertUnreachableFromActSlice fails if path imports or names anything that
// belongs to the act slice.
func assertUnreachableFromActSlice(t *testing.T, path string) {
	t.Helper()
	forbiddenIdents := map[string]bool{"ReleaseWork": true, "NewReleaseWork": true, "WorkReleaser": true, "ReleaseNextWork": true, "ReleaseProposal": true, "ReleaseOutcome": true}
	// ADR 0028: the advisory exception lookup is attached to proposals for a
	// human; it is not a tool and the model never drives it.
	for _, name := range exceptionLookupIdents {
		forbiddenIdents[name] = true
	}
	forbiddenImports := []string{"/internal/domain/approval", a2aSDKImportPrefix, "/internal/adapters/outbound/a2a"}
	f := parsed(t, path)
	for _, v := range importPaths(f) {
		for _, bad := range forbiddenImports {
			if strings.Contains(v, bad) {
				t.Errorf("%s imports %s — the MCP server and the LLM reasoner must never reach the act slice (ADR 0027)", path, v)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if name := identName(n); forbiddenIdents[name] {
			t.Errorf("%s references %s — the MCP server and the LLM reasoner must never reach the act slice (ADR 0027)", path, name)
		}
		return true
	})
}

// (4) The write use case, its port and the confirmation gate are invisible to
// the MCP server and the LLM reasoner.
func TestActSliceIsUnreachableFromMCPAndTheReasoner(t *testing.T) {
	guarded := []string{
		"../../adapters/inbound/mcp",
		"../../adapters/outbound/llm",
		"../../ports/reasoner.go",
		"../../../cmd/agent/reasoner.go",
	}
	var files []string
	for _, g := range guarded {
		files = append(files, goFilesUnder(t, g)...)
	}
	if len(files) < 3 {
		t.Fatalf("guarded file set = %v — suspiciously small, the scan is broken", files)
	}
	for _, path := range files {
		assertUnreachableFromActSlice(t, path)
	}
}

// exceptionLookupIdents are the ADR-0028 names that must stay out of the MCP
// server and the LLM reasoner (and out of everything but REST, the use case
// and the composition root).
var exceptionLookupIdents = []string{
	"ExceptionContext", "ExceptionContextQuery", "ExceptionContextResult", "ExceptionSummary", "ExceptionBrief",
	"ExceptionClient", "NewExceptionClient", "WithExceptionContext", "ErrExceptionContextUnavailable",
}

// usesReleaseWork reports the selector name when n is usecases.ReleaseWork or
// usecases.NewReleaseWork.
func usesReleaseWork(n ast.Node) (string, bool) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != "usecases" {
		return "", false
	}
	switch sel.Sel.Name {
	case "ReleaseWork", "NewReleaseWork", "WithExceptionContext", "ExceptionContextResult", "ReleaseWorkOption":
		return sel.Sel.Name, true
	}
	return sel.Sel.Name, false
}

// (4b) Only the REST adapter and the composition root may use the write use
// case.
func TestReleaseWorkUseCaseIsOnlyUsedByRESTAndTheCompositionRoot(t *testing.T) {
	allowed := []string{httpAdapterDir, "../../../cmd/agent", "../../application/usecases"}
	for _, path := range nonTestGoFiles(t) {
		if within(path, allowed[0]) || within(path, allowed[1]) || within(path, allowed[2]) {
			continue
		}
		ast.Inspect(parsed(t, path), func(n ast.Node) bool {
			if name, bad := usesReleaseWork(n); bad {
				t.Errorf("%s uses usecases.%s — only the REST adapter and cmd/agent may (ADR 0027)", path, name)
			}
			return true
		})
	}
}
