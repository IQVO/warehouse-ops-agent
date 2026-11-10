// Command agent is the composition root for warehouse-ops-agent: it wires
// env config to the twenty-seven outbound MCP-client adapters (one per upstream
// bounded context; each is built only when its *_MCP_ENDPOINT is set, so
// an empty endpoint means no client and the use case reports that
// upstream as unavailable), the REST clients for the console-bff, and the
// telemetry and log readers (a real Prometheus reader when PROMETHEUS_URL
// is set, a no-op stub otherwise; a Loki reader when LOKI_URL is set),
// then wires those into the decision-support use cases (daily brief, flow
// balance, explain travel factor, stranded reservation, master-data gaps,
// inbound outlook, transfer watch, network order/node outlooks) and the
// console-bff and runtime-signals use cases. Every outbound http.Client
// carries an otelhttp transport (internal/adapters/outbound/httpclient).
// It serves them over BOTH an inbound HTTP endpoint and this agent's own
// inbound MCP server (up to twelve tools: see
// internal/adapters/inbound/mcp/tools.go) — a single process, two driving
// adapters over the same use cases, exactly the pattern the bounded
// contexts use for their own HTTP+MCP pair.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/logs"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/telemetry"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/observability"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func main() {
	if err := run(); err != nil {
		slog.Error("warehouse-ops-agent exited with error", "error", err)
		os.Exit(1)
	}
}

// run is the composition root's body: logger + observability, env config,
// every outbound client, every use case, both inbound adapters, then serve
// until SIGINT/SIGTERM. The wiring itself lives in the newX helpers (in this
// file, wire_clients.go and wire_decision_support.go),
// one per adapter family.
func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	rootCtx := context.Background()
	serviceName := observability.ServiceName()
	otelShutdown, err := observability.Setup(rootCtx, serviceName, observability.ServiceVersion(), observability.Endpoint())
	if err != nil {
		logger.Error("opentelemetry setup degraded", "error", err)
	}
	if otelShutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := otelShutdown(ctx); err != nil {
				logger.Error("opentelemetry shutdown failed", "error", err)
			}
		}()
	} else {
		logger.Warn("opentelemetry disabled; traces and metrics will not be exported")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	clients := newOutboundClients(cfg)

	decision := newDecisionSupport(cfg, clients)
	if err := wireReasoner(rootCtx, cfg, logger, decision.flowBalance); err != nil {
		return err
	}

	releaseWork, err := newReleaseWork(cfg, logger, decision.flowBalance)
	if err != nil {
		return err
	}

	laborOutlook, laborReallocation, err := newLaborReallocation(cfg, logger, clients.wfmSite)
	if err != nil {
		return err
	}

	mcpDeps := inboundmcp.Deps{
		DailyBrief:          decision.dailyBrief,
		FlowBalanceAdvisory: decision.flowBalance,
		ExplainTravelFactor: decision.explainTravelFactor,
		StrandedReservation: decision.strandedReservation,
		MasterDataGaps:      decision.masterDataGaps,
		InboundOutlook:      decision.inboundOutlook,
		TransferWatch:       decision.transferWatch,
		NetworkOrderOutlook: decision.networkOrderOutlook,
		NetworkNodeOutlook:  decision.networkNodeOutlook,
		// Only the token-less outlook: the act half (laborReallocation) is
		// REST-only and is NOT here, by design.
		LaborReallocationOutlook: laborOutlook,
	}
	mcpHandler := inboundmcp.Handler(inboundmcp.NewServer(mcpDeps))

	idempotency, err := newIdempotencyStore()
	if err != nil {
		return err
	}

	handlers := &inboundhttp.Handlers{
		DailyBrief:          decision.dailyBrief,
		FlowBalanceAdvisory: decision.flowBalance,
		ExplainTravelFactor: decision.explainTravelFactor,
		OrderLifecycle:      newOrderLifecycle(cfg),
		ConsoleReports:      newConsoleReports(cfg),
		RuntimeSignals:      newRuntimeSignals(cfg, clients.telemetry, clients.logs),
		MasterDataGaps:      decision.masterDataGaps,
		InboundOutlook:      decision.inboundOutlook,
		TransferWatch:       decision.transferWatch,
		// ReleaseWork is the ADR-0027 act slice, REST-only: it is NOT in
		// mcpDeps above, by design.
		ReleaseWork: releaseWork,
		// LaborReallocation is the ADR-0029 act slice #2, REST-only as well.
		LaborReallocation: laborReallocation,
		// Idempotency backs the Idempotency-Key on every act POST (ADR 0030).
		Idempotency: idempotency,
		// MCPHandler mounts /mcp on the SAME chi router as every REST
		// route (ADR-0010): otelchi trace, otelchimetric RED duration,
		// and RequestLogger all apply to MCP traffic too, not just REST.
		MCPHandler: mcpHandler,
		Logger:     logger,
	}

	return serveAgent(cfg, logger, serviceName, handlers)
}

// serveAgent serves the REST router (which also carries this agent's own
// MCP server, mounted at /mcp with the same otelchi+otelchimetric+
// RequestLogger middleware chain every REST route gets — ADR-0010) until
// SIGINT/SIGTERM, then drains in-flight requests for up to 10 seconds.
func serveAgent(cfg config.Config, logger *slog.Logger, serviceName string, handlers *inboundhttp.Handlers) error {
	router := inboundhttp.NewRouter(handlers, serviceName)

	srv := &http.Server{Addr: cfg.Addr, Handler: router, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("warehouse-ops-agent listening",
			"addr", cfg.Addr,
			"http_routes", httpRouteList(router),
			"mcp_route", "/mcp",
			"wes_work_planning_endpoint_configured", cfg.WesWorkPlanning.Endpoint != "",
			"fulfillment_execution_endpoint_configured", cfg.FulfillmentExecution.Endpoint != "",
			"inventory_storage_endpoint_configured", cfg.InventoryStorage.Endpoint != "",
			"workforce_management_endpoint_configured", cfg.WorkforceManagement.Endpoint != "",
			"workforce_management_a2a_configured", cfg.WorkforceManagementA2AURL != "",
			"facility_layout_endpoint_configured", cfg.FacilityLayout.Endpoint != "",
			"warehouse_planning_endpoint_configured", cfg.WarehousePlanning.Endpoint != "",
			"product_master_endpoint_configured", cfg.ProductMaster.Endpoint != "",
			"inbound_receiving_endpoint_configured", cfg.InboundReceiving.Endpoint != "",
			"network_inventory_planning_endpoint_configured", cfg.NetworkInventoryPlanning.Endpoint != "",
			"slotting_optimization_endpoint_configured", cfg.SlottingOptimization.Endpoint != "",
			"returns_processing_endpoint_configured", cfg.ReturnsProcessing.Endpoint != "",
			"exception_management_endpoint_configured", cfg.ExceptionManagement.Endpoint != "",
			"order_management_endpoint_configured", cfg.OrderManagement.Endpoint != "",
			"labor_performance_endpoint_configured", cfg.LaborPerformance.Endpoint != "",
			"customer_ordering_endpoint_configured", cfg.CustomerOrdering.Endpoint != "",
			"delivery_promise_endpoint_configured", cfg.DeliveryPromise.Endpoint != "",
			"last_mile_delivery_endpoint_configured", cfg.LastMileDelivery.Endpoint != "",
			"linehaul_transportation_endpoint_configured", cfg.LinehaulTransportation.Endpoint != "",
			"node_performance_endpoint_configured", cfg.NodePerformance.Endpoint != "",
			"node_topology_endpoint_configured", cfg.NodeTopology.Endpoint != "",
			"offer_availability_endpoint_configured", cfg.OfferAvailability.Endpoint != "",
			"order_sourcing_endpoint_configured", cfg.OrderSourcing.Endpoint != "",
			"shipping_and_tracking_endpoint_configured", cfg.ShippingAndTracking.Endpoint != "",
			"vendor_connectivity_endpoint_configured", cfg.VendorConnectivity.Endpoint != "",
			"seller_inbound_endpoint_configured", cfg.SellerInbound.Endpoint != "",
			"task_interleaving_endpoint_configured", cfg.TaskInterleaving.Endpoint != "",
			"pick_face_replenishment_endpoint_configured", cfg.PickFaceReplenishment.Endpoint != "",
			"process_path_management_endpoint_configured", cfg.ProcessPathManagement.Endpoint != "",
			"path_targets", len(cfg.PathTargets),
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// httpRouteList names every REST route the router serves (GET routes,
// /mcp excluded: it is logged separately), read from the router itself so
// the startup log can never drift from the registered routes.
func httpRouteList(router chi.Routes) string {
	var routes []string
	_ = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && route != "/mcp" {
			routes = append(routes, route)
		}
		return nil
	})
	return strings.Join(routes, ", ")
}

// toUseCaseTargets maps the config layer's []config.PathTarget onto the
// application layer's own []usecases.PathTarget. Kept as an explicit
// mapping (not a shared type) so internal/application never imports
// internal/config, preserving the hexagonal dependency rule enforced by
// internal/architecture/architecture_test.go.
func toUseCaseTargets(targets []config.PathTarget) []usecases.PathTarget {
	out := make([]usecases.PathTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, usecases.PathTarget{
			SiteCode:    t.SiteCode,
			PathId:      t.PathId,
			ProcessPath: t.ProcessPath,
			BuildingId:  t.BuildingId,
			ShiftId:     t.ShiftId,

			PlanningPathId:   t.PlanningPathId,
			UnitsPerOrder:    t.UnitsPerOrder,
			PackagesPerOrder: t.PackagesPerOrder,
		})
	}
	return out
}

// toPathTaskTypes maps a wes-work-planning pathId to its
// fulfillment-execution process-path/task-type name (PICK/PACK/SLAM),
// reusing the SAME config.PathTarget.ProcessPath binding DailyBrief
// already consumes (toUseCaseTargets, above) rather than inventing a
// second resolution mechanism for FlowBalanceAdvisory's labor-utilization
// correlation (ADR 0008).
func toPathTaskTypes(targets []config.PathTarget) map[string]string {
	out := make(map[string]string, len(targets))
	for _, t := range targets {
		if t.PathId != "" && t.ProcessPath != "" {
			out[t.PathId] = t.ProcessPath
		}
	}
	return out
}

// mcpAuthKeys previously read this agent's own inbound MCP server's bearer
// keys from config; removed with the fleet-wide auth removal (this
// agent's /mcp endpoint is now open, matching the five upstream servers).

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(observability.NewSlogHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}),
	))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newTelemetryReader builds the outbound TelemetryReader: a real
// Prometheus client when a base URL is configured, or a no-op StubReader
// (RuntimeSignals degrades gracefully -- see its Execute doc comment)
// when PROMETHEUS_URL is unset, e.g. a local dev run with no observability
// stack up.
func newTelemetryReader(prometheusURL string) ports.TelemetryReader {
	if prometheusURL == "" {
		return telemetry.NewStubReader()
	}
	return telemetry.NewPrometheusReader(prometheusURL, 5*time.Second)
}

// newLogReader builds the outbound LogReader: a real Loki client when a
// base URL is configured, or nil when LOKI_URL is unset -- RuntimeSignals
// treats a nil Logs port the same as a query error (source reported
// unavailable, never a panic).
func newLogReader(lokiURL string) ports.LogReader {
	if lokiURL == "" {
		return nil
	}
	return logs.NewLokiReader(lokiURL, 5*time.Second)
}
