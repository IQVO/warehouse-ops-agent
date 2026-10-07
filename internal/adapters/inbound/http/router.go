// Package http is warehouse-ops-agent's inbound REST adapter: chi router,
// the handlers for its twelve GET routes (/healthz, /daily-brief,
// /flow-balance/{pathId}, /explain-travel-factor,
// /console/orders/{id}/lifecycle, /console/reports/wms,
// /console/reports/wes, /runtime-signals, /master-data-gaps,
// /transfer-watch/stuck, /transfer-watch/transfers/{id},
// /transfer-watch/imbalance) plus the optional /mcp mount, and
// DTOs. Domain/application structs never leak across this boundary.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Handlers holds every use case the inbound HTTP adapter depends on.
type Handlers struct {
	DailyBrief *usecases.DailyBrief

	// FlowBalanceAdvisory is the E1 correlation use case (T2). Nil is a
	// valid value in tests that only exercise the daily-brief route;
	// getFlowBalanceException responds 503 rather than panicking when
	// nil (see its body).
	FlowBalanceAdvisory *usecases.FlowBalanceAdvisory

	// ExplainTravelFactor is the ADR-0009 use case. Nil is a valid
	// value (same 503-not-panic convention as FlowBalanceAdvisory).
	ExplainTravelFactor *usecases.ExplainTravelFactor

	// OrderLifecycle is the console-bff read model for the
	// warehouse-console shell's Order Lifecycle screen. Nil is a valid
	// value (same 503-not-panic convention as FlowBalanceAdvisory) for
	// any deployment that hasn't wired the four REST client upstreams.
	OrderLifecycle *usecases.OrderLifecycle

	// ConsoleReports is the console-bff's second capability: the WMS and
	// WES chart dashboards. Nil is a valid value (same 503-not-panic
	// convention as the fields above) for any deployment that hasn't
	// wired the seven analytics REST upstreams.
	ConsoleReports *usecases.ConsoleReports

	// RuntimeSignals is the Phase 5 Task 5.3 runtime-feedback use case.
	// Nil is a valid value (same 503-not-panic convention as the fields
	// above) for any deployment that hasn't wired it.
	RuntimeSignals *usecases.RuntimeSignals

	// MasterDataGaps is the ADR 0020 product-master use case. Nil (no
	// PRODUCT_MASTER_MCP_ENDPOINT) answers 503 on GET /master-data-gaps.
	MasterDataGaps *usecases.MasterDataGaps

	// TransferWatch is the ADR-0019 read-only view of
	// network-inventory-planning. Nil is a valid value (same
	// 503-not-panic convention as the fields above): the three
	// /transfer-watch routes answer 503 while
	// NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT is unset.
	TransferWatch *usecases.TransferWatch
	// MCPHandler is this agent's own inbound MCP server
	// (internal/adapters/inbound/mcp), mounted at /mcp on this SAME chi
	// router (ADR-0010) so MCP traffic gets the identical otelchi trace,
	// otelchimetric RED duration, and RequestLogger line every REST
	// route already gets — rather than a second http.ServeMux outside
	// this middleware chain. Nil is a valid value: /mcp is simply not
	// registered (e.g. in tests that only exercise REST routes).
	MCPHandler http.Handler

	// Logger receives the RequestLogger middleware's structured
	// "http request" line for every call, including /mcp. Defaults to
	// slog.Default() when nil.
	Logger *slog.Logger
}

// NewRouter wires every operational REST route. All routes are open; the
// fleet-wide auth removal (see the ADR superseding 0005) dropped the OIDC
// middleware this router used to require.
func NewRouter(h *Handlers, serviceName string) *chi.Mux {
	r := chi.NewRouter()

	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}

	metricCfg := otelchimetric.NewBaseConfig(serviceName)

	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(metricCfg))
	r.Use(otelchimetric.NewServerActiveRequests(metricCfg))
	r.Use(RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware())

	r.Get("/healthz", healthz)
	r.Get("/daily-brief", h.getDailyBrief)
	r.Get("/flow-balance/{pathId}", h.getFlowBalanceException)
	r.Get("/explain-travel-factor", h.getExplainTravelFactor)
	r.Get("/console/orders/{id}/lifecycle", h.getOrderLifecycle)
	r.Get("/console/reports/wms", h.getWMSDashboard)
	r.Get("/console/reports/wes", h.getWESDashboard)
	r.Get("/runtime-signals", h.getRuntimeSignals)
	r.Get("/master-data-gaps", h.getMasterDataGaps)
	r.Get("/transfer-watch/stuck", h.getStuckTransfers)
	r.Get("/transfer-watch/transfers/{id}", h.getTransferStatus)
	r.Get("/transfer-watch/imbalance", h.getNetworkImbalance)

	// /mcp: same router, same middleware chain as every REST route above
	// (ADR-0010) — NOT a second handler mounted outside it on a raw
	// ServeMux. chi.Mux.Handle matches every HTTP method at this exact
	// path, which is what Streamable HTTP needs (POST for messages, GET
	// for the SSE stream).
	if h.MCPHandler != nil {
		r.Handle("/mcp", h.MCPHandler)
	}

	return r
}

func healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// corsMiddleware allows the warehouse-console browser SPA to call this
// service's API (including the console-bff routes) directly from the
// browser. Every route is a read-only GET (plus the OPTIONS preflight), so
// only those methods are allowed cross-origin. CORS_ALLOWED_ORIGINS
// overrides the local-dev default (comma-separated) for staging/prod
// deployments.
func corsMiddleware() func(http.Handler) http.Handler {
	origins := []string{"http://localhost:5173"}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		origins = strings.Split(v, ",")
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{http.MethodGet, http.MethodOptions},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	})
}

func (h *Handlers) getDailyBrief(w http.ResponseWriter, r *http.Request) {
	brief := h.DailyBrief.Execute(r.Context())
	writeJSON(w, http.StatusOK, toDailyBriefDTO(brief))
}

// getFlowBalanceException handles GET /flow-balance/{pathId}?buildingId=&shiftId=.
// buildingId and shiftId are required query params (the workforce-management
// staffing-gap lookup needs them); a missing FlowBalanceAdvisory (not wired
// by the composition root) responds 503 rather than a nil-pointer panic.
func (h *Handlers) getFlowBalanceException(w http.ResponseWriter, r *http.Request) {
	if h.FlowBalanceAdvisory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "flow balance advisory not configured"})
		return
	}
	pathId := chi.URLParam(r, "pathId")
	buildingId := r.URL.Query().Get("buildingId")
	shiftId := r.URL.Query().Get("shiftId")

	decision, err := h.FlowBalanceAdvisory.Execute(r.Context(), buildingId, shiftId, pathId)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toFlowBalanceExceptionDTO(decision))
}

// getExplainTravelFactor handles
// GET /explain-travel-factor?pathId=&fromLocationCode=&toLocationCode=.
// fromLocationCode and toLocationCode are required query params -- the
// caller must already know both facility-layout location codes (see
// usecases.ExplainTravelFactor's own doc comment for why this handler
// never infers them). A missing ExplainTravelFactor (not wired by the
// composition root) responds 503 rather than a nil-pointer panic.
func (h *Handlers) getExplainTravelFactor(w http.ResponseWriter, r *http.Request) {
	if h.ExplainTravelFactor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "explain travel factor not configured"})
		return
	}
	pathId := r.URL.Query().Get("pathId")
	from := r.URL.Query().Get("fromLocationCode")
	to := r.URL.Query().Get("toLocationCode")

	result, err := h.ExplainTravelFactor.Execute(r.Context(), pathId, from, to)
	if err != nil {
		// Invalid caller input is a 400; anything else is facility-layout
		// being unreachable or rejecting the call -- an upstream
		// degradation, reported as 502 rather than blamed on the caller.
		status := http.StatusBadGateway
		if errors.Is(err, usecases.ErrInvalidInput) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toTravelFactorDTO(result))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// getRuntimeSignals handles GET /runtime-signals. Nil RuntimeSignals
// (not wired by the composition root) responds 503, same convention as
// every other optional use case in this router.
func (h *Handlers) getRuntimeSignals(w http.ResponseWriter, r *http.Request) {
	if h.RuntimeSignals == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "runtime signals not configured"})
		return
	}
	report := h.RuntimeSignals.Execute(r.Context())
	writeJSON(w, http.StatusOK, toRuntimeSignalsDTO(report))
}

// --- DTOs ------------------------------------------------------------------

type dailyBriefDTO struct {
	GeneratedAt    time.Time          `json:"generatedAt"`
	Sites          []siteBriefDTO     `json:"sites"`
	OpenExceptions []openExceptionDTO `json:"openExceptions"`
}

type siteBriefDTO struct {
	SiteCode string         `json:"siteCode"`
	SiteName string         `json:"siteName"`
	Paths    []pathBriefDTO `json:"paths"`
}

type pathBriefDTO struct {
	PathId      string             `json:"pathId"`
	ProcessPath string             `json:"processPath"`
	Backlog     *backlogFactDTO    `json:"backlog,omitempty"`
	Staffing    *staffingFactDTO   `json:"staffing,omitempty"`
	Queue       *queueFactDTO      `json:"queue,omitempty"`
	Stuck       *stuckTasksFactDTO `json:"stuck,omitempty"`
	Unavailable []string           `json:"unavailable,omitempty"`
	Exceptions  []openExceptionDTO `json:"exceptions,omitempty"`

	// CapacityOutlook is present only when warehouse-planning is configured
	// (ADR 0013); absent otherwise, so the brief is unchanged.
	CapacityOutlook *capacityOutlookDTO `json:"capacityOutlook,omitempty"`
}

type backlogFactDTO struct {
	BacklogDepth       int  `json:"backlogDepth"`
	WIP                int  `json:"wip"`
	OverAlarmThreshold bool `json:"overAlarmThreshold"`
}

type staffingFactDTO struct {
	PlannedHeads int  `json:"plannedHeads"`
	ActiveHeads  int  `json:"activeHeads"`
	Understaffed bool `json:"understaffed"`
}

type queueFactDTO struct {
	Depth int `json:"depth"`
}

type stuckTasksFactDTO struct {
	Count int `json:"count"`
}

type openExceptionDTO struct {
	Kind     string   `json:"kind"`
	SiteCode string   `json:"siteCode"`
	PathId   string   `json:"pathId"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
}

type flowBalanceEvidenceDTO struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
}

type utilizationCorrelationDTO struct {
	Kind      string `json:"kind"`
	Rationale string `json:"rationale"`
}

type flowBalanceExceptionDTO struct {
	PathId            string                     `json:"pathId"`
	RecommendedAction string                     `json:"recommendedAction"`
	ProposedHeads     int                        `json:"proposedHeads,omitempty"`
	Rationale         string                     `json:"rationale"`
	Partial           bool                       `json:"partial"`
	MissingSignals    []string                   `json:"missingSignals,omitempty"`
	Evidence          []flowBalanceEvidenceDTO   `json:"evidence"`
	Utilization       *utilizationCorrelationDTO `json:"utilization,omitempty"`
	// Source is the ADR-0004 arbitration source that produced this
	// Decision: deterministic, llm, or fallback. Omitted when empty
	// (tests/paths that never went through FlowBalanceAdvisory.arbitrate).
	Source string `json:"source,omitempty"`
}

func toFlowBalanceExceptionDTO(d policy.Decision) flowBalanceExceptionDTO {
	evidence := make([]flowBalanceEvidenceDTO, 0, len(d.Evidence))
	for _, e := range d.Evidence {
		evidence = append(evidence, flowBalanceEvidenceDTO{Source: e.Source, Detail: e.Detail})
	}
	dto := flowBalanceExceptionDTO{
		PathId:            d.PathId,
		RecommendedAction: string(d.RecommendedAction),
		ProposedHeads:     d.ProposedHeads,
		Rationale:         d.Rationale,
		Partial:           d.Partial,
		MissingSignals:    d.MissingSignals,
		Evidence:          evidence,
		Source:            string(d.Source),
	}
	if d.Utilization != nil {
		dto.Utilization = &utilizationCorrelationDTO{
			Kind:      string(d.Utilization.Kind),
			Rationale: d.Utilization.Rationale,
		}
	}
	return dto
}

// travelFactorDTO is the GET /explain-travel-factor response body.
// Kind/Rationale are omitted entirely (never a zero-valued/empty string
// masquerading as an outcome) when facility-layout was unreachable and
// no correlation could be produced.
type travelFactorDTO struct {
	MetresM   float64 `json:"metresM"`
	Estimated bool    `json:"estimated"`
	Kind      string  `json:"kind,omitempty"`
	Rationale string  `json:"rationale,omitempty"`
}

func toTravelFactorDTO(r usecases.TravelFactorResult) travelFactorDTO {
	dto := travelFactorDTO{}
	if r.Reading != nil {
		dto.MetresM = r.Reading.MetresM
		dto.Estimated = r.Reading.Estimated
	}
	if r.Correlation != nil {
		dto.Kind = string(r.Correlation.Kind)
		dto.Rationale = r.Correlation.Rationale
	}
	return dto
}

func toDailyBriefDTO(b policy.DailyBrief) dailyBriefDTO {
	dto := dailyBriefDTO{
		GeneratedAt:    b.GeneratedAt,
		OpenExceptions: toOpenExceptionDTOs(b.OpenExceptions),
	}
	for _, s := range b.Sites {
		dto.Sites = append(dto.Sites, toSiteBriefDTO(s))
	}
	return dto
}

func toSiteBriefDTO(s policy.SiteBrief) siteBriefDTO {
	dto := siteBriefDTO{SiteCode: s.SiteCode, SiteName: s.SiteName}
	for _, p := range s.Paths {
		dto.Paths = append(dto.Paths, toPathBriefDTO(p))
	}
	return dto
}

func toPathBriefDTO(p policy.PathBrief) pathBriefDTO {
	dto := pathBriefDTO{
		PathId:      p.Target.PathId,
		ProcessPath: p.Target.ProcessPath,
		Unavailable: p.Unavailable,
		Exceptions:  toOpenExceptionDTOs(p.Exceptions),
	}
	if p.Backlog != nil {
		dto.Backlog = &backlogFactDTO{
			BacklogDepth:       p.Backlog.BacklogDepth,
			WIP:                p.Backlog.WIP,
			OverAlarmThreshold: p.Backlog.OverAlarmThreshold,
		}
	}
	if p.Staffing != nil {
		dto.Staffing = &staffingFactDTO{
			PlannedHeads: p.Staffing.PlannedHeads,
			ActiveHeads:  p.Staffing.ActiveHeads,
			Understaffed: p.Staffing.Understaffed,
		}
	}
	if p.Queue != nil {
		dto.Queue = &queueFactDTO{Depth: p.Queue.Depth}
	}
	if p.Stuck != nil {
		dto.Stuck = &stuckTasksFactDTO{Count: p.Stuck.Count}
	}
	dto.CapacityOutlook = toCapacityOutlookDTO(p.CapacityOutlook)
	return dto
}

// capacityOutlookDTO is the optional warehouse-planning capacity section of
// a path brief (ADR 0013). Either OmittedReason is set (the section could not
// be produced; no capacity figure is present) or the rate/bottleneck fields
// are. NormalizedRate is ORDER per hour.
type capacityOutlookDTO struct {
	PlanningPathId    string            `json:"planningPathId,omitempty"`
	WindowStart       time.Time         `json:"windowStart"`
	WindowEnd         time.Time         `json:"windowEnd"`
	NormalizedRate    *float64          `json:"normalizedRate,omitempty"`
	BottleneckStep    string            `json:"bottleneckStep,omitempty"`
	BindingConstraint string            `json:"bindingConstraint,omitempty"`
	Steps             []capacityStepDTO `json:"steps,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	OmittedReason     string            `json:"omittedReason,omitempty"`
}

type capacityStepDTO struct {
	Step              string  `json:"step"`
	NormalizedRate    float64 `json:"normalizedRate"`
	BindingConstraint string  `json:"bindingConstraint,omitempty"`
}

func toCapacityOutlookDTO(o *policy.CapacityOutlook) *capacityOutlookDTO {
	if o == nil {
		return nil
	}
	dto := &capacityOutlookDTO{
		PlanningPathId: o.PlanningPathId,
		WindowStart:    o.WindowStart,
		WindowEnd:      o.WindowEnd,
		OmittedReason:  o.OmittedReason,
	}
	if o.OmittedReason != "" {
		return dto
	}
	rate := o.NormalizedRate
	dto.NormalizedRate = &rate
	dto.BottleneckStep = o.BottleneckStep
	dto.BindingConstraint = o.BindingConstraint
	dto.Warnings = o.Warnings
	for _, s := range o.Steps {
		dto.Steps = append(dto.Steps, capacityStepDTO{Step: s.Step, NormalizedRate: s.NormalizedRate, BindingConstraint: s.BindingConstraint})
	}
	return dto
}

func toOpenExceptionDTOs(exceptions []policy.OpenException) []openExceptionDTO {
	out := make([]openExceptionDTO, 0, len(exceptions))
	for _, e := range exceptions {
		out = append(out, openExceptionDTO{
			Kind:     string(e.Kind),
			SiteCode: e.SiteCode,
			PathId:   e.PathId,
			Severity: string(e.Severity),
			Summary:  e.Summary,
			Evidence: e.Evidence,
		})
	}
	return out
}

// --- console-bff: Order Lifecycle -------------------------------------

// getOrderLifecycle handles GET /console/orders/{id}/lifecycle. It never
// panics on an unwired OrderLifecycle (503, matching
// getFlowBalanceException's convention), and an order-management 404
// (the order genuinely doesn't exist) is reported as 404 rather than a
// generic 500. Everything else, per usecases.OrderLifecycle.Execute's
// contract and ADR 0002, degrades that one stage to null in a 200
// response -- Execute's only error return is ports.ErrNotFound, so there
// is deliberately no upstream-failure (502) branch here; surfacing
// upstream failures as 502 would contradict the partial-tolerant design.
func (h *Handlers) getOrderLifecycle(w http.ResponseWriter, r *http.Request) {
	if h.OrderLifecycle == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "order lifecycle not configured"})
		return
	}
	orderId := chi.URLParam(r, "id")

	result, err := h.OrderLifecycle.Execute(r.Context(), orderId)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
			return
		}
		// Defensive only: Execute does not return any other error today.
		// If that contract ever widens, fail loudly as an internal error
		// rather than mislabelling it an upstream (502) failure.
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, toOrderLifecycleDTO(result))
}

// orderLifecycleDTO's field names/shapes are hand-kept in sync with
// warehouse-console's src/features/order-lifecycle/types.ts OrderLifecycle
// interface -- the console is this endpoint's only consumer today.
type orderLifecycleDTO struct {
	OrderId         string               `json:"orderId"`
	OrderManagement *orderStageDTO       `json:"orderManagement"`
	Inventory       *inventoryStageDTO   `json:"inventory"`
	Planning        *planningStageDTO    `json:"planning"`
	Fulfillment     *fulfillmentStageDTO `json:"fulfillment"`
}

type orderStageDTO struct {
	Status               string         `json:"status"`
	AllowPartialShipment bool           `json:"allowPartialShipment"`
	PromiseDate          *string        `json:"promiseDate"`
	Lines                []orderLineDTO `json:"lines"`
	ReceivedAt           *string        `json:"receivedAt"`
}

type orderLineDTO struct {
	LineNo   int    `json:"lineNo"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

type inventoryStageDTO struct {
	Reservations []reservationLineDTO `json:"reservations"`
}

type reservationLineDTO struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

type planningStageDTO struct {
	WorkUnits []workUnitLineDTO `json:"workUnits"`
}

type workUnitLineDTO struct {
	WorkUnitId string `json:"workUnitId"`
	PathId     string `json:"pathId"`
	Status     string `json:"status"`
	Fragile    bool   `json:"fragile"`
	GiftWrap   bool   `json:"giftWrap"`
}

type fulfillmentStageDTO struct {
	Tasks         []taskLineDTO `json:"tasks"`
	PackageSealed bool          `json:"packageSealed"`
	LabelApplied  bool          `json:"labelApplied"`
}

type taskLineDTO struct {
	TaskId            string  `json:"taskId"`
	TaskType          string  `json:"taskType"`
	Status            string  `json:"status"`
	StationId         *string `json:"stationId"`
	WeightDiscrepancy bool    `json:"weightDiscrepancy"`
	LeaseExpiredCount int     `json:"leaseExpiredCount"`
}

func toOrderLifecycleDTO(r usecases.OrderLifecycleResult) orderLifecycleDTO {
	dto := orderLifecycleDTO{OrderId: r.OrderId}

	if r.OrderManagement != nil {
		lines := make([]orderLineDTO, 0, len(r.OrderManagement.Lines))
		for _, l := range r.OrderManagement.Lines {
			lines = append(lines, orderLineDTO{
				LineNo:   l.LineNo,
				SKU:      l.SKU,
				Quantity: l.Quantity,
				Status:   l.Status,
			})
		}
		dto.OrderManagement = &orderStageDTO{
			Status:               r.OrderManagement.Status,
			AllowPartialShipment: r.OrderManagement.AllowPartialShipment,
			PromiseDate:          r.OrderManagement.PromiseDate,
			Lines:                lines,
		}
	}

	if r.Inventory != nil {
		reservations := make([]reservationLineDTO, 0, len(r.Inventory))
		for _, res := range r.Inventory {
			reservations = append(reservations, reservationLineDTO{
				SKU:      res.SKU,
				Quantity: res.Quantity,
				Status:   res.Status,
			})
		}
		dto.Inventory = &inventoryStageDTO{Reservations: reservations}
	}

	if r.Planning != nil {
		units := make([]workUnitLineDTO, 0, len(r.Planning))
		for _, wu := range r.Planning {
			units = append(units, workUnitLineDTO{
				WorkUnitId: wu.Id,
				PathId:     wu.PathId,
				Status:     wu.State,
				Fragile:    false, // wes's WorkUnitDTO doesn't carry a fragile flag today; see PR notes.
				GiftWrap:   wu.GiftWrap,
			})
		}
		dto.Planning = &planningStageDTO{WorkUnits: units}
	}

	if r.Fulfillment != nil {
		tasks := make([]taskLineDTO, 0, len(r.Fulfillment))
		sealed := false
		for _, t := range r.Fulfillment {
			tasks = append(tasks, taskLineDTO{
				TaskId:    t.Id,
				TaskType:  t.Type,
				Status:    t.Status,
				StationId: t.LeaseStationId,
			})
			if t.Type == "SLAM" && t.Status == "COMPLETED" {
				sealed = true
			}
		}
		dto.Fulfillment = &fulfillmentStageDTO{
			Tasks:         tasks,
			PackageSealed: sealed,
			LabelApplied:  sealed,
		}
	}

	return dto
}

// --- console-bff: WMS / WES report dashboards -------------------------

// getWMSDashboard handles GET /console/reports/wms?from=&to=.
func (h *Handlers) getWMSDashboard(w http.ResponseWriter, r *http.Request) {
	h.serveDashboard(w, r, func(ctx context.Context, from, to time.Time) usecases.DashboardResult {
		return h.ConsoleReports.ExecuteWMS(ctx, from, to)
	})
}

// getWESDashboard handles GET /console/reports/wes?from=&to=.
func (h *Handlers) getWESDashboard(w http.ResponseWriter, r *http.Request) {
	h.serveDashboard(w, r, func(ctx context.Context, from, to time.Time) usecases.DashboardResult {
		return h.ConsoleReports.ExecuteWES(ctx, from, to)
	})
}

// serveDashboard is the shared request handling for both dashboards:
// the same optional-window parsing, the same 503-when-unwired
// convention, and the same envelope.
//
// Unlike the seven upstreams these fan out to -- where from and to are
// REQUIRED RFC3339 params -- both are OPTIONAL here, defaulting to a
// 24-hour trailing window ending now. This is a screen a human opens
// without necessarily knowing what window to ask for; making them state
// one before seeing anything would be a worse default than showing them
// the last day. A param that IS supplied must still be well-formed: a
// malformed timestamp is the caller's bug and gets a 400, never a
// silently substituted default that would answer a question nobody asked.
func (h *Handlers) serveDashboard(w http.ResponseWriter, r *http.Request, execute func(context.Context, time.Time, time.Time) usecases.DashboardResult) {
	if h.ConsoleReports == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "console reports not configured"})
		return
	}

	from, err := parseOptionalTime(r.URL.Query().Get("from"), "from")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	to, err := parseOptionalTime(r.URL.Query().Get("to"), "to")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resolvedFrom, resolvedTo := h.ConsoleReports.ResolveWindow(from, to)
	if !resolvedTo.After(resolvedFrom) {
		// Rejected here rather than fanned out: an inverted window would
		// make every upstream either 400 or return nothing, degrading
		// all sections at once and reporting "the contexts are down"
		// when in fact the request was wrong.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query parameter 'to' must be after 'from'"})
		return
	}

	result := execute(r.Context(), resolvedFrom, resolvedTo)
	writeJSON(w, http.StatusOK, toDashboardDTO(result))
}

// parseOptionalTime parses an RFC3339 timestamp, returning nil (not an
// error) when the param is absent so the use case can apply its default
// window.
func parseOptionalTime(raw, name string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("query parameter '%s' must be an RFC3339 timestamp", name)
	}
	return &t, nil
}

// dashboardDTO is the wire envelope both /console/reports/wms and
// /console/reports/wes return -- deliberately IDENTICAL in shape, so the
// console renders either dashboard with one component that switches only
// on each section's chartKind.
type dashboardDTO struct {
	From        string             `json:"from"`
	To          string             `json:"to"`
	GeneratedAt string             `json:"generatedAt"`
	Sections    []reportSectionDTO `json:"sections"`
}

// reportSectionDTO is one panel. error and freshnessLagSeconds are
// pointers so they serialise as explicit JSON null rather than being
// omitted: the console distinguishes "available, no staleness known"
// from "unavailable, here is why", and an absent key would collapse both
// into undefined.
type reportSectionDTO struct {
	Id                  string           `json:"id"`
	Title               string           `json:"title"`
	SourceContext       string           `json:"sourceContext"`
	ChartKind           string           `json:"chartKind"`
	Available           bool             `json:"available"`
	Error               *string          `json:"error"`
	FreshnessLagSeconds *float64         `json:"freshnessLagSeconds"`
	Series              []seriesPointDTO `json:"series"`
}

// seriesPointDTO's shape is the contract with warehouse-ui-kit: its
// FunnelChart, BarChart and LineChart all take a {label, value}[] `data`
// prop, so all three chartKinds share this one point type.
type seriesPointDTO struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

func toDashboardDTO(r usecases.DashboardResult) dashboardDTO {
	sections := make([]reportSectionDTO, 0, len(r.Sections))
	for _, s := range r.Sections {
		sections = append(sections, toReportSectionDTO(s))
	}
	return dashboardDTO{
		From:        r.From.UTC().Format(time.RFC3339),
		To:          r.To.UTC().Format(time.RFC3339),
		GeneratedAt: r.GeneratedAt.UTC().Format(time.RFC3339),
		Sections:    sections,
	}
}

func toReportSectionDTO(s usecases.ReportSection) reportSectionDTO {
	// Built with make(..., 0, n) so an empty or degraded section
	// marshals as [] rather than null -- a chart component binding to
	// null is a client-side crash, binding to [] is an honest empty
	// chart. Same rule labor-performance's own reports adapter applies.
	series := make([]seriesPointDTO, 0, len(s.Series))
	for _, p := range s.Series {
		series = append(series, seriesPointDTO{Label: p.Label, Value: p.Value})
	}

	dto := reportSectionDTO{
		Id:                  s.Id,
		Title:               s.Title,
		SourceContext:       s.SourceContext,
		ChartKind:           s.ChartKind,
		Available:           s.Available,
		FreshnessLagSeconds: s.FreshnessLagSeconds,
		Series:              series,
	}
	if s.Error != "" {
		errMsg := s.Error
		dto.Error = &errMsg
	}
	return dto
}

// --- runtime signals (Phase 5 Task 5.3) -------------------------------

type runtimeSignalsDTO struct {
	GeneratedAt        string             `json:"generatedAt"`
	Services           []serviceSignalDTO `json:"services"`
	UnavailableSources []string           `json:"unavailableSources,omitempty"`
}

type serviceSignalDTO struct {
	ServiceName      string  `json:"serviceName"`
	Severity         string  `json:"severity"`
	ErrorRate        float64 `json:"errorRate"`
	ErrorRateSev     string  `json:"errorRateSeverity"`
	LatencyP99MS     float64 `json:"latencyP99Ms"`
	LatencyP99Sev    string  `json:"latencyP99Severity"`
	RecentErrorLogs  int     `json:"recentErrorLogs"`
	SampleWindowMins int     `json:"sampleWindowMins"`
}

func toRuntimeSignalsDTO(r policy.RuntimeSignalsReport) runtimeSignalsDTO {
	dto := runtimeSignalsDTO{
		GeneratedAt:        r.GeneratedAt,
		UnavailableSources: r.UnavailableSources,
	}
	for _, s := range r.Services {
		dto.Services = append(dto.Services, serviceSignalDTO{
			ServiceName:      s.ServiceName,
			Severity:         string(s.Severity()),
			ErrorRate:        s.ErrorRate,
			ErrorRateSev:     string(s.ErrorRateSev),
			LatencyP99MS:     s.LatencyP99MS,
			LatencyP99Sev:    string(s.LatencyP99Sev),
			RecentErrorLogs:  s.RecentErrorLogs,
			SampleWindowMins: s.SampleWindowMins,
		})
	}
	return dto
}
