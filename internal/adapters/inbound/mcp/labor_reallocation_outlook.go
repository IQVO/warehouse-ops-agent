package mcp

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// --- labor_reallocation_outlook (ADR 0029, authority L2) --------------------
//
// The READ side of act slice #2: a ranked list of at most three moves, each
// with reason codes, evidence and the next read-only check. It carries NO
// confirmation token and has no write path: the propose/approve routes are
// REST-only (a human act), and this server holds only the token-less
// usecases.LaborReallocationOutlook.

type reallocationCandidateInput struct {
	AssociateId string `json:"associateId" jsonschema:"an associate the human offers for a move"`
	FromPathId  string `json:"fromPathId,omitempty" jsonschema:"the process path the associate currently works on, when known; omit if unknown"`
}

type laborReallocationOutlookInput struct {
	SiteCode   string                       `json:"siteCode" jsonschema:"the canonical site code (the facility-layout Site code, e.g. WH1) whose shift plan to read"`
	ShiftId    string                       `json:"shiftId" jsonschema:"the shift whose committed plan to read staffing from"`
	ToPathId   string                       `json:"toPathId" jsonschema:"the understaffed process path the moves would cover (e.g. pack, pick, stow)"`
	Candidates []reallocationCandidateInput `json:"candidates" jsonschema:"1 to 20 associates a human offers for the move; workforce-management exposes no associate list, so this tool never invents one"`
}

type pathStaffingOutput struct {
	PathId       string `json:"pathId"`
	PlannedHeads int    `json:"plannedHeads"`
	ActiveHeads  int    `json:"activeHeads"`
	Understaffed bool   `json:"understaffed"`
}

type reallocationEvidenceOutput struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

type reallocationMoveOutput struct {
	Rank        int                          `json:"rank"`
	AssociateId string                       `json:"associateId"`
	FromPathId  string                       `json:"fromPathId,omitempty"`
	ToPathId    string                       `json:"toPathId"`
	ReasonCodes []string                     `json:"reasonCodes"`
	Evidence    []reallocationEvidenceOutput `json:"evidence"`
	NextCheck   string                       `json:"nextCheck"`
}

type skippedCandidateOutput struct {
	AssociateId string `json:"associateId"`
	Reason      string `json:"reason"`
}

type laborReallocationOutlookOutput struct {
	SiteCode string                   `json:"siteCode"`
	ShiftId  string                   `json:"shiftId"`
	Target   pathStaffingOutput       `json:"target"`
	Moves    []reallocationMoveOutput `json:"moves"`
	Skipped  []skippedCandidateOutput `json:"skipped"`
	// Advisory restates the boundary of this tool for a model reading it.
	Advisory string `json:"advisory"`
}

const laborReallocationAdvisory = "Recommendation only: this tool has no write path and issues no confirmation token. Placing an associate is a separate, human-approved REST step; run each move's nextCheck first."

func (d Deps) laborReallocationOutlook(ctx context.Context, in laborReallocationOutlookInput) (laborReallocationOutlookOutput, error) {
	req := usecases.LaborReallocationInput{SiteCode: in.SiteCode, ShiftId: in.ShiftId, ToPathId: in.ToPathId}
	for _, c := range in.Candidates {
		req.Candidates = append(req.Candidates, policy.ReallocationCandidate{AssociateId: c.AssociateId, FromPathId: c.FromPathId})
	}
	o, err := d.LaborReallocationOutlook.Execute(ctx, req)
	if err != nil {
		return laborReallocationOutlookOutput{}, err
	}
	out := laborReallocationOutlookOutput{
		SiteCode: o.SiteCode,
		ShiftId:  o.ShiftId,
		Target:   pathStaffingOutput{PathId: o.Target.PathId, PlannedHeads: o.Target.PlannedHeads, ActiveHeads: o.Target.ActiveHeads, Understaffed: o.Target.Understaffed},
		Moves:    make([]reallocationMoveOutput, 0, len(o.Result.Moves)),
		Skipped:  make([]skippedCandidateOutput, 0, len(o.Result.Skipped)),
		Advisory: laborReallocationAdvisory,
	}
	for _, m := range o.Result.Moves {
		ev := make([]reallocationEvidenceOutput, 0, len(m.Evidence))
		for _, e := range m.Evidence {
			ev = append(ev, reallocationEvidenceOutput{Tool: e.Tool, Summary: e.Summary})
		}
		out.Moves = append(out.Moves, reallocationMoveOutput{
			Rank: m.Rank, AssociateId: m.AssociateId, FromPathId: m.FromPathId, ToPathId: m.ToPathId,
			ReasonCodes: m.ReasonCodes, Evidence: ev, NextCheck: m.NextCheck,
		})
	}
	for _, s := range o.Result.Skipped {
		out.Skipped = append(out.Skipped, skippedCandidateOutput{AssociateId: s.AssociateId, Reason: s.Reason})
	}
	return out, nil
}
