package observability

import "go.opentelemetry.io/otel/attribute"

// The fleet's agentic-standard telemetry attributes (contracts G9). Span
// NAMES are unchanged ("mcp.tool <name>", "a2a.skill <id>"); these are
// ADDED to them so a dashboard can slice agent operations by kind, agent and
// authority level.
const (
	// AttrOperationName is the GenAI semantic-convention operation name.
	AttrOperationName = "gen_ai.operation.name"
	// AttrAgentName is the GenAI semantic-convention agent name; here the
	// bounded-context name of the service emitting the span.
	AttrAgentName = "gen_ai.agent.name"
	// AttrAuthority is the declared authority level (L1..L4) of the tool or
	// skill the span executes.
	AttrAuthority = "warehouse.authority"

	// OpExecuteTool: a tool was executed (an MCP tool call, served or made).
	OpExecuteTool = "execute_tool"
	// OpInvokeAgent: another agent was invoked (an A2A skill delegation).
	OpInvokeAgent = "invoke_agent"

	// AgentName is this service's agent name.
	AgentName = "warehouse-ops-agent"

	// MetaAuthorityKey is the mcp.Tool.Meta key carrying the authority level
	// (contracts G2).
	MetaAuthorityKey = "com.warehouse/authority"
)

// The four authority levels (contracts G1). There is no L5.
const (
	AuthorityL1 = "L1"
	AuthorityL2 = "L2"
	AuthorityL3 = "L3"
	AuthorityL4 = "L4"
)

// ValidAuthority reports whether level is one of the four declared levels.
func ValidAuthority(level string) bool {
	switch level {
	case AuthorityL1, AuthorityL2, AuthorityL3, AuthorityL4:
		return true
	}
	return false
}

// AgenticAttributes returns the three G9 attributes for one span.
func AgenticAttributes(operation, authority string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(AttrOperationName, operation),
		attribute.String(AttrAgentName, AgentName),
		attribute.String(AttrAuthority, authority),
	}
}
