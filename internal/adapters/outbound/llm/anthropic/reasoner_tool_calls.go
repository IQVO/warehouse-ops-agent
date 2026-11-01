package anthropic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// executeToolCall invokes one offered read tool on the model's behalf and
// returns both the tool_result block to echo back and the audit-trail
// entry. Arguments that are not a JSON object are refused without an
// invocation; so are arguments that fail schema validation or the
// targeted enum re-check (ADR-0004 tool-call safety) — in every refusal
// case the upstream is never called. The call itself is wrapped in an
// OTel span (llm.tool_call) and the audit log carries a sha256 hash of
// the canonical (sorted-key) JSON-encoded arguments, never the raw
// arguments themselves.
func (r *Reasoner) executeToolCall(ctx context.Context, c apiContent, spec ports.ToolSpec) (apiContent, ports.ToolCall) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "llm.tool_call", trace.WithAttributes(
		attribute.String("llm.tool_call.upstream", spec.Upstream),
		attribute.String("llm.tool_call.tool", spec.Name),
	))
	defer span.End()

	var args map[string]any
	if len(c.Input) > 0 {
		if err := json.Unmarshal(c.Input, &args); err != nil {
			return r.refuseToolCall(ctx, span, c, spec, nil, "refused: bad arguments", "arguments are not a JSON object")
		}
	}
	if err := validateToolArgs(spec.InputSchema, args); err != nil {
		return r.refuseToolCall(ctx, span, c, spec, args, "refused: schema validation failed", err.Error())
	}
	if err := recheckEnums(spec.InputSchema, args); err != nil {
		return r.refuseToolCall(ctx, span, c, spec, args, "refused: enum re-check failed", err.Error())
	}

	started := time.Now()
	out, err := r.invoker.Invoke(ctx, spec.Upstream, spec.Name, args)
	call := ports.ToolCall{Upstream: spec.Upstream, Tool: spec.Name, Args: args, Outcome: "ok"}
	if err != nil {
		call.Outcome = err.Error()
	}
	r.logToolCall(ctx, spec, args, call.Outcome, time.Since(started))
	if err != nil {
		span.SetStatus(codes.Error, call.Outcome)
	}

	result := apiContent{Type: "tool_result", ToolUseID: c.ID, Content: out}
	if err != nil {
		result.Content, result.IsError = err.Error(), true
	}
	return result, call
}

// refuseToolCall is the shared path for every pre-invocation refusal
// (bad JSON, failed schema validation, failed enum re-check): the
// upstream is never called, the span records the refusal, and the audit
// log still carries only the args hash, never raw args.
func (r *Reasoner) refuseToolCall(ctx context.Context, span trace.Span, c apiContent, spec ports.ToolSpec, args map[string]any, outcome, detail string) (apiContent, ports.ToolCall) {
	span.SetStatus(codes.Error, outcome)
	r.logToolCall(ctx, spec, args, outcome, 0)
	return apiContent{Type: "tool_result", ToolUseID: c.ID, Content: detail, IsError: true},
		ports.ToolCall{Upstream: spec.Upstream, Tool: spec.Name, Outcome: outcome}
}

// logToolCall emits the llm.tool_call audit log entry. It NEVER logs raw
// args — only a sha256 hash of their canonical (sorted-key, via
// encoding/json's own deterministic map-key ordering) JSON encoding, so
// an operator can correlate repeated/identical calls without the log
// carrying potentially sensitive tool-argument content.
func (r *Reasoner) logToolCall(ctx context.Context, spec ports.ToolSpec, args map[string]any, outcome string, latency time.Duration) {
	r.cfg.Logger.InfoContext(ctx, "llm.tool_call",
		"upstream", spec.Upstream, "tool", spec.Name, "outcome", outcome,
		"args_sha256", argsHash(args), "latency_ms", latency.Milliseconds())
}

// argsHash returns the hex-encoded sha256 of args' canonical JSON
// encoding ("canonical" meaning encoding/json's own deterministic
// sorted-map-key form — the same form every call in this process
// produces for equal args, so equal args always hash equal).
func argsHash(args map[string]any) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (r *Reasoner) tools(brief ports.Brief) ([]apiTool, map[string]ports.ToolSpec) {
	lookup := make(map[string]ports.ToolSpec, len(brief.Tools))
	tools := make([]apiTool, 0, len(brief.Tools)+1)
	for _, t := range brief.Tools {
		name := t.Upstream + "__" + t.Name
		lookup[name] = t
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, apiTool{Name: name, Description: t.Description, InputSchema: schema})
	}
	tools = append(tools, apiTool{
		Name:        submitPlanTool,
		Description: "Submit your final recommendation. This is the ONLY way to answer.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"recommendedAction": map[string]any{"type": "string", "enum": brief.AllowedActions},
				"proposedHeads":     map[string]any{"type": "integer", "minimum": 0, "maximum": 50, "description": "Only for assign_labor (at least 1, at most 50); 0 otherwise."},
				"rationale":         map[string]any{"type": "string", "description": "One or two sentences citing the facts you relied on."},
			},
			"required":             []string{"recommendedAction", "proposedHeads", "rationale"},
			"additionalProperties": false,
		},
	})
	return tools, lookup
}
