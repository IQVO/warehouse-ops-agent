// Package anthropic — tool-call argument safety (ADR 0004): before any
// offered read tool is invoked on the model's behalf, its arguments are
// validated against the tool's own JSON schema (as published by the
// upstream's tools/list response, carried on ports.ToolSpec.InputSchema),
// and enum-constrained properties get a second, targeted re-check. Both
// checks run BEFORE the MCP call; an untrusted/malformed argument set is
// refused exactly like the pre-existing "arguments are not a JSON object"
// guard, never sent upstream.
//
// The general schema validation uses github.com/google/jsonschema-go,
// already an indirect dependency of this module (via
// github.com/modelcontextprotocol/go-sdk, whose mcp.Tool.InputSchema is a
// *jsonschema.Schema) — no new third-party dependency is introduced. The
// enum re-check is a small, targeted, dependency-free walk of the raw
// schema map: defense in depth for the one constraint most load-bearing
// for an untrusted model's arguments (a policy enum like recommendedAction
// smuggling in a value the deterministic layer never heard of), kept
// independent of whatever the general validator did or did not enforce.
package anthropic

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// remarshal round-trips src through JSON into dst (a pointer), the same
// decode path a *jsonschema.Schema would take coming off the wire.
func remarshal(src any, dst any) error {
	raw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// validateToolArgs checks args against the tool's advertised JSON schema.
// A nil/empty schema (an upstream that published no inputSchema) is
// treated as "anything goes" — there is nothing to validate against, and
// this is not a new gap: the tool call would have gone through unchecked
// before this validation existed too.
func validateToolArgs(schema map[string]any, args map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	s, err := decodeSchema(schema)
	if err != nil {
		// A schema this adapter cannot even parse is refused closed,
		// not treated as "no schema" — an upstream publishing a
		// malformed schema is itself a signal not to trust the call.
		return fmt.Errorf("decode tool input schema: %w", err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return fmt.Errorf("resolve tool input schema: %w", err)
	}
	if err := resolved.Validate(toValidateInstance(args)); err != nil {
		return fmt.Errorf("arguments do not match the tool's input schema: %w", err)
	}
	return nil
}

// decodeSchema round-trips the map[string]any form ports.ToolSpec carries
// (see mcpclient.ToolInvoker.Specs) into a *jsonschema.Schema.
func decodeSchema(schema map[string]any) (*jsonschema.Schema, error) {
	var s jsonschema.Schema
	if err := remarshal(schema, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// toValidateInstance normalizes args the same way remarshal would decode
// JSON: jsonschema.Resolved.Validate expects the instance shaped the way
// encoding/json would unmarshal it (map[string]any / []any / float64 /
// string / bool / nil), which is already exactly what args is here.
func toValidateInstance(args map[string]any) any {
	if args == nil {
		return map[string]any{}
	}
	return args
}

// enumViolation is returned by recheckEnums naming the offending property.
type enumViolation struct {
	Property string
	Value    any
	Allowed  []any
}

func (e *enumViolation) Error() string {
	return fmt.Sprintf("argument %q = %#v is not one of the tool's allowed enum values %#v", e.Property, e.Value, e.Allowed)
}

// recheckEnums is the targeted, dependency-free second pass over any
// top-level property the schema constrains with "enum": it re-confirms
// every value actually present in args is one of the allowed values,
// independent of whatever validateToolArgs already did. A property the
// schema does not mention, or does not constrain with enum, is untouched;
// a property absent from args (even if required) is left to
// validateToolArgs/the upstream tool to reject.
func recheckEnums(schema map[string]any, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	for name, rawProp := range props {
		val, present := args[name]
		if !present {
			continue
		}
		propSchema, ok := rawProp.(map[string]any)
		if !ok {
			continue
		}
		rawEnum, ok := propSchema["enum"]
		if !ok {
			continue
		}
		allowed, ok := rawEnum.([]any)
		if !ok {
			continue
		}
		if !enumContains(allowed, val) {
			return &enumViolation{Property: name, Value: val, Allowed: allowed}
		}
	}
	return nil
}

func enumContains(allowed []any, val any) bool {
	for _, a := range allowed {
		if reflect.DeepEqual(a, val) {
			return true
		}
	}
	return false
}
