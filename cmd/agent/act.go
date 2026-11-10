package main

import (
	"fmt"
	"log/slog"
	"time"

	outbounda2a "github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/a2a"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/memory"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// newReleaseWork wires the ADR-0027 act slice: the human-confirmed
// release of the next work unit. It is opt-in — with
// WES_WORK_PLANNING_A2A_URL unset it returns (nil, nil), no write adapter is
// built, and the /act routes answer 503. The result is handed to the REST
// adapter ONLY; main must never pass it to the MCP server or the reasoner
// (internal/architecture/zerowrite asserts that statically).
func newReleaseWork(cfg config.Config, logger *slog.Logger, advisory *usecases.FlowBalanceAdvisory) (*usecases.ReleaseWork, error) {
	if cfg.Act.WorkPlanningA2AURL == "" {
		logger.Info("act slice disabled (WES_WORK_PLANNING_A2A_URL unset): this agent stays read-only")
		return nil, nil
	}
	if advisory == nil {
		return nil, fmt.Errorf("act slice: the flow-balance advisory is required to propose a release")
	}

	signer, err := newSigner(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("act slice: %w", err)
	}

	uc, err := usecases.NewReleaseWork(advisory, signer, outbounda2a.NewClient(cfg.Act.WorkPlanningA2AURL, nil), logger, nil, exceptionContextOption(cfg, logger))
	if err != nil {
		return nil, fmt.Errorf("act slice: %w", err)
	}
	logger.Warn("act slice ENABLED: POST /act/release-next-work/{proposals,approvals} can release work through the wes-work-planning agent after explicit confirmation",
		"agent", cfg.Act.WorkPlanningA2AURL, "tokenTTL", cfg.Act.TokenTTL)
	return uc, nil
}

// newLaborReallocation wires the ADR-0029 labor-reallocation slice and
// returns its two halves:
//
//   - the token-less READ outlook, built whenever the workforce-management
//     MCP endpoint is configured; it is the ONLY half the MCP server may
//     hold (labor_reallocation_outlook, authority L2);
//   - the human-gated ACT use case, built only when
//     WORKFORCE_MANAGEMENT_A2A_URL is set. Unset means (nil, nil): no write
//     adapter, both /labor-reallocation routes answer 503. The act half is
//     handed to the REST adapter ONLY, never to the MCP server or the
//     reasoner (internal/architecture/zerowrite asserts that statically).
//
// Setting the A2A URL without the MCP endpoint is a startup error: a
// proposal without its staffing reads would be a guess.
func newLaborReallocation(cfg config.Config, logger *slog.Logger, staffing ports.StaffingGapBySiteClient) (*usecases.LaborReallocationOutlook, *usecases.LaborReallocation, error) {
	if staffing == nil {
		if cfg.WorkforceManagementA2AURL != "" {
			return nil, nil, fmt.Errorf("labor reallocation: WORKFORCE_MANAGEMENT_A2A_URL is set but WORKFORCE_MANAGEMENT_MCP_ENDPOINT is not: the proposals need the staffing reads")
		}
		logger.Info("labor reallocation disabled (WORKFORCE_MANAGEMENT_MCP_ENDPOINT unset): no outlook tool, no proposals")
		return nil, nil, nil
	}
	outlook, err := usecases.NewLaborReallocationOutlook(staffing)
	if err != nil {
		return nil, nil, fmt.Errorf("labor reallocation: %w", err)
	}
	if cfg.WorkforceManagementA2AURL == "" {
		logger.Info("labor reallocation act disabled (WORKFORCE_MANAGEMENT_A2A_URL unset): the outlook tool is read-only")
		return outlook, nil, nil
	}
	signer, err := newSigner(cfg, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("labor reallocation: %w", err)
	}
	uc, err := usecases.NewLaborReallocation(outlook, signer, outbounda2a.NewLaborClient(cfg.WorkforceManagementA2AURL, nil), logger, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("labor reallocation: %w", err)
	}
	logger.Warn("labor reallocation act ENABLED: POST /labor-reallocation/proposals[/{proposalId}/approvals] can place an associate through the workforce-management agent after explicit confirmation",
		"agent", cfg.WorkforceManagementA2AURL, "tokenTTL", cfg.Act.TokenTTL)
	return outlook, uc, nil
}

// newSigner builds the confirmation-token signer from the act config: the
// pinned APPROVAL_SIGNING_KEY when set, else an ephemeral per-process key
// (coherent with the single-replica chart default).
func newSigner(cfg config.Config, logger *slog.Logger) (*approval.Signer, error) {
	if cfg.Act.SigningKey != "" {
		return approval.NewSigner([]byte(cfg.Act.SigningKey), cfg.Act.TokenTTL, nil)
	}
	logger.Warn("APPROVAL_SIGNING_KEY unset: using an ephemeral per-process key; confirmation tokens only verify in this process, so keep replicaCount at 1")
	return approval.NewEphemeralSigner(cfg.Act.TokenTTL, nil)
}

// exceptionContextOption wires the ADR-0028 advisory lookup: with
// EXCEPTION_MANAGEMENT_A2A_URL unset it returns an option that leaves the
// use case's lookup disabled (nil port, an untyped nil interface so the
// use case's nil check holds). The client it builds implements the
// READ-ONLY ExceptionContext port only; it can never release or change
// anything at exception-management.
func exceptionContextOption(cfg config.Config, logger *slog.Logger) usecases.ReleaseWorkOption {
	if cfg.ExceptionManagementA2AURL == "" {
		logger.Info("exception context disabled (EXCEPTION_MANAGEMENT_A2A_URL unset): proposals carry exceptionContext.status=disabled")
		return usecases.WithExceptionContext(nil, 0)
	}
	logger.Info("exception context enabled: proposals consult exception-management over A2A (read-only, advisory)", "agent", cfg.ExceptionManagementA2AURL)
	return usecases.WithExceptionContext(outbounda2a.NewExceptionClient(cfg.ExceptionManagementA2AURL, nil), 0)
}

// Idempotency-Key retention and capacity of the act routes (ADR 0030): the
// fleet guideline's 24 h minimum, and a bound that keeps the store small next
// to the chart's 256Mi limit (an answer is a few KiB; the oldest goes first).
const (
	idempotencyRetention = 24 * time.Hour
	idempotencyMaxKeys   = 2048
)

// newIdempotencyStore builds the process-local Idempotency-Key store every
// act POST route sits behind. Like the confirmation token's used-nonce set
// (ADR 0027) it lives in this one process: the chart keeps replicaCount 1.
func newIdempotencyStore() (*memory.IdempotencyStore, error) {
	return memory.NewIdempotencyStore(idempotencyRetention, idempotencyMaxKeys, time.Now)
}
