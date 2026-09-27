package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/llm/anthropic"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/mcpclient"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/telemetry"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// wireReasoner installs the ADR 0004 model-backed Reasoner behind the
// flow-balance use case according to LLM_MODE. It is the ONLY place the
// model's actuator surface is assembled: a ToolInvoker over the same
// mcpclient sessions the deterministic path uses, restricted to
// cfg.LLM.ToolAllowList.
//
// Fail-fast rules (startup errors, never silent degradation):
//   - an unrecognized LLM_MODE;
//   - a non-off mode without ANTHROPIC_API_KEY.
//
// Off mode returns without touching the use case, so the pre-ADR wiring
// is byte-for-byte unchanged.
func wireReasoner(ctx context.Context, cfg config.Config, logger *slog.Logger, fb *usecases.FlowBalanceAdvisory) error {
	mode, err := policy.ParseLLMMode(cfg.LLM.Mode)
	if err != nil {
		return err
	}
	if mode == policy.LLMOff {
		logger.Info("llm reasoner disabled", "mode", string(mode))
		return nil
	}
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("LLM_MODE=%s requires ANTHROPIC_API_KEY", mode)
	}

	// Sessions keyed by the upstream names the allow-list uses. Unset
	// endpoints are dropped by NewToolInvoker.
	sessions := map[string]*mcpclient.Session{
		"wes-work-planning":     mcpclient.New(mcpclient.Config{Name: "wes-work-planning", Endpoint: cfg.WesWorkPlanning.Endpoint}),
		"fulfillment-execution": mcpclient.New(mcpclient.Config{Name: "fulfillment-execution", Endpoint: cfg.FulfillmentExecution.Endpoint}),
		"workforce-management":  mcpclient.New(mcpclient.Config{Name: "workforce-management", Endpoint: cfg.WorkforceManagement.Endpoint}),
		"inventory-storage":     mcpclient.New(mcpclient.Config{Name: "inventory-storage", Endpoint: cfg.InventoryStorage.Endpoint}),
		"facility-layout":       mcpclient.New(mcpclient.Config{Name: "facility-layout", Endpoint: cfg.FacilityLayout.Endpoint}),
	}
	allowed := map[string][]string{}
	for _, entry := range cfg.LLM.ToolAllowList {
		upstream, tool, ok := strings.Cut(entry, "/")
		if !ok || upstream == "" || tool == "" {
			return fmt.Errorf("LLM_TOOL_ALLOWLIST entry %q is not <upstream>/<tool>", entry)
		}
		allowed[upstream] = append(allowed[upstream], tool)
	}
	invoker := mcpclient.NewToolInvoker(sessions, allowed)

	// ADR-0011: the Anthropic Messages API call is itself an external
	// dependency, wrapped in a per-dependency circuit breaker whose state
	// transitions publish the circuit_breaker_state{dependency="anthropic-llm"}
	// gauge via the SAME OTel meter ArbitrationMetrics already uses. A
	// failed registration is logged and the reasoner still gets a nil
	// recorder (resilience.RecordStateChange's documented no-op) rather
	// than blocking startup over an observability nicety.
	breakerMetrics, err := telemetry.NewCircuitBreakerMetrics()
	if err != nil {
		logger.Warn("llm reasoner: circuit breaker metrics unavailable", "error", err.Error())
	}

	reasoner, err := anthropic.New(anthropic.Config{
		APIKey:          cfg.LLM.APIKey,
		Model:           cfg.LLM.Model,
		BaseURL:         cfg.LLM.BaseURL,
		Timeout:         cfg.LLM.Timeout,
		Logger:          logger,
		BreakerRecorder: breakerMetrics,
	}, invoker)
	if err != nil {
		return err
	}

	metrics, err := telemetry.NewArbitrationMetrics()
	if err != nil {
		return fmt.Errorf("llm metrics: %w", err)
	}

	// Discover the allow-listed tools' schemas once, with a bounded wait.
	// A failed discovery is logged, not fatal: the model is offered the
	// subset that answered, and the deterministic path is unaffected.
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	specs, errs := invoker.Specs(discoverCtx)
	for _, e := range errs {
		logger.Warn("llm reasoner: tool discovery failed for an upstream", "error", e.Error())
	}

	fb.Reasoner = reasoner
	fb.LLMMode = mode
	fb.ReasonerTools = specs
	fb.Metrics = metrics

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Upstream+"/"+s.Name)
	}
	logger.Info("llm reasoner configured",
		"mode", string(mode),
		"model", cfg.LLM.Model,
		"timeout", cfg.LLM.Timeout.String(),
		"tools", names,
	)
	return nil
}
