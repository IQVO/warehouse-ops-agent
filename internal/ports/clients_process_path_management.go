// Package ports (this file): the outbound port for the
// process-path-management bounded context, the fleet's owner of the process
// path catalogue (match prefixes, capabilities, eligibility, intake) and the
// per-site CPT (critical pull time) schedules.
//
// process-path-management's MCP server is READ-ONLY on its default surface
// (its own governance test pins the three registry tools, every one
// annotated readOnlyHint). This port mirrors all three: get_process_path,
// list_process_paths and get_cpt_schedule. process-path-management's writes
// (register, reclassify, deactivate a path, declare a CPT schedule) are
// REST-only there and have no method here. The zero-write fitness test
// (internal/architecture/zerowrite) fails the build if the mcpclient
// adapter ever names a write-verb tool, and the contract snapshot test in
// mcpclient pins the published tools against process-path-management's
// published registry golden (converted from that repo's older TSV registry
// golden into this repo's JSON convention; ADR 0026).
//
// History: a previous two-tool client (get_process_path,
// list_process_paths) was deleted by ADR 0024 (gap A9) as wired-but-
// unconsumed catalogue configuration. ADR 0026 re-adds the client pinned to
// the CURRENT registry — which has since grown get_cpt_schedule, an actual
// ops signal — so the endpoint the cluster already deploys is honest
// configuration again.
package ports

import (
	"context"
)

// ProcessPathManagementClient is the outbound port for
// process-path-management's three published read tools.
type ProcessPathManagementClient interface {
	// GetProcessPath calls get_process_path: one catalogue entry in full.
	GetProcessPath(ctx context.Context, pathID string) (ProcessPathEntry, error)
	// ListProcessPaths calls list_process_paths: the catalogue, ACTIVE-only
	// by default or every path including deactivated ones.
	ListProcessPaths(ctx context.Context, activeOnly bool) (ProcessPathList, error)
	// GetCPTSchedule calls get_cpt_schedule: one site's CPT schedule.
	GetCPTSchedule(ctx context.Context, siteID string) (CPTSchedule, error)
}

// --- process-path-management read-model DTOs ---------------------------------
//
// Field-for-field mirrors of process-path-management's MCP output views
// (internal/adapters/inbound/mcp/mapping.go in that repo). Timestamps stay
// the strings the server publishes. The contract snapshot test in mcpclient
// checks every json tag below against the published output schemas in
// testdata/process_path_management_tools.golden.json.

// ProcessPathEligibility mirrors one path's eligibility contract.
type ProcessPathEligibility struct {
	MaxUnitsPerLine           *int     `json:"maxUnitsPerLine,omitempty"`
	RequiredProductAttributes []string `json:"requiredProductAttributes,omitempty"`
	ExcludedProductAttributes []string `json:"excludedProductAttributes,omitempty"`
	NonSortable               bool     `json:"nonSortable,omitempty"`
}

// ProcessPathEntry mirrors processPathDTO (get_process_path and each
// list_process_paths item): the full definition of one catalogue path.
type ProcessPathEntry struct {
	PathID                  string                 `json:"pathId"`
	MatchPrefix             string                 `json:"matchPrefix"`
	Direct                  bool                   `json:"direct"`
	RequiredCapabilities    []string               `json:"requiredCapabilities"`
	DestinationLocationRole string                 `json:"destinationLocationRole,omitempty"`
	Status                  string                 `json:"status"`
	Active                  bool                   `json:"active"`
	CreatedAt               string                 `json:"createdAt"`
	UpdatedAt               string                 `json:"updatedAt"`
	CycleTimeP95            string                 `json:"cycleTimeP95"`
	Eligibility             ProcessPathEligibility `json:"eligibility"`
	Intake                  []string               `json:"intake"`
}

// ProcessPathList mirrors the list_process_paths result.
type ProcessPathList struct {
	Paths []ProcessPathEntry `json:"paths"`
}

// CPTCutoff mirrors one recurring daily cutoff of a site's CPT schedule.
type CPTCutoff struct {
	CptID           string   `json:"cptId"`
	LocalTime       string   `json:"localTime"`
	DaysOfWeek      []string `json:"daysOfWeek"`
	ShipMethod      string   `json:"shipMethod"`
	EligiblePathIDs []string `json:"eligiblePathIds"`
}

// CPTSchedule mirrors cptScheduleDTO (get_cpt_schedule): one site's
// critical-pull-time schedule.
type CPTSchedule struct {
	SiteID    string      `json:"siteId"`
	Timezone  string      `json:"timezone"`
	Cutoffs   []CPTCutoff `json:"cutoffs"`
	CreatedAt string      `json:"createdAt"`
	UpdatedAt string      `json:"updatedAt"`
}
