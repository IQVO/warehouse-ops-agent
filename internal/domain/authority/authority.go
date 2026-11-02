// Package authority holds the fleet's agentic-standard vocabulary for this
// agent (contracts G1, G2, G9): the four authority levels, the MCP Meta key
// that declares one, and the telemetry attribute names that report it. It is
// pure constants and one predicate, so every adapter (MCP server, MCP and A2A
// clients) can share one spelling without importing each other.
package authority

// The four authority levels (G1). There is no L5.
const (
	L1 = "L1"
	L2 = "L2"
	L3 = "L3"
	L4 = "L4"
)

// MetaKey is the mcp.Tool.Meta key carrying a tool's level (G2).
const MetaKey = "com.warehouse/authority"

// Telemetry attribute names and values (G9). Span NAMES are unchanged
// ("mcp.tool <name>", "a2a.skill <id>"); these are ADDED to them.
const (
	// AttrOperationName is the GenAI semantic-convention operation name.
	AttrOperationName = "gen_ai.operation.name"
	// AttrAgentName is the GenAI semantic-convention agent name: the
	// bounded-context name of the service emitting the span.
	AttrAgentName = "gen_ai.agent.name"
	// AttrAuthority is the declared level of the tool or skill the span runs.
	AttrAuthority = "warehouse.authority"

	// OpExecuteTool: an MCP tool was executed (served or called).
	OpExecuteTool = "execute_tool"
	// OpInvokeAgent: another agent was invoked (an A2A skill delegation).
	OpInvokeAgent = "invoke_agent"

	// AgentName is this service's agent name.
	AgentName = "warehouse-ops-agent"
)

// Valid reports whether level is one of the four declared levels.
func Valid(level string) bool {
	switch level {
	case L1, L2, L3, L4:
		return true
	}
	return false
}
