// Command agent is the composition root for warehouse-ops-agent: it wires
// env config to the five outbound MCP-client adapters (one per upstream
// bounded context) and the telemetry-reader stub, then wires those into
// the DailyBrief (E3) use case and serves it over BOTH an inbound HTTP
// endpoint and this agent's own inbound MCP server (get_daily_brief,
// list_open_exceptions) — a single process, two driving adapters over the
// same use case, exactly the pattern the five bounded contexts use for
// their own HTTP+MCP pair.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	inboundmcp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/mcp"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/logs"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/mcpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/restclient"
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
// until SIGINT/SIGTERM. The wiring itself lives in the newX helpers below,
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

	cfg := config.Load()
	clients := newOutboundClients(cfg)

	decision := newDecisionSupport(cfg, clients)
	if err := wireReasoner(rootCtx, cfg, logger, decision.flowBalance); err != nil {
		return err
	}

	handlers := &inboundhttp.Handlers{
		DailyBrief:          decision.dailyBrief,
		FlowBalanceAdvisory: decision.flowBalance,
		ExplainTravelFactor: decision.explainTravelFactor,
		OrderLifecycle:      newOrderLifecycle(cfg),
		ConsoleReports:      newConsoleReports(cfg),
		RuntimeSignals:      newRuntimeSignals(cfg, clients.telemetry, clients.logs),
	}
	mcpDeps := inboundmcp.Deps{
		DailyBrief:          decision.dailyBrief,
		FlowBalanceAdvisory: decision.flowBalance,
		ExplainTravelFactor: decision.explainTravelFactor,
		StrandedReservation: decision.strandedReservation,
	}

	return serveAgent(cfg, logger, serviceName, handlers, mcpDeps)
}

// outboundClients bundles every outbound adapter the use cases consume,
// built from env config. Each MCP client satisfies its internal/ports
// interface at compile time (see the var _ assertions in
// internal/adapters/outbound/mcpclient/*.go).
type outboundClients struct {
	wes      ports.WesWorkPlanningClient
	fe       ports.FulfillmentExecutionClient
	wfm      ports.WorkforceManagementClient
	facility ports.FacilityLayoutClient
	inv      ports.InventoryStorageClient
	lp       ports.LaborPerformanceClient
	// planning is nil when WAREHOUSE_PLANNING_MCP_ENDPOINT is unset: the
	// capacity outlook is then not wired at all (ADR 0013). Unlike its
	// siblings it is assigned only when configured, so a nil check on this
	// interface is a genuine "not configured".
	planning  ports.WarehousePlanningClient
	telemetry ports.TelemetryReader
	logs      ports.LogReader
}

// newPlanningClient builds the warehouse-planning MCP client (read tools
// only), or returns a nil interface when no endpoint is configured so boot
// never depends on it and the capacity outlook degrades to "absent".
func newPlanningClient(cfg config.Config) ports.WarehousePlanningClient {
	if cfg.WarehousePlanning.Endpoint == "" {
		return nil
	}
	return mcpclient.NewWarehousePlanning(mcpclient.Config{
		Name:     "warehouse-planning",
		Endpoint: cfg.WarehousePlanning.Endpoint,
	})
}

// newOutboundClients builds every outbound MCP client (one per upstream
// bounded context) plus the telemetry and log readers.
func newOutboundClients(cfg config.Config) outboundClients {
	var (
		om ports.OrderManagementMCPClient = mcpclient.NewOrderManagement(mcpclient.Config{
			Name:     "order-management",
			Endpoint: cfg.OrderManagement.Endpoint,
		})
		ppm ports.ProcessPathManagementClient = mcpclient.NewProcessPathManagement(mcpclient.Config{
			Name:     "process-path-management",
			Endpoint: cfg.ProcessPathManagement.Endpoint,
		})
	)
	_ = om  // not used by the E3 daily brief; kept wired for a future use case.
	_ = ppm // not used by the E3 daily brief; kept wired for a future use case.
	// inv (below) is consumed by strandedReservation; no `_ =` marker needed.

	return outboundClients{
		wes: mcpclient.NewWesWorkPlanning(mcpclient.Config{
			Name:     "wes-work-planning",
			Endpoint: cfg.WesWorkPlanning.Endpoint,
		}),
		fe: mcpclient.NewFulfillmentExecution(mcpclient.Config{
			Name:     "fulfillment-execution",
			Endpoint: cfg.FulfillmentExecution.Endpoint,
		}),
		wfm: mcpclient.NewWorkforceManagement(mcpclient.Config{
			Name:     "workforce-management",
			Endpoint: cfg.WorkforceManagement.Endpoint,
		}),
		facility: mcpclient.NewFacilityLayout(mcpclient.Config{
			Name:     "facility-layout",
			Endpoint: cfg.FacilityLayout.Endpoint,
		}),
		inv: mcpclient.NewInventoryStorage(mcpclient.Config{
			Name:     "inventory-storage",
			Endpoint: cfg.InventoryStorage.Endpoint,
		}),
		telemetry: newTelemetryReader(cfg.PrometheusURL),
		lp: mcpclient.NewLaborPerformance(mcpclient.Config{
			Name:     "labor-performance",
			Endpoint: cfg.LaborPerformance.Endpoint,
		}),
		logs:     newLogReader(cfg.LokiURL),
		planning: newPlanningClient(cfg),
	}
}

// decisionSupport bundles the MCP-Customer / decision-support use case
// family: the daily brief (E3), the flow-balance advisory (E1) with its
// ADR-0008 utilization overlay, explain-travel-factor (ADR 0009), and the
// E2 stranded-reservation correlation.
type decisionSupport struct {
	dailyBrief          *usecases.DailyBrief
	flowBalance         *usecases.FlowBalanceAdvisory
	explainTravelFactor *usecases.ExplainTravelFactor
	strandedReservation *usecases.DetectStrandedReservation
}

// newDecisionSupport wires the decision-support use cases over the
// outbound MCP clients.
func newDecisionSupport(cfg config.Config, clients outboundClients) decisionSupport {
	flowBalance := &usecases.FlowBalanceAdvisory{
		Wes:           clients.wes,
		WFM:           clients.wfm,
		FE:            clients.fe,
		LP:            clients.lp,
		PathTaskTypes: toPathTaskTypes(cfg.PathTargets),
	}
	dailyBrief := &usecases.DailyBrief{
		Facility: clients.facility,
		Wes:      clients.wes,
		Fe:       clients.fe,
		Wfm:      clients.wfm,
		Targets:  toUseCaseTargets(cfg.PathTargets),
	}
	// Capacity outlook (ADR 0013): only wired when warehouse-planning is
	// configured. A nil Outlook leaves the daily brief byte-for-byte as it
	// was; a configured one is fail-open per path.
	if clients.planning != nil {
		dailyBrief.Outlook = &usecases.CapacityOutlook{
			Planning: clients.planning,
			Horizon:  cfg.CapacityOutlookHorizon,
		}
	}

	// StrandedReservation is the E2 correlation use case: read-only,
	// consuming the already-wired fulfillment-execution and
	// inventory-storage clients above. It only ever recommends a
	// revoke_reservation; it never calls one.
	strandedReservation := &usecases.DetectStrandedReservation{
		FulfillmentExecution: clients.fe,
		InventoryStorage:     clients.inv,
	}

	return decisionSupport{
		dailyBrief:          dailyBrief,
		flowBalance:         flowBalance,
		explainTravelFactor: &usecases.ExplainTravelFactor{Facility: clients.facility},
		strandedReservation: strandedReservation,
	}
}

// newOrderLifecycle wires the console-bff order-lifecycle use case. Its
// REST clients are separate from the MCP clients above (see
// internal/ports/order_lifecycle_clients.go's doc comment for why these
// are a deliberately distinct port shape).
func newOrderLifecycle(cfg config.Config) *usecases.OrderLifecycle {
	var orderMgmtClient ports.OrderManagementClient = restclient.NewOrderManagement(cfg.OrderManagementRESTURL, 5*time.Second)
	return &usecases.OrderLifecycle{
		OrderManagement: &orderMgmtClient,
		Inventory:       restclient.NewInventoryReservations(cfg.InventoryStorageRESTURL, 5*time.Second),
		WorkUnits:       restclient.NewWorkUnits(cfg.WesWorkPlanningRESTURL, 5*time.Second),
		Tasks:           restclient.NewTasksByOrder(cfg.FulfillmentExecutionRESTURL, 5*time.Second),
	}
}

// newConsoleReports wires the console-bff WMS/WES dashboards: a THIRD set
// of clients, pointed at each context's *-reports READER binary rather
// than its OLTP API (different process, different analytical database,
// different base URL) -- see internal/ports/console_reports_clients.go.
func newConsoleReports(cfg config.Config) *usecases.ConsoleReports {
	return &usecases.ConsoleReports{
		OrderFunnel:           restclient.NewOrderFunnelReports(cfg.OrderManagementReportsRESTURL, 5*time.Second),
		InventoryFlowAccuracy: restclient.NewFlowAccuracyReports(cfg.InventoryStorageReportsRESTURL, 5*time.Second),
		CatalogGrowth:         restclient.NewCatalogGrowthReports(cfg.FacilityLayoutReportsRESTURL, 5*time.Second),
		PlanningThroughput:    restclient.NewPlanningThroughputReports(cfg.WesWorkPlanningReportsRESTURL, 5*time.Second),
		FulfillmentThroughput: restclient.NewFulfillmentThroughputReports(cfg.FulfillmentExecutionReportsRESTURL, 5*time.Second),
		Labor:                 restclient.NewLaborReports(cfg.WorkforceManagementReportsRESTURL, 5*time.Second),
		LaborPerformance:      restclient.NewLaborPerformanceReports(cfg.LaborPerformanceReportsRESTURL, 5*time.Second),
	}
}

// newRuntimeSignals wires the runtime-signals report over the telemetry
// (Prometheus) and log (Loki) readers, with a 10-minute rolling window.
func newRuntimeSignals(cfg config.Config, telemetryReader ports.TelemetryReader, logReader ports.LogReader) *usecases.RuntimeSignals {
	return &usecases.RuntimeSignals{
		Telemetry:     telemetryReader,
		Logs:          logReader,
		Services:      cfg.RuntimeSignalsServices,
		Namespace:     cfg.RuntimeSignalsNamespace,
		WindowMinutes: 10,
	}
}

// serveAgent mounts the REST router and this agent's own MCP server on one
// mux, serves until SIGINT/SIGTERM, then drains in-flight requests for up
// to 10 seconds.
func serveAgent(cfg config.Config, logger *slog.Logger, serviceName string, handlers *inboundhttp.Handlers, mcpDeps inboundmcp.Deps) error {
	router := inboundhttp.NewRouter(handlers, serviceName)
	mcpHandler := inboundmcp.Handler(inboundmcp.NewServer(mcpDeps))

	mux := http.NewServeMux()
	mux.Handle("/", router)
	mux.Handle("/mcp", mcpHandler)

	srv := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("warehouse-ops-agent listening",
			"addr", cfg.Addr,
			"http_routes", "/healthz, /daily-brief, /flow-balance/{pathId}, /explain-travel-factor, /console/orders/{id}/lifecycle, /console/reports/wms, /console/reports/wes, /runtime-signals",
			"mcp_route", "/mcp",
			"wes_work_planning_endpoint_configured", cfg.WesWorkPlanning.Endpoint != "",
			"fulfillment_execution_endpoint_configured", cfg.FulfillmentExecution.Endpoint != "",
			"inventory_storage_endpoint_configured", cfg.InventoryStorage.Endpoint != "",
			"workforce_management_endpoint_configured", cfg.WorkforceManagement.Endpoint != "",
			"facility_layout_endpoint_configured", cfg.FacilityLayout.Endpoint != "",
			"warehouse_planning_endpoint_configured", cfg.WarehousePlanning.Endpoint != "",
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
