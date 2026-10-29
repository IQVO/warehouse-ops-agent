package main

import (
	"fmt"
	"log/slog"

	outbounda2a "github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/a2a"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
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

	var (
		signer *approval.Signer
		err    error
	)
	if cfg.Act.SigningKey != "" {
		signer, err = approval.NewSigner([]byte(cfg.Act.SigningKey), cfg.Act.TokenTTL, nil)
	} else {
		signer, err = approval.NewEphemeralSigner(cfg.Act.TokenTTL, nil)
		logger.Warn("APPROVAL_SIGNING_KEY unset: using an ephemeral per-process key; confirmation tokens only verify in this process, so keep replicaCount at 1")
	}
	if err != nil {
		return nil, fmt.Errorf("act slice: %w", err)
	}

	uc, err := usecases.NewReleaseWork(advisory, signer, outbounda2a.NewClient(cfg.Act.WorkPlanningA2AURL, nil), logger, nil)
	if err != nil {
		return nil, fmt.Errorf("act slice: %w", err)
	}
	logger.Warn("act slice ENABLED: POST /act/release-next-work/{proposals,approvals} can release work through the wes-work-planning agent after explicit confirmation",
		"agent", cfg.Act.WorkPlanningA2AURL, "tokenTTL", cfg.Act.TokenTTL)
	return uc, nil
}
