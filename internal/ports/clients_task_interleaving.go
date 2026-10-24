// Package ports (this file): the outbound port for the task-interleaving
// bounded context (wms), the fleet's owner of the interleaving hints
// that chain a second task onto a station's completed one.
//
// task-interleaving's MCP server is READ-ONLY (its own governance test
// pins its four tools, every one annotated readOnlyHint). This port
// mirrors all four: get_hint, get_site_policy, get_station and
// list_hints. task-interleaving's writes (issue a hint, consume one,
// declare a policy) are upstream of it and have no method here. The
// zero-write fitness test (internal/architecture/zerowrite) fails the
// build if the mcpclient adapter ever names a write-verb tool, and the
// contract snapshot test in mcpclient pins the published tools against
// task-interleaving's published registry.
package ports

import (
	"context"
)

// TaskInterleavingClient is the outbound port for task-interleaving's
// four published read tools.
type TaskInterleavingClient interface {
	// GetHint calls get_hint: one interleaving hint by id.
	GetHint(ctx context.Context, hintID string) (InterleavingHint, error)
	// GetSitePolicy calls get_site_policy: the interleaving policy a
	// site declared.
	GetSitePolicy(ctx context.Context, siteID string) (InterleavingPolicy, error)
	// GetStation calls get_station: what task-interleaving knows about
	// one station, its active hint included.
	GetStation(ctx context.Context, stationID string) (StationState, error)
	// ListHints calls list_hints: the hints newest first, one page at a
	// time. Zero query fields are omitted so task-interleaving applies
	// its own defaults (every hint, the default page size).
	ListHints(ctx context.Context, query HintListQuery) (HintPage, error)
}

// HintListQuery filters and pages list_hints: an optional station, site
// and state (Active, Consumed, Expired or Superseded), the previous
// page's cursor and an optional page size (1..500).
type HintListQuery struct {
	StationID string
	SiteID    string
	State     string
	Cursor    string
	Limit     int
}

// --- task-interleaving read-model DTOs ---------------------------------------
//
// Field-for-field mirrors of task-interleaving's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/task_interleaving_tools.golden.json.

// HintCandidate mirrors one candidate task of a hint: its rank, the task
// and its type, where it is and how far the detour is in metres.
type HintCandidate struct {
	Rank     int     `json:"rank"`
	TaskID   string  `json:"taskId"`
	TaskType string  `json:"taskType"`
	Location string  `json:"location"`
	DetourM  float64 `json:"detourM"`
}

// InterleavingHint mirrors hintView (get_hint and each list_hints
// item). State is Active, Consumed, Expired or Superseded.
type InterleavingHint struct {
	HintID         string          `json:"hintId"`
	StationID      string          `json:"stationId"`
	SiteID         string          `json:"siteId"`
	State          string          `json:"state"`
	AfterTaskID    string          `json:"afterTaskId"`
	OriginLocation string          `json:"originLocation"`
	IssuedAt       string          `json:"issuedAt"`
	ExpiresAt      string          `json:"expiresAt"`
	PolicyVersion  int             `json:"policyVersion"`
	Candidates     []HintCandidate `json:"candidates"`
	Version        int             `json:"version"`
	ConsumedAt     string          `json:"consumedAt,omitempty"`
	ConsumedTaskID string          `json:"consumedTaskId,omitempty"`
	SupersededAt   string          `json:"supersededAt,omitempty"`
}

// InterleavingPolicy mirrors the site policy view: the detour bound,
// candidate cap, hint TTL, the eligible task types and the vertical
// weight of the detour metric.
type InterleavingPolicy struct {
	SiteID            string   `json:"siteId"`
	MaxDetourM        float64  `json:"maxDetourM"`
	MaxCandidates     int      `json:"maxCandidates"`
	HintTtlSeconds    int      `json:"hintTtlSeconds"`
	EligibleTaskTypes []string `json:"eligibleTaskTypes"`
	VerticalWeight    float64  `json:"verticalWeight"`
	Version           int      `json:"version"`
	DeclaredAt        string   `json:"declaredAt"`
}

// StationPoint mirrors a station's position in metres once the geometry
// is known.
type StationPoint struct {
	XM float64 `json:"xM"`
	YM float64 `json:"yM"`
	ZM float64 `json:"zM"`
}

// StationPosition mirrors a station's position: its location code, and
// the metric point when the geometry is known.
type StationPosition struct {
	Location string        `json:"location"`
	Point    *StationPoint `json:"point,omitempty"`
}

// ActiveHintView mirrors a station's currently active hint (the
// candidate list included).
type ActiveHintView struct {
	HintID        string          `json:"hintId"`
	State         string          `json:"state"`
	IssuedAt      string          `json:"issuedAt"`
	ExpiresAt     string          `json:"expiresAt"`
	PolicyVersion int             `json:"policyVersion"`
	Candidates    []HintCandidate `json:"candidates"`
}

// StationState mirrors stationView (get_station): the station's site and
// position, its last completed task and its active hint.
type StationState struct {
	StationID           string          `json:"stationId"`
	SiteID              string          `json:"siteId"`
	Position            StationPosition `json:"position"`
	LastCompletedTaskID string          `json:"lastCompletedTaskId"`
	LastCompletedAt     string          `json:"lastCompletedAt"`
	Version             int             `json:"version"`
	ActiveHint          *ActiveHintView `json:"activeHint,omitempty"`
}

// HintPage mirrors the list_hints result.
type HintPage struct {
	Items      []InterleavingHint `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}
