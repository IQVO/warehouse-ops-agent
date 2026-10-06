package anthropic

import (
	"context"
	"strings"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- validateToolArgs ------------------------------------------------------

func TestValidateToolArgs_NoSchema_AnythingGoes(t *testing.T) {
	if err := validateToolArgs(nil, map[string]any{"anything": 1}); err != nil {
		t.Fatalf("nil schema must not reject: %v", err)
	}
	if err := validateToolArgs(map[string]any{}, map[string]any{"anything": 1}); err != nil {
		t.Fatalf("empty schema must not reject: %v", err)
	}
}

func TestValidateToolArgs_TypeMismatchRejected(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"windowSeconds": map[string]any{"type": "integer"}},
	}
	if err := validateToolArgs(schema, map[string]any{"windowSeconds": "not-a-number"}); err == nil {
		t.Fatal("expected a type-mismatch validation error")
	}
}

func TestValidateToolArgs_RequiredMissingRejected(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"pathId": map[string]any{"type": "string"}},
		"required":   []any{"pathId"},
	}
	if err := validateToolArgs(schema, map[string]any{}); err == nil {
		t.Fatal("expected a missing-required-property validation error")
	}
}

func TestValidateToolArgs_ValidArgsAccepted(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"pathId": map[string]any{"type": "string"}},
		"required":   []any{"pathId"},
	}
	if err := validateToolArgs(schema, map[string]any{"pathId": "pick-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgs_MalformedSchemaRejectedClosed(t *testing.T) {
	// "type" must be a string or array of strings; a number is malformed.
	schema := map[string]any{"type": 123}
	if err := validateToolArgs(schema, map[string]any{}); err == nil {
		t.Fatal("expected a malformed-schema error (refused closed), got nil")
	}
}

// --- recheckEnums ------------------------------------------------------

func TestRecheckEnums_ValueOutsideEnumRejected(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"severity": map[string]any{"type": "string", "enum": []any{"info", "warning", "critical"}},
		},
	}
	if err := recheckEnums(schema, map[string]any{"severity": "catastrophic"}); err == nil {
		t.Fatal("expected an enum-violation error")
	}
}

func TestRecheckEnums_ValueInsideEnumAccepted(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"severity": map[string]any{"type": "string", "enum": []any{"info", "warning", "critical"}},
		},
	}
	if err := recheckEnums(schema, map[string]any{"severity": "warning"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecheckEnums_NoEnumConstraintUntouched(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{"pathId": map[string]any{"type": "string"}},
	}
	if err := recheckEnums(schema, map[string]any{"pathId": "anything-at-all"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecheckEnums_PropertyAbsentFromArgsSkipped(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"severity": map[string]any{"type": "string", "enum": []any{"info", "warning"}},
		},
	}
	if err := recheckEnums(schema, map[string]any{}); err != nil {
		t.Fatalf("unexpected error for an absent property: %v", err)
	}
}

// --- argsHash ------------------------------------------------------

func TestArgsHash_DeterministicForEqualArgs(t *testing.T) {
	a := map[string]any{"pathId": "pick-a", "windowSeconds": float64(60)}
	b := map[string]any{"windowSeconds": float64(60), "pathId": "pick-a"} // different insertion order
	if argsHash(a) != argsHash(b) {
		t.Fatal("equal args (different key order) must hash equal")
	}
}

func TestArgsHash_DifferentForDifferentArgs(t *testing.T) {
	a := map[string]any{"pathId": "pick-a"}
	b := map[string]any{"pathId": "pick-b"}
	if argsHash(a) == argsHash(b) {
		t.Fatal("different args must not hash equal")
	}
}

func TestArgsHash_NeverContainsRawArgValues(t *testing.T) {
	h := argsHash(map[string]any{"secretLookingArg": "super-secret-value-xyz"})
	if strings.Contains(h, "super-secret-value-xyz") {
		t.Fatal("hash must never leak raw argument content")
	}
}

// --- executeToolCall integration (schema validation + enum re-check are
// enforced BEFORE the upstream is ever invoked) ---------------------------

func TestExecuteToolCall_SchemaViolation_NeverInvokesUpstream(t *testing.T) {
	inv := &fakeInvoker{out: map[string]string{"get_task_type_utilization": `{"utilizationPct":42}`}}
	r, err := New(Config{APIKey: "k", BaseURL: "http://example.invalid"}, inv)
	if err != nil {
		t.Fatal(err)
	}
	spec := ports.ToolSpec{
		Upstream: "labor-performance", Name: "get_task_type_utilization",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"windowSeconds": map[string]any{"type": "integer"}},
		},
	}
	result, call := r.executeToolCall(context.Background(), apiContent{ID: "tu_1", Input: []byte(`{"windowSeconds":"not-a-number"}`)}, spec)
	if len(inv.calls) != 0 {
		t.Fatalf("upstream must never be called on a schema violation, got %v", inv.calls)
	}
	if !result.IsError || !strings.Contains(call.Outcome, "refused") {
		t.Fatalf("expected a refused outcome, got result=%+v call=%+v", result, call)
	}
}

func TestExecuteToolCall_EnumViolation_NeverInvokesUpstream(t *testing.T) {
	inv := &fakeInvoker{out: map[string]string{"list_open_exceptions": `{"count":0}`}}
	r, err := New(Config{APIKey: "k", BaseURL: "http://example.invalid"}, inv)
	if err != nil {
		t.Fatal(err)
	}
	spec := ports.ToolSpec{
		Upstream: "warehouse-ops-agent", Name: "list_open_exceptions",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"severity": map[string]any{"type": "string", "enum": []any{"info", "warning", "critical"}}},
		},
	}
	result, call := r.executeToolCall(context.Background(), apiContent{ID: "tu_1", Input: []byte(`{"severity":"catastrophic"}`)}, spec)
	if len(inv.calls) != 0 {
		t.Fatalf("upstream must never be called on an enum violation, got %v", inv.calls)
	}
	if !result.IsError || !strings.Contains(call.Outcome, "refused") {
		t.Fatalf("expected a refused outcome, got result=%+v call=%+v", result, call)
	}
}

func TestExecuteToolCall_ValidArgs_InvokesUpstreamAndHashesArgs(t *testing.T) {
	inv := &fakeInvoker{out: map[string]string{"get_staffing_gap": `{"plannedHeads":5}`}}
	r, err := New(Config{APIKey: "k", BaseURL: "http://example.invalid"}, inv)
	if err != nil {
		t.Fatal(err)
	}
	spec := ports.ToolSpec{
		Upstream: "workforce-management", Name: "get_staffing_gap",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pathId": map[string]any{"type": "string"}}},
	}
	_, call := r.executeToolCall(context.Background(), apiContent{ID: "tu_1", Input: []byte(`{"pathId":"pick-a"}`)}, spec)
	if len(inv.calls) != 1 {
		t.Fatalf("expected exactly one upstream invocation, got %v", inv.calls)
	}
	if call.Outcome != "ok" {
		t.Fatalf("expected ok outcome, got %q", call.Outcome)
	}
}
