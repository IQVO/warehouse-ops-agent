// Command agent is the composition root for warehouse-ops-agent: it wires
// env config to the twelve outbound MCP-client adapters (one per upstream
// bounded context; each is built only when its *_MCP_ENDPOINT is set, so
// an empty endpoint means no client and the use case reports that
// upstream as unavailable), the REST clients for the console-bff, and the
// telemetry and log readers (a real Prometheus reader when PROMETHEUS_URL
// is set, a no-op stub otherwise; a Loki reader when LOKI_URL is set),
// then wires those into the decision-support use cases (daily brief, flow
// balance, explain travel factor, stranded reservation, master-data gaps,
// inbound outlook, transfer watch) and the console-bff and
// runtime-signals use cases. Every outbound http.Client carries an
// otelhttp transport (internal/adapters/outbound/httpclient). It serves
// them over BOTH an inbound HTTP endpoint and this agent's own inbound MCP
// server (up to ten tools: see internal/adapters/inbound/mcp/tools.go) —
// a single process, two driving adapters over the same use cases, exactly
// the pattern the bounded contexts use for their own HTTP+MCP pair.
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

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	clients := newOutboundClients(cfg)

	decision := newDecisionSupport(cfg, clients)
	if err := wireReasoner(rootCtx, cfg, logger, decision.flowBalance); err != nil {
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
	}
	mcpHandler := inboundmcp.Handler(inboundmcp.NewServer(mcpDeps))

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
		// MCPHandler mounts /mcp on the SAME chi router as every REST
		// route (ADR-0010): otelchi trace, otelchimetric RED duration,
		// and RequestLogger all apply to MCP traffic too, not just REST.
		MCPHandler: mcpHandler,
		Logger:     logger,
	}

	return serveAgent(cfg, logger, serviceName, handlers)
}

// outboundClients bundles every outbound adapter the use cases consume,
// built from env config. Each MCP client satisfies its internal/ports
// interface at compile time (see the var _ assertions in
// internal/adapters/outbound/mcpclient/*.go) and is a genuinely nil
// interface when its endpoint is unset (newMCPClient), so a nil check on
// any of them means "not configured".
type outboundClients struct {
	// om backs the daily brief's promise-health section (ADR 0024).
	om       ports.OrderManagementMCPClient
	wes      ports.WesWorkPlanningClient
	fe       ports.FulfillmentExecutionClient
	wfm      ports.WorkforceManagementClient
	facility ports.FacilityLayoutClient
	inv      ports.InventoryStorageClient
	lp       ports.LaborPerformanceClient
	// planning is nil when WAREHOUSE_PLANNING_MCP_ENDPOINT is unset: the
	// capacity outlook is then not wired at all (ADR 0013).
	planning ports.WarehousePlanningClient
	// productMaster is nil when PRODUCT_MASTER_MCP_ENDPOINT is unset: the
	// master-data-gaps use case is then not wired (ADR 0020).
	productMaster ports.ProductMasterClient
	// inboundReceiving is nil when INBOUND_RECEIVING_MCP_ENDPOINT is unset:
	// the inbound outlook is then not wired (ADR 0021).
	inboundReceiving ports.InboundReceivingClient
	// nip is nil when NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT is unset:
	// the transfer watch is then not wired at all (ADR 0019).
	nip ports.NetworkInventoryPlanningClient
	// slotting is nil when SLOTTING_OPTIMIZATION_MCP_ENDPOINT is unset:
	// the daily brief's slot-plan watch is then not wired (ADR 0024).
	slotting  ports.SlottingOptimizationClient
	telemetry ports.TelemetryReader
	logs      ports.LogReader
}

// newMCPClient builds one upstream's MCP client, or returns the zero
// value of the port interface P (a genuinely nil interface, never a
// non-nil interface around a nil pointer) when endpoint is empty. No
// client is ever built for an empty endpoint, so boot never depends on an
// upstream and no call is ever dialled to "".
func newMCPClient[P any](name, endpoint string, build func(mcpclient.Config) P) P {
	var none P
	if endpoint == "" {
		return none
	}
	return build(mcpclient.Config{Name: name, Endpoint: endpoint})
}

// newProductMasterClient builds the product-master MCP client (its four
// read tools only), or nil when no endpoint is configured (fail-open).
func newProductMasterClient(cfg config.Config) ports.ProductMasterClient {
	return newMCPClient("product-master", cfg.ProductMaster.Endpoint, func(c mcpclient.Config) ports.ProductMasterClient {
		return mcpclient.NewProductMaster(c)
	})
}

// newInboundReceivingClient builds the inbound-receiving MCP client (its
// seven read tools only), or nil when no endpoint is configured
// (fail-open).
func newInboundReceivingClient(cfg config.Config) ports.InboundReceivingClient {
	return newMCPClient("inbound-receiving", cfg.InboundReceiving.Endpoint, func(c mcpclient.Config) ports.InboundReceivingClient {
		return mcpclient.NewInboundReceiving(c)
	})
}

// newNIPClient builds the network-inventory-planning MCP client (read
// tools only), or nil when no endpoint is configured.
func newNIPClient(cfg config.Config) ports.NetworkInventoryPlanningClient {
	return newMCPClient("network-inventory-planning", cfg.NetworkInventoryPlanning.Endpoint, func(c mcpclient.Config) ports.NetworkInventoryPlanningClient {
		return mcpclient.NewNetworkInventoryPlanning(c)
	})
}

// newSlottingClient builds the slotting-optimization MCP client (its two
// slot-plan read tools only, ADR 0024), or nil when no endpoint is
// configured (fail-open).
func newSlottingClient(cfg config.Config) ports.SlottingOptimizationClient {
	return newMCPClient("slotting-optimization", cfg.SlottingOptimization.Endpoint, func(c mcpclient.Config) ports.SlottingOptimizationClient {
		return mcpclient.NewSlottingOptimization(c)
	})
}

// newPlanningClient builds the warehouse-planning MCP client (read tools
// only), or nil when no endpoint is configured, so the capacity outlook
// degrades to "absent".
func newPlanningClient(cfg config.Config) ports.WarehousePlanningClient {
	return newMCPClient("warehouse-planning", cfg.WarehousePlanning.Endpoint, func(c mcpclient.Config) ports.WarehousePlanningClient {
		return mcpclient.NewWarehousePlanning(c)
	})
}

// newOutboundClients builds every outbound MCP client (one per upstream
// bounded context, each only when its endpoint is set) plus the telemetry
// and log readers.
func newOutboundClients(cfg config.Config) outboundClients {
	return outboundClients{
		om: newMCPClient("order-management", cfg.OrderManagement.Endpoint, func(c mcpclient.Config) ports.OrderManagementMCPClient {
			return mcpclient.NewOrderManagement(c)
		}),
		wes: newMCPClient("wes-work-planning", cfg.WesWorkPlanning.Endpoint, func(c mcpclient.Config) ports.WesWorkPlanningClient {
			return mcpclient.NewWesWorkPlanning(c)
		}),
		fe: newMCPClient("fulfillment-execution", cfg.FulfillmentExecution.Endpoint, func(c mcpclient.Config) ports.FulfillmentExecutionClient {
			return mcpclient.NewFulfillmentExecution(c)
		}),
		wfm: newMCPClient("workforce-management", cfg.WorkforceManagement.Endpoint, func(c mcpclient.Config) ports.WorkforceManagementClient {
			return mcpclient.NewWorkforceManagement(c)
		}),
		facility: newMCPClient("facility-layout", cfg.FacilityLayout.Endpoint, func(c mcpclient.Config) ports.FacilityLayoutClient {
			return mcpclient.NewFacilityLayout(c)
		}),
		inv: newMCPClient("inventory-storage", cfg.InventoryStorage.Endpoint, func(c mcpclient.Config) ports.InventoryStorageClient {
			return mcpclient.NewInventoryStorage(c)
		}),
		lp: newMCPClient("labor-performance", cfg.LaborPerformance.Endpoint, func(c mcpclient.Config) ports.LaborPerformanceClient {
			return mcpclient.NewLaborPerformance(c)
		}),
		telemetry:        newTelemetryReader(cfg.PrometheusURL),
		logs:             newLogReader(cfg.LokiURL),
		planning:         newPlanningClient(cfg),
		productMaster:    newProductMasterClient(cfg),
		inboundReceiving: newInboundReceivingClient(cfg),
		nip:              newNIPClient(cfg),
		slotting:         newSlottingClient(cfg),
	}
}

// decisionSupport bundles the MCP-Customer / decision-support use case
// family: the daily brief (E3), the flow-balance advisory (E1) with its
// ADR-0008 utilization overlay, explain-travel-factor (ADR 0009), the
// E2 stranded-reservation correlation and the ADR 0020 master-data gaps
// (nil when product-master is not configured).
type decisionSupport struct {
	dailyBrief          *usecases.DailyBrief
	flowBalance         *usecases.FlowBalanceAdvisory
	explainTravelFactor *usecases.ExplainTravelFactor
	strandedReservation *usecases.DetectStrandedReservation
	masterDataGaps      *usecases.MasterDataGaps
	// inboundOutlook is nil when inbound-receiving is not configured
	// (ADR 0021).
	inboundOutlook *usecases.InboundOutlook
	// transferWatch is nil when network-inventory-planning is not
	// configured (ADR 0019).
	transferWatch *usecases.TransferWatch
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

	// Slot-plan watch (ADR 0024): only wired when slotting-optimization is
	// configured. A nil watch leaves the daily brief byte-for-byte as it
	// was; a configured one is fail-open per plan.
	if clients.slotting != nil {
		dailyBrief.SlotPlanWatch = &usecases.SlotPlanWatch{
			Slotting:   clients.slotting,
			StaleAfter: cfg.SlotPlanStaleAge,
		}
	}

	// Promise health (ADR 0024): only wired when order-management is
	// configured. A nil section use case leaves the daily brief
	// byte-for-byte as it was; a configured one is fail-open.
	if clients.om != nil {
		dailyBrief.PromiseHealth = &usecases.PromiseHealth{OrderMgmt: clients.om}
	}

	// StrandedReservation is the E2 correlation use case: read-only,
	// consuming the already-wired fulfillment-execution and
	// inventory-storage clients above. It only ever recommends a
	// revoke_reservation; it never calls one.
	strandedReservation := &usecases.DetectStrandedReservation{
		FulfillmentExecution: clients.fe,
		InventoryStorage:     clients.inv,
	}

	// Master-data gaps (ADR 0020): only wired when product-master is
	// configured; otherwise GET /master-data-gaps answers 503 and
	// find_master_data_gaps is not registered.
	var masterDataGaps *usecases.MasterDataGaps
	if clients.productMaster != nil {
		masterDataGaps = &usecases.MasterDataGaps{ProductMaster: clients.productMaster}
	}

	return decisionSupport{
		dailyBrief:          dailyBrief,
		flowBalance:         flowBalance,
		explainTravelFactor: newExplainTravelFactor(clients),
		strandedReservation: strandedReservation,
		masterDataGaps:      masterDataGaps,
		inboundOutlook:      newInboundOutlook(cfg, clients),
		transferWatch:       newTransferWatch(clients),
	}
}

// newExplainTravelFactor wires the ADR-0009 use case, or returns nil when
// facility-layout is not configured: GET /explain-travel-factor then
// answers 503 "not configured" and explain_travel_factor is not
// registered, the same as every other upstream-specific feature.
func newExplainTravelFactor(clients outboundClients) *usecases.ExplainTravelFactor {
	if clients.facility == nil {
		return nil
	}
	return &usecases.ExplainTravelFactor{Facility: clients.facility}
}

// newInboundOutlook wires the ADR-0021 read-only inbound outlook, or returns
// nil when inbound-receiving is not configured. The nil must be a real nil
// *InboundOutlook (never a non-nil struct around a nil port) so the inbound
// adapters' "not configured" checks are genuine.
func newInboundOutlook(cfg config.Config, clients outboundClients) *usecases.InboundOutlook {
	if clients.inboundReceiving == nil {
		return nil
	}
	return &usecases.InboundOutlook{
		Inbound:         clients.inboundReceiving,
		StaleReceiptAge: cfg.InboundStaleReceiptAge,
	}
}

// newTransferWatch wires the ADR-0019 read-only transfer watch, or returns
// nil when network-inventory-planning is not configured. The nil must be a
// real nil *TransferWatch (never a non-nil struct around a nil port) so the
// inbound adapters' "not configured" checks are genuine.
func newTransferWatch(clients outboundClients) *usecases.TransferWatch {
	if clients.nip == nil {
		return nil
	}
	return &usecases.TransferWatch{NIP: clients.nip}
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
			"facility_layout_endpoint_configured", cfg.FacilityLayout.Endpoint != "",
			"warehouse_planning_endpoint_configured", cfg.WarehousePlanning.Endpoint != "",
			"product_master_endpoint_configured", cfg.ProductMaster.Endpoint != "",
			"inbound_receiving_endpoint_configured", cfg.InboundReceiving.Endpoint != "",
			"network_inventory_planning_endpoint_configured", cfg.NetworkInventoryPlanning.Endpoint != "",
			"slotting_optimization_endpoint_configured", cfg.SlottingOptimization.Endpoint != "",
			"order_management_endpoint_configured", cfg.OrderManagement.Endpoint != "",
			"labor_performance_endpoint_configured", cfg.LaborPerformance.Endpoint != "",
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
