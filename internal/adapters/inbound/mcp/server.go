package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds warehouse-ops-agent's own MCP server: five read-only
// tools (get_daily_brief, list_open_exceptions, get_flow_balance_exception,
// explain_travel_factor, detect_stranded_reservation — the last three
// registered only when their use case is wired; see registerTools).
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "warehouse-ops-agent-mcp", Version: "1.0.0"},
		&mcp.ServerOptions{
			Instructions: "Read-only access to warehouse-ops-agent's synthesized operational advice. Always available: get_daily_brief (per-site/per-path backlog, staffing, and stuck-task facts plus correlated open exceptions ranked by severity) and list_open_exceptions (those exceptions, optionally filtered by minimum severity). Available only when the deployment wires them: get_flow_balance_exception (correlated rebalance/staffing/stuck-task recommendation for one process path), explain_travel_factor (travel-distance significance between two caller-supplied location codes) and detect_stranded_reservation (stranded-reservation recommendation for one SKU; it only recommends, never revokes). This agent never writes to any bounded context.",
		},
	)

	deps.registerTools(server)

	return server
}

// Handler returns the Streamable HTTP handler for the MCP server. The
// fleet-wide auth removal (see ADR superseding 0005/0008) dropped the
// bearer-key gate this used to carry; every request is served
// unauthenticated.
func Handler(server *mcp.Server) *mcp.StreamableHTTPHandler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
}
