package zerowrite

// ADR 0028 adds a second outbound A2A client — a READ-ONLY, advisory lookup
// at exception-management — beside the ADR-0027 write client. This file is
// its executable boundary, so "the lookup grew teeth" can only happen by
// editing these assertions in a reviewed change:
//
//  1. the a2a adapter package holds exactly TWO client types;
//  2. the new client implements the read port only (one exported method),
//     and the write verb stays on the write client alone;
//  3. the read port declares a single read verb;
//  4. the new client calls only allow-listed READ skills of
//     exception-management, and never references the write skill, the
//     write port or the planning errors;
//  5. neither the MCP server nor the LLM reasoner can reach the lookup (the
//     lookup is attached to proposals for a human, never offered as a tool).
//
// The ADR-0027 pins in act_slice_test.go stay as they were: exactly one
// write route pair, the SDK confined to one adapter package, exactly one
// write-port implementation, the use case reachable from REST + the
// composition root only.

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	portsDir         = "../../ports"
	exceptionPortGo  = "exception_context.go"
	exceptionClient  = "exceptions.go"
	exceptionRecv    = "ExceptionClient"
	releaseClientRcv = "Client"
)

// readSkillsAllowed is every exception-management skill the lookup client
// may ever call. Both are read-only on the peer's card; any addition needs
// its own review.
var readSkillsAllowed = map[string]bool{"find_exceptions": true, "sla_status": true}

// exportedStructTypes names the exported struct types a file declares.
func exportedStructTypes(f *ast.File) []string {
	var out []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, isStruct := ts.Type.(*ast.StructType); isStruct && ts.Name.IsExported() {
				out = append(out, ts.Name.Name)
			}
		}
	}
	return out
}

// recvTypeName returns the receiver's type name of a method ("" for a plain
// function), looking through a pointer.
func recvTypeName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	t := d.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// exportedMethodsOf lists the exported methods declared on recv in f.
func exportedMethodsOf(f *ast.File, recv string) []string {
	var out []string
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && recvTypeName(d) == recv && d.Name.IsExported() {
			out = append(out, d.Name.Name)
		}
	}
	return out
}

// interfaceMethodNames lists the methods of the named interface type in f.
func interfaceMethodNames(f *ast.File, iface string) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != iface {
			return true
		}
		if it, ok := ts.Type.(*ast.InterfaceType); ok {
			for _, m := range it.Methods.List {
				for _, name := range m.Names {
					out = append(out, name.Name)
				}
			}
		}
		return false
	})
	return out
}

// askSkillArgs returns the skill argument (2nd) of every `<x>.ask(ctx, skill,
// ...)` call in f, as an identifier name or a string literal value.
func askSkillArgs(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "ask" {
			return true
		}
		switch a := call.Args[1].(type) {
		case *ast.Ident:
			out = append(out, a.Name)
		case *ast.BasicLit:
			v, _ := strconv.Unquote(a.Value)
			out = append(out, "literal:"+v)
		default:
			out = append(out, "expr")
		}
		return true
	})
	return out
}

// stringConsts maps each string constant declared in f to its value.
func stringConsts(f *ast.File) map[string]string {
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					out[name.Name] = v
				}
			}
		}
		return true
	})
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func requireEqual(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(want), ",") {
		t.Fatalf("%s = %v, want exactly %v (ADR 0028)", what, sortedCopy(got), sortedCopy(want))
	}
}

func a2aAdapterFiles(t *testing.T) []string {
	t.Helper()
	files := goFilesIn(t, a2aAdapterDir)
	if len(files) < 3 {
		t.Fatalf("a2a adapter files = %v — the scan is broken or the layout moved", files)
	}
	return files
}

func a2aAdapterFile(t *testing.T, name string) string {
	t.Helper()
	for _, f := range a2aAdapterFiles(t) {
		if filepath.Base(f) == name {
			return f
		}
	}
	t.Fatalf("%s is missing from the a2a adapter — update this pin deliberately", name)
	return ""
}

// (1) The adapter package holds exactly two client types.
func TestA2AAdapterHoldsExactlyTwoClients(t *testing.T) {
	var clients []string
	for _, path := range a2aAdapterFiles(t) {
		clients = append(clients, exportedStructTypes(parsed(t, path))...)
	}
	requireEqual(t, "exported client types in internal/adapters/outbound/a2a", clients, []string{releaseClientRcv, exceptionRecv})
}

// (2) The lookup client implements the read port only; the write verb stays
// on the write client alone.
func TestExceptionClientImplementsOnlyTheReadPort(t *testing.T) {
	var onException, onRelease []string
	for _, path := range a2aAdapterFiles(t) {
		f := parsed(t, path)
		onException = append(onException, exportedMethodsOf(f, exceptionRecv)...)
		onRelease = append(onRelease, exportedMethodsOf(f, releaseClientRcv)...)
	}
	requireEqual(t, "exported methods of ExceptionClient", onException, []string{"OpenExceptions"})
	requireEqual(t, "exported methods of Client (the one write-port implementation)", onRelease, []string{"ReleaseNextWork"})
}

// (3) The read port declares one read verb and nothing else.
func TestExceptionContextPortDeclaresOnlyTheReadVerb(t *testing.T) {
	f := parsed(t, filepath.Join(portsDir, exceptionPortGo))
	requireEqual(t, "methods of ports.ExceptionContext", interfaceMethodNames(f, "ExceptionContext"), []string{"OpenExceptions"})
}

// (4) The lookup client calls allow-listed read skills only and never
// touches the write side.
func TestExceptionClientCallsOnlyAllowListedReadSkills(t *testing.T) {
	f := parsed(t, a2aAdapterFile(t, exceptionClient))
	consts := stringConsts(f)
	asked := askSkillArgs(f)
	if len(asked) == 0 {
		t.Fatal("exceptions.go asks no skill — the scan is vacuous; update the helper")
	}
	for _, arg := range asked {
		id, ok := consts[arg]
		if !ok {
			t.Errorf("exceptions.go asks skill %q, which is not a string constant in the file — use a named, allow-listed skill constant", arg)
			continue
		}
		if !readSkillsAllowed[id] {
			t.Errorf("exceptions.go asks skill %q (%q) which is not an allow-listed read skill %v", arg, id, sortedKeys(readSkillsAllowed))
		}
	}
	for name, id := range consts {
		if strings.HasSuffix(name, "Skill") && !readSkillsAllowed[id] {
			t.Errorf("exceptions.go declares skill constant %s=%q outside the read allowlist", name, id)
		}
	}
	writeSide := map[string]bool{"releaseSkill": true, "WorkReleaser": true, "ReleasedWork": true, "ReleaseNextWork": true,
		"ErrWorkPlanningUnreachable": true, "ErrNoPendingWork": true, "ErrUnknownWorkPlanningPath": true}
	ast.Inspect(f, func(n ast.Node) bool {
		if name := identName(n); writeSide[name] {
			t.Errorf("exceptions.go references the write side (%s) — the lookup client must stay read-only", name)
		}
		return true
	})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
