// Package http is warehouse-ops-agent's inbound REST adapter: chi router,
// the handlers for its thirteen GET routes (/healthz, /daily-brief,
// /flow-balance/{pathId}, /explain-travel-factor,
// /console/orders/{id}/lifecycle, /console/reports/wms,
// /console/reports/wes, /runtime-signals, /master-data-gaps,
// /inbound-outlook, /transfer-watch/stuck, /transfer-watch/transfers/{id},
// /transfer-watch/imbalance) plus the optional /mcp mount, and
// DTOs. Domain/application structs never leak across this boundary.
//
// The only POST routes are the two act-slice routes (ADR 0027,
// act_release_work.go): POST /act/release-next-work/proposals and
// /approvals — the human-confirmed release of the next work unit.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
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

	// InboundOutlook is the ADR 0021 inbound-receiving use case. Nil (no
	// INBOUND_RECEIVING_MCP_ENDPOINT) answers 503 on GET /inbound-outlook.
	InboundOutlook *usecases.InboundOutlook

	// TransferWatch is the ADR-0019 read-only view of
	// network-inventory-planning. Nil is a valid value (same
	// 503-not-panic convention as the fields above): the three
	// /transfer-watch routes answer 503 while
	// NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT is unset.
	TransferWatch *usecases.TransferWatch

	// ReleaseWork is the ADR-0027 act slice: the human-gated
	// propose -> approve -> execute release of the next work unit. Nil (no
	// WES_WORK_PLANNING_A2A_URL) answers 503 on both /act routes. It is
	// wired here and NOWHERE else — never into the MCP server or the LLM
	// reasoner (asserted in internal/architecture/zerowrite).
	ReleaseWork *usecases.ReleaseWork
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
	r.Get("/inbound-outlook", h.getInboundOutlook)
	r.Get("/transfer-watch/stuck", h.getStuckTransfers)
	r.Get("/transfer-watch/transfers/{id}", h.getTransferStatus)
	r.Get("/transfer-watch/imbalance", h.getNetworkImbalance)

	// The act slice (ADR 0027): the only POST routes on this agent.
	r.Post("/act/release-next-work/proposals", h.postReleaseProposal)
	r.Post("/act/release-next-work/approvals", h.postReleaseApproval)

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
