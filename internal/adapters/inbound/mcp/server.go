package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds warehouse-ops-agent's own MCP server: get_daily_brief
// and list_open_exceptions, both read-only.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "warehouse-ops-agent-mcp", Version: "1.0.0"},
		&mcp.ServerOptions{
			Instructions: "Read-only access to warehouse-ops-agent's synthesized daily operational brief: per-site/per-path backlog, staffing, and stuck-task facts, plus correlated open exceptions ranked by severity. This agent never writes to any bounded context.",
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
