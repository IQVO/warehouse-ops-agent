package mcp

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/authority"
)

// toolAuthority declares the authority level of EVERY tool this agent's MCP
// server can register (fleet contract G1-G3): the plain reads are L1; the
// labor_reallocation_outlook is L2 because it recommends a concrete action
// (a ranked list of moves) that a human may then approve over REST. This
// server has no L3/L4 tool: the two writes of this agent are REST-only act
// slices (ADR 0027, ADR 0029), never MCP tools.
//
// addTool stamps the level onto the tool's Meta under
// authority.MetaKey ("com.warehouse/authority"). A tool missing
// from this table panics at registration, so a new tool cannot ship without
// a declared level; TestEveryToolDeclaresExactlyOneAuthorityLevel registers
// every tool and pins the table to the registered set.
var toolAuthority = map[string]string{
	"get_daily_brief":             authority.L1,
	"list_open_exceptions":        authority.L1,
	"get_flow_balance_exception":  authority.L1,
	"explain_travel_factor":       authority.L1,
	"detect_stranded_reservation": authority.L1,
	"find_master_data_gaps":       authority.L1,
	"get_inbound_outlook":         authority.L1,
	"triage_stuck_transfers":      authority.L1,
	"get_transfer_status":         authority.L1,
	"explain_network_imbalance":   authority.L1,
	"get_network_order_outlook":   authority.L1,
	"get_network_node_outlook":    authority.L1,

	"labor_reallocation_outlook": authority.L2,
}

// authorityOf returns the declared level of a tool, or an error when the
// tool is undeclared or declared with something other than L1..L4.
func authorityOf(tool string) (string, error) {
	level, ok := toolAuthority[tool]
	if !ok {
		return "", fmt.Errorf("mcp tool %q has no declared authority level; add it to toolAuthority", tool)
	}
	if !authority.Valid(level) {
		return "", fmt.Errorf("mcp tool %q declares authority %q, want one of L1..L4", tool, level)
	}
	return level, nil
}

// stampAuthority sets tool.Meta[com.warehouse/authority] and returns the level.
func stampAuthority(tool *mcp.Tool) string {
	level, err := authorityOf(tool.Name)
	if err != nil {
		panic(err)
	}
	if tool.Meta == nil {
		tool.Meta = mcp.Meta{}
	}
	tool.Meta[authority.MetaKey] = level
	return level
}
