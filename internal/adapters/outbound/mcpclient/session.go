// Package mcpclient is the outbound MCP-client adapter: one thin,
// schema-typed client per upstream bounded context's Open Host Service. Each
// client in this package implements one of the internal/ports interfaces by
// calling the EXISTING published MCP tools each context already exposes at
// internal/adapters/inbound/mcp/ in its own repo (Streamable HTTP,
// mcp-go-inbound-adapter skill/template). These clients depend on published
// tool schemas (name + JSON input/output shape) only — never on any
// context's Go code; see the sibling repos' tools.go/mapping.go files, which
// are the contract this package binds to.
package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config is the connection info for one upstream context's MCP server: its
// Streamable-HTTP endpoint.
type Config struct {
	// Name identifies the upstream context in client implementation names
	// and error messages (e.g. "wes-work-planning").
	Name string
	// Endpoint is the Streamable-HTTP MCP endpoint, e.g.
	// "http://wes-work-planning-mcp:8090".
	Endpoint string
	// Timeout bounds a single tool call. Defaults to 10s when zero.
	Timeout time.Duration
}

// Session holds the connection config for a single upstream MCP server and
// opens a short-lived client session per tool call (it does not keep a
// connected session). It is shared by every typed client in this package:
// each typed client (WesWorkPlanning, FulfillmentExecution, ...) embeds a
// Session and adds one method per tool it calls.
type Session struct {
	cfg Config
}

// New builds a Session for the given upstream server config. It does not
// connect eagerly: every tool call opens its own fresh client session (see
// connect) and closes it when the call returns. Nothing is cached or reused
// between calls.
func New(cfg Config) *Session {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Session{cfg: cfg}
}

// connect opens a fresh Streamable-HTTP session. A fresh session per call
// keeps this adapter stateless and simple; the SDK's Streamable HTTP client
// is a lightweight logical connection, not a costly TCP handshake, so this
// trades a small per-call overhead for never having to reason about
// session/reconnect lifecycle in a decision-support agent that calls each
// tool infrequently (interval sampling, not a hot path).
func (s *Session) connect(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "warehouse-ops-agent", Version: "0.1.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: s.cfg.Endpoint}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: connect: %w", s.cfg.Name, err)
	}
	return session, nil
}

// callTool calls the named tool over a fresh session and unmarshals its
// structured content into out (nil out discards the result).
func (s *Session) callTool(ctx context.Context, tool string, args any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	session, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return fmt.Errorf("%s: call %s: %w", s.cfg.Name, tool, err)
	}
	if result.IsError {
		return fmt.Errorf("%s: tool %s reported an error: %s", s.cfg.Name, tool, contentText(result))
	}
	if out == nil {
		return nil
	}

	// Prefer StructuredContent (the typed path every published tool in this
	// fleet returns per the mcp-go-inbound-adapter template); fall back to
	// re-marshaling it into out's concrete type.
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return fmt.Errorf("%s: marshal structured content from %s: %w", s.cfg.Name, tool, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: decode structured content from %s: %w", s.cfg.Name, tool, err)
	}
	return nil
}

// contentText renders a tool result's unstructured content for an error
// message, best-effort.
func contentText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return "(no content)"
}
