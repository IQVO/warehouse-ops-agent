package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterUndeclaredToolForTest registers a tool that is absent from
// toolAuthority; addTool must refuse it.
func RegisterUndeclaredToolForTest(server *mcp.Server) {
	addTool(server, &mcp.Tool{Name: "not_declared_anywhere"}, func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
}

// DeclaredTools lists the tool names in the authority table.
func DeclaredTools() []string {
	out := make([]string, 0, len(toolAuthority))
	for name := range toolAuthority {
		out = append(out, name)
	}
	return out
}
