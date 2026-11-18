package mcpclient

import (
	"fmt"
	"slices"
	"sort"
)

// readOnlyReasonerUpstreams lists the upstreams whose MCP server publishes
// write (L3) tools next to its reads AND whose reasoner allow-list is held to
// a pinned read set (ADR 0032): the yard services. The reasoner's only
// actuators are read tools; for these two upstreams that rule is enforced by
// the pinned list, not by operator care, so an LLM_TOOL_ALLOWLIST entry
// naming a write tool (or any tool outside the pinned reads) can never reach
// the model.
var readOnlyReasonerUpstreams = map[string][]string{
	"dock-scheduling": DockSchedulingReadTools,
	"yard-management": YardManagementReadTools,
}

// CheckReasonerAllowList rejects an allow-list that names, for a pinned
// upstream, any tool outside that upstream's pinned read set. It is a
// startup check (an unrecognised entry is an error, never silently dropped):
// the caller aborts boot on a non-nil result. Upstreams not pinned here are
// untouched, so the legacy allow-list behaviour is unchanged.
func CheckReasonerAllowList(allowed map[string][]string) error {
	upstreams := make([]string, 0, len(allowed))
	for upstream := range allowed {
		upstreams = append(upstreams, upstream)
	}
	sort.Strings(upstreams)
	for _, upstream := range upstreams {
		reads, pinned := readOnlyReasonerUpstreams[upstream]
		if !pinned {
			continue
		}
		for _, tool := range allowed[upstream] {
			if !slices.Contains(reads, tool) {
				return fmt.Errorf("LLM_TOOL_ALLOWLIST entry %s/%s is not one of %s's pinned read tools %v: the reasoner may only call read tools (ADR 0032, D-Y4)", upstream, tool, upstream, reads)
			}
		}
	}
	return nil
}
