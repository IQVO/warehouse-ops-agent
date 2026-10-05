package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolInvoker is the ports.ToolInvoker the LLM reasoner is handed (ADR
// 0004): a map of upstream name -> Session, exposing ONLY the tools on an
// explicit allow-list (LLM_TOOL_ALLOWLIST) per upstream. The model can
// therefore reach nothing but curated MCP read tools — REST and MCP are
// unauthenticated fleet-wide (ADR 0006), so the allow-list, not a bearer
// scope, is what bounds it — and every call flows through the same
// Session.callTool path (same timeout, same error mapping) the
// deterministic use cases use.
type ToolInvoker struct {
	sessions map[string]*Session
	allowed  map[string]map[string]struct{}

	mu    sync.Mutex
	specs []ports.ToolSpec // discovered lazily via tools/list
}

// NewToolInvoker binds sessions and their allow-lists. An upstream with an
// empty endpoint is skipped (same "unset means skip" rule as the rest of
// the config), so a partially wired cluster never yields a nil session.
func NewToolInvoker(sessions map[string]*Session, allowed map[string][]string) *ToolInvoker {
	inv := &ToolInvoker{sessions: map[string]*Session{}, allowed: map[string]map[string]struct{}{}}
	for name, s := range sessions {
		if s == nil || s.cfg.Endpoint == "" {
			continue
		}
		inv.sessions[name] = s
		set := map[string]struct{}{}
		for _, t := range allowed[name] {
			set[t] = struct{}{}
		}
		inv.allowed[name] = set
	}
	return inv
}

// Invoke runs one allow-listed tool and returns the result as JSON text
// (structured content when the server provided it, otherwise the text
// content), which is what the model gets back verbatim as a tool_result.
func (i *ToolInvoker) Invoke(ctx context.Context, upstream, tool string, args map[string]any) (string, error) {
	s, ok := i.sessions[upstream]
	if !ok {
		return "", fmt.Errorf("mcpclient: unknown upstream %q", upstream)
	}
	if _, ok := i.allowed[upstream][tool]; !ok {
		return "", fmt.Errorf("mcpclient: tool %s/%s is not on the reasoner allow-list", upstream, tool)
	}
	var out any
	if err := s.callTool(ctx, tool, args, &out); err != nil {
		return "", err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("mcpclient: encode %s/%s result: %w", upstream, tool, err)
	}
	return string(raw), nil
}

// Specs lists the allow-listed tools with the descriptions and input
// schemas the upstreams publish, discovered once per process via
// tools/list and cached. Upstreams that fail discovery are skipped (logged
// by the caller via the returned error list); the model simply is not
// offered their tools.
func (i *ToolInvoker) Specs(ctx context.Context) ([]ports.ToolSpec, []error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.specs != nil {
		return i.specs, nil
	}
	var specs []ports.ToolSpec
	var errs []error
	names := make([]string, 0, len(i.sessions))
	for n := range i.sessions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		listed, err := i.sessions[name].listTools(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, t := range listed {
			if _, ok := i.allowed[name][t.Name]; !ok {
				continue
			}
			var schema map[string]any
			if t.InputSchema != nil {
				if raw, err := json.Marshal(t.InputSchema); err == nil {
					_ = json.Unmarshal(raw, &schema)
				}
			}
			specs = append(specs, ports.ToolSpec{Upstream: name, Name: t.Name, Description: t.Description, InputSchema: schema})
		}
	}
	if len(errs) == 0 {
		i.specs = specs
	}
	return specs, errs
}

func (s *Session) listTools(ctx context.Context) ([]*mcp.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	session, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("%s: list tools: %w", s.cfg.Name, err)
	}
	return res.Tools, nil
}
