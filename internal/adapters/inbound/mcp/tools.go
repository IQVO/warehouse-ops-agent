package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// tracerName is the OTel instrumentation scope for MCP tool spans.
const tracerName = "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the same use case the HTTP adapter uses; the adapter never
// constructs an outbound adapter itself.
type Deps struct {
	// DailyBrief is the existing E3 read-model use case, reused unchanged.
	DailyBrief *usecases.DailyBrief

	// FlowBalanceAdvisory is the E1 correlation use case (T2), reused
	// unchanged. Nil is a valid value in tests that only exercise the
	// daily-brief tools; get_flow_balance_exception is simply not
	// registered when nil (see registerTools).
	FlowBalanceAdvisory *usecases.FlowBalanceAdvisory

	// ExplainTravelFactor is the ADR-0009 use case. Nil is a valid
	// value; explain_travel_factor is simply not registered when nil,
	// mirroring FlowBalanceAdvisory's own precedent.
	ExplainTravelFactor *usecases.ExplainTravelFactor

	// StrandedReservation is the E2 correlation use case
	// (internal/application/usecases/stranded_reservation.go). Nil is a
	// valid value; detect_stranded_reservation is simply not registered
	// when nil, mirroring FlowBalanceAdvisory's own precedent. Read-only:
	// it only ever recommends a revoke_reservation, never calls it.
	StrandedReservation *usecases.DetectStrandedReservation

	// MasterDataGaps is the ADR 0020 product-master use case. Nil (no
	// PRODUCT_MASTER_MCP_ENDPOINT) means find_master_data_gaps is simply
	// not registered.
	MasterDataGaps *usecases.MasterDataGaps

	// TransferWatch is the ADR-0019 read-only view of
	// network-inventory-planning. Nil is a valid value; the three
	// transfer-watch tools are simply not registered when nil.
	TransferWatch *usecases.TransferWatch
}

// --- get_daily_brief -----------------------------------------------------

type dailyBriefInput struct{}

type dailyBriefOutput struct {
	Sites          []siteBriefDTO     `json:"sites"`
	OpenExceptions []openExceptionDTO `json:"openExceptions"`
}

func (d Deps) getDailyBrief(ctx context.Context, _ dailyBriefInput) (dailyBriefOutput, error) {
	brief := d.DailyBrief.Execute(ctx)
	return dailyBriefOutput{
		Sites:          toSiteBriefDTOs(brief.Sites),
		OpenExceptions: toOpenExceptionDTOs(brief.OpenExceptions),
	}, nil
}

// --- list_open_exceptions -------------------------------------------------

// listOpenExceptionsInput's Severity, when non-empty, filters the result to
// exceptions at or above that minimum severity. Untrusted model input: an
// unknown value is rejected, never silently defaulted to "return
// everything" or "return nothing".
type listOpenExceptionsInput struct {
	Severity string `json:"severity,omitempty" jsonschema:"optional minimum severity filter: info, warning, or critical; omit to return every open exception"`
}

type listOpenExceptionsOutput struct {
	Count      int                `json:"count"`
	Exceptions []openExceptionDTO `json:"exceptions"`
}

// severityRank orders severities worst-first so a minimum-severity filter
// can be expressed as "rank <= requested rank".
func severityRank(s string) (int, bool) {
	switch s {
	case "critical":
		return 0, true
	case "warning":
		return 1, true
	case "info":
		return 2, true
	default:
		return 0, false
	}
}

func (d Deps) listOpenExceptions(ctx context.Context, in listOpenExceptionsInput) (listOpenExceptionsOutput, error) {
	minRank := 2 // default: include info and above, i.e. everything.
	if in.Severity != "" {
		r, ok := severityRank(in.Severity)
		if !ok {
			return listOpenExceptionsOutput{}, invalidSeverityErr(in.Severity)
		}
		minRank = r
	}

	brief := d.DailyBrief.Execute(ctx)
	all := toOpenExceptionDTOs(brief.OpenExceptions)

	filtered := make([]openExceptionDTO, 0, len(all))
	for _, e := range all {
		if r, ok := severityRank(e.Severity); ok && r <= minRank {
			filtered = append(filtered, e)
		}
	}
	return listOpenExceptionsOutput{Count: len(filtered), Exceptions: filtered}, nil
}

// --- get_flow_balance_exception --------------------------------------------

// flowBalanceExceptionInput's fields scope the E1 correlation exactly as
// FlowBalanceAdvisory.Execute requires: pathId anchors the wes rebalance
// recommendation and the workforce-management staffing lookup, which also
// needs buildingId/shiftId. All three are untrusted caller input, passed
// straight through to the use case's outbound port calls (each of which
// validates its own arguments on the upstream side).
type flowBalanceExceptionInput struct {
	BuildingId string `json:"buildingId" jsonschema:"the building this process path belongs to, for the workforce-management staffing lookup"`
	ShiftId    string `json:"shiftId" jsonschema:"the shift to check staffing against"`
	PathId     string `json:"pathId" jsonschema:"the wes-work-planning process path id to correlate"`
}

type flowBalanceExceptionOutput struct {
	PathId            string                     `json:"pathId"`
	RecommendedAction string                     `json:"recommendedAction"`
	ProposedHeads     int                        `json:"proposedHeads,omitempty"`
	Rationale         string                     `json:"rationale"`
	Partial           bool                       `json:"partial"`
	MissingSignals    []string                   `json:"missingSignals,omitempty"`
	Evidence          []evidenceEntryDTO         `json:"evidence"`
	Utilization       *utilizationCorrelationDTO `json:"utilization,omitempty"`
	// Source is the ADR-0004 arbitration source that produced this
	// Decision: deterministic, llm, or fallback.
	Source string `json:"source,omitempty"`
}

func (d Deps) getFlowBalanceException(ctx context.Context, in flowBalanceExceptionInput) (flowBalanceExceptionOutput, error) {
	decision, err := d.FlowBalanceAdvisory.Execute(ctx, in.BuildingId, in.ShiftId, in.PathId)
	if err != nil {
		return flowBalanceExceptionOutput{}, err
	}
	return flowBalanceExceptionOutput{
		PathId:            decision.PathId,
		RecommendedAction: string(decision.RecommendedAction),
		ProposedHeads:     decision.ProposedHeads,
		Rationale:         decision.Rationale,
		Partial:           decision.Partial,
		MissingSignals:    decision.MissingSignals,
		Evidence:          toFlowBalanceEvidenceDTOs(decision.Evidence),
		Utilization:       toUtilizationCorrelationDTO(decision.Utilization),
		Source:            string(decision.Source),
	}, nil
}

// --- explain_travel_factor --------------------------------------------------

// explainTravelFactorInput's fromLocationCode/toLocationCode are the two
// facility-layout seven-segment location codes to measure travel between
// — supplied entirely by the caller, never resolved or guessed by this
// tool (see usecases.ExplainTravelFactor's own doc comment for why:
// there is no published MCP tool anywhere in the fleet today that
// surfaces a station's or task's location code). pathId is carried
// through for context/logging only.
type explainTravelFactorInput struct {
	PathId           string `json:"pathId" jsonschema:"the process path id being investigated, for context/logging only"`
	FromLocationCode string `json:"fromLocationCode" jsonschema:"the seven-segment facility-layout location code to measure travel from, e.g. WH1-STOR-AMB-A07-01-01-A"`
	ToLocationCode   string `json:"toLocationCode" jsonschema:"the seven-segment facility-layout location code to measure travel to, e.g. WH1-STOR-AMB-A09-03-01-A"`
}

type explainTravelFactorOutput struct {
	MetresM   float64 `json:"metresM"`
	Estimated bool    `json:"estimated"`
	Kind      string  `json:"kind,omitempty"`
	Rationale string  `json:"rationale,omitempty"`
}

func (d Deps) explainTravelFactor(ctx context.Context, in explainTravelFactorInput) (explainTravelFactorOutput, error) {
	result, err := d.ExplainTravelFactor.Execute(ctx, in.PathId, in.FromLocationCode, in.ToLocationCode)
	if err != nil {
		return explainTravelFactorOutput{}, err
	}
	out := explainTravelFactorOutput{}
	if result.Reading != nil {
		out.MetresM = result.Reading.MetresM
		out.Estimated = result.Reading.Estimated
	}
	if result.Correlation != nil {
		out.Kind = string(result.Correlation.Kind)
		out.Rationale = result.Correlation.Rationale
	}
	return out, nil
}

// --- detect_stranded_reservation ---------------------------------------

// strandedReservationInput's fields mirror
// usecases.StrandedReservationRequest exactly: taskType and skuCode/
// minUsableThreshold anchor the correlation, reservationId/binId are
// optional candidates the tool would recommend revoking (with a mandatory
// blast radius) if the correlation confirms a stranded reservation. All
// fields are untrusted caller input, passed straight through to the
// use case's outbound port calls (each of which validates its own
// arguments on the upstream side) -- this tool never resolves or guesses
// a candidate reservation/bin on the caller's behalf.
type strandedReservationInput struct {
	TaskType           string `json:"taskType" jsonschema:"the process/task type whose expired leases are the suspected correlation signal: PICK, PACK, or SLAM"`
	WithinSeconds      int    `json:"withinSeconds,omitempty" jsonschema:"bounds fulfillment-execution's diagnose_stuck_tasks window; 0 (default) means already-expired only"`
	SKU                string `json:"sku" jsonschema:"the stock-keeping unit under suspicion of being stranded"`
	MinUsableThreshold int    `json:"minUsableThreshold" jsonschema:"the usable-quantity ceiling at or below which a shortfall is considered correlated with the expired leases"`
	ReservationId      string `json:"reservationId,omitempty" jsonschema:"the candidate reservation this tool would recommend revoking if the correlation confirms a stranded reservation; omit if no candidate is yet known"`
	BinId              string `json:"binId,omitempty" jsonschema:"the bin holding the reservation's stock, used to build the mandatory blast radius before any revoke is recommended; omit if not yet known"`
}

type binLineDTO struct {
	StockUnitId string `json:"stockUnitId"`
	SKU         string `json:"sku"`
	Reserved    int    `json:"reserved"`
	Usable      int    `json:"usable"`
	State       string `json:"state"`
}

type blastRadiusDTO struct {
	SKU           string       `json:"sku"`
	BinId         string       `json:"binId"`
	ReservationId string       `json:"reservationId"`
	QuantityFreed int          `json:"quantityFreed"`
	BinLines      []binLineDTO `json:"binLines"`
}

type strandedReservationEvidenceDTO struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

type strandedReservationOutput struct {
	Detected      bool                             `json:"detected"`
	Action        string                           `json:"action"`
	ReservationId string                           `json:"reservationId,omitempty"`
	Rationale     string                           `json:"rationale"`
	Evidence      []strandedReservationEvidenceDTO `json:"evidence"`
	BlastRadius   *blastRadiusDTO                  `json:"blastRadius,omitempty"`
}

func (d Deps) detectStrandedReservation(ctx context.Context, in strandedReservationInput) (strandedReservationOutput, error) {
	result, err := d.StrandedReservation.Execute(ctx, usecases.StrandedReservationRequest{
		TaskType:           policy.TaskType(in.TaskType),
		WithinSeconds:      in.WithinSeconds,
		SKU:                in.SKU,
		MinUsableThreshold: in.MinUsableThreshold,
		ReservationId:      in.ReservationId,
		BinId:              in.BinId,
	})
	if err != nil {
		return strandedReservationOutput{}, err
	}

	evidence := make([]strandedReservationEvidenceDTO, 0, len(result.Evidence))
	for _, e := range result.Evidence {
		evidence = append(evidence, strandedReservationEvidenceDTO{Tool: e.Tool, Summary: e.Summary})
	}

	out := strandedReservationOutput{
		Detected:      result.Detected,
		Action:        string(result.Action),
		ReservationId: result.ReservationId,
		Rationale:     result.Rationale,
		Evidence:      evidence,
	}
	if result.BlastRadius != nil {
		lines := make([]binLineDTO, 0, len(result.BlastRadius.BinLines))
		for _, l := range result.BlastRadius.BinLines {
			lines = append(lines, binLineDTO{
				StockUnitId: l.StockUnitId,
				SKU:         l.SKU,
				Reserved:    l.Reserved,
				Usable:      l.Usable,
				State:       l.State,
			})
		}
		out.BlastRadius = &blastRadiusDTO{
			SKU:           result.BlastRadius.SKU,
			BinId:         result.BlastRadius.BinId,
			ReservationId: result.BlastRadius.ReservationId,
			QuantityFreed: result.BlastRadius.QuantityFreed,
			BinLines:      lines,
		}
	}
	return out, nil
}

// --- registration -----------------------------------------------------------

// registerTools adds every tool to the server, each wrapped so its handler
// runs inside an OTel span named "mcp.tool <name>". get_daily_brief and
// list_open_exceptions are always registered; get_flow_balance_exception,
// explain_travel_factor, detect_stranded_reservation and
// find_master_data_gaps are registered only when their use case is wired
// (see each one's own nil check below) — up to six tools total, every one
// of them read-only. This agent has no write tool at all.
func (d Deps) registerTools(server *mcp.Server) {
	readOnly := true

	addTool(server, &mcp.Tool{
		Name:        "get_daily_brief",
		Description: "Return the full synthesized daily operational brief: every monitored site's paths with their backlog, staffing, queue, and stuck-task facts, plus the correlated open exceptions across all paths, ranked critical-first.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getDailyBrief)

	addTool(server, &mcp.Tool{
		Name:        "list_open_exceptions",
		Description: "List the daily brief's correlated open exceptions, optionally filtered to a minimum severity (info, warning, or critical). Each exception carries its full evidence trail.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.listOpenExceptions)

	if d.FlowBalanceAdvisory != nil {
		addTool(server, &mcp.Tool{
			Name:        "get_flow_balance_exception",
			Description: "Correlate wes-work-planning's rebalance recommendation, workforce-management's staffing gap, and fulfillment-execution's stuck-task diagnostic for one process path into a single ranked FlowBalanceException recommendation (assign_labor, release_next_work, or hold), with its full evidence trail.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
		}, d.getFlowBalanceException)
	}

	if d.ExplainTravelFactor != nil {
		addTool(server, &mcp.Tool{
			Name:        "explain_travel_factor",
			Description: "Estimate the real travel distance facility-layout computes between two caller-supplied location codes and classify whether that distance is a materially significant contributor to a slow path, or negligible. The caller must already know both location codes — this tool never infers or guesses them.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
		}, d.explainTravelFactor)
	}

	if d.StrandedReservation != nil {
		addTool(server, &mcp.Tool{
			Name:        "detect_stranded_reservation",
			Description: "Correlate fulfillment-execution's expired/expiring task leases with inventory-storage's usable-stock shortfall for one SKU into a ranked StrandedReservationException recommendation (revoke_reservation or hold). A revoke is only ever recommended alongside its mandatory blast radius (which bin, how much stock would return to usable) — never on partial evidence. This tool only recommends; it never calls inventory-storage's revoke_reservation write tool itself.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
		}, d.detectStrandedReservation)
	}

	if d.MasterDataGaps != nil {
		addTool(server, &mcp.Tool{
			Name:        "find_master_data_gaps",
			Description: "List product-master products whose master data is missing or contradictory: unclassified (no handling classification, so hazmat/fragile/temperature handling is unknown downstream) and dimension-discrepancy (product-master's own flag that declared and measured unit dimensions disagree beyond its tolerance; both dimension sets are returned). Optional kind filter (unclassified or dimension-discrepancy); a scan covers at most 5,000 products per call and returns complete=false plus nextCursor to resume. Read-only: it only reports, it never classifies, declares or measures anything.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
		}, d.findMasterDataGaps)
	}

	if d.TransferWatch != nil {
		d.registerTransferWatchTools(server)
	}
}

// addTool registers one tool. It centralises the cross-cutting concern
// every tool shares: a span per call, and mapping a handler error onto the
// span before returning it.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		ctx, span := otel.Tracer(tracerName).Start(ctx, "mcp.tool "+tool.Name,
			trace.WithAttributes(
				attribute.String("mcp.tool.name", tool.Name),
			),
		)
		defer span.End()

		out, err := handle(ctx, in)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, zero, err
		}
		return nil, out, nil
	})
}
