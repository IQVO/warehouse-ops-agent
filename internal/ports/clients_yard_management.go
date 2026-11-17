package ports

import (
	"context"
	"time"
)

// YardManagementClient is the outbound port for yard-management's published
// READ tools (get_yard_board, list_trailers, get_trailer, list_moves,
// list_visits, get_visit, get_yard_policy, get_yard_dwell_report,
// get_inspection, list_trailer_inspections). yard-management's MCP server
// also publishes eleven L3 write tools (request_move, cancel_move,
// place_hold, release_hold, record_seal, secure_trailer, release_securing,
// open_inspection, record_inspection_item, complete_inspection,
// open_yard_check); this port deliberately has no method for any of them
// (ADR 0032, decision D-Y4: every agent write stays human-approved). Wired
// but not yet consumed by any use case.
type YardManagementClient interface {
	// GetYardBoard reads the live map of one site.
	GetYardBoard(ctx context.Context, siteID string) (YardBoard, error)
	// ListTrailers pages through trailers by arrival.
	ListTrailers(ctx context.Context, q YardTrailerQuery) (YardTrailerPage, error)
	// GetTrailer reads one trailer by id.
	GetTrailer(ctx context.Context, trailerID string) (YardTrailer, error)
	// ListMoves pages through yard moves, oldest request first.
	ListMoves(ctx context.Context, q YardMoveQuery) (YardMovePage, error)
	// ListVisits pages through gate visits, newest arrival first.
	ListVisits(ctx context.Context, q YardVisitQuery) (YardVisitPage, error)
	// GetVisit reads one gate visit by id.
	GetVisit(ctx context.Context, visitID string) (YardVisit, error)
	// GetYardPolicy reads a site's yard policy (the defaults with
	// declared=false when none was declared).
	GetYardPolicy(ctx context.Context, siteID string) (YardPolicy, error)
	// GetYardDwellReport reads the dwell report of [from, to) (at most 92
	// days); an empty siteID reports every site. Its rows are empty until
	// yard-management wires its analytics reader.
	GetYardDwellReport(ctx context.Context, from, to time.Time, siteID string) (YardDwellReport, error)
	// GetInspection reads one compliance inspection by id.
	GetInspection(ctx context.Context, inspectionID string) (YardInspection, error)
	// ListTrailerInspections pages through one trailer's inspections,
	// newest first.
	ListTrailerInspections(ctx context.Context, trailerID string, limit int, cursor string) (YardInspectionPage, error)
}

// YardTrailerQuery carries list_trailers' optional filters. A zero field is
// omitted so yard-management applies its own default (an omitted State
// means not DEPARTED; 100 per page).
type YardTrailerQuery struct {
	SiteID       string
	State        string
	Kind         string
	LocationCode string
	UnitNumber   string
	VisitID      string
	Limit        int
	Cursor       string
}

// YardMoveQuery carries list_moves' optional filters.
type YardMoveQuery struct {
	SiteID     string
	State      string
	TrailerID  string
	HostlerRef string
	Limit      int
	Cursor     string
}

// YardVisitQuery carries list_visits' optional filters.
type YardVisitQuery struct {
	SiteID       string
	State        string
	TractorPlate string
	Limit        int
	Cursor       string
}
