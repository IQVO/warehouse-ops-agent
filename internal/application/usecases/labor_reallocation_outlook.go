package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The labor-reallocation READ side (ADR 0029, act slice #2).
//
// LaborReallocationOutlook reads workforce-management's staffing gap for a
// target path and for the current paths of the candidates a human offers,
// and ranks at most three moves with the pure policy. It holds NO token
// signer and NO write port: this is the only part of the slice the MCP
// server (labor_reallocation_outlook, authority L2) may hold. The act side
// (LaborReallocation) wraps it and is REST-only.

// Input bounds. They protect the upstream from an unbounded fan-out of
// get_staffing_gap reads; they are not staffing thresholds.
const (
	// MaxReallocationCandidates bounds the associates offered per call.
	MaxReallocationCandidates = 20
	// maxReallocationIDRunes bounds every caller-supplied identifier.
	maxReallocationIDRunes = 128
)

// Sentinels the REST adapter maps onto statuses.
var (
	// ErrNotUnderstaffed: workforce-management does not report the target
	// path short of its planned heads, so there is nothing to propose.
	ErrNotUnderstaffed = errors.New("target path is not understaffed")
	// ErrNoReallocationMove: the target is short but none of the offered
	// associates can move without opening a gap elsewhere.
	ErrNoReallocationMove = errors.New("no offered associate can be moved to the target path")
)

// LaborReallocationInput is the caller's question: which of these associates
// could cover the gap on ToPathId at this site and shift?
type LaborReallocationInput struct {
	SiteCode   string
	ShiftId    string
	ToPathId   string
	Candidates []policy.ReallocationCandidate
}

// ReallocationOutlook is the ranked answer plus the reading it rests on.
// Result.Moves may be empty; that is an answer, not an error.
type ReallocationOutlook struct {
	SiteCode string
	ShiftId  string
	Target   policy.PathStaffing
	Result   policy.ReallocationResult
}

// ReallocationOutlookReader is the slice of LaborReallocationOutlook the act
// use case needs.
type ReallocationOutlookReader interface {
	Execute(ctx context.Context, in LaborReallocationInput) (ReallocationOutlook, error)
}

// LaborReallocationOutlook is the read-only reallocation view.
type LaborReallocationOutlook struct {
	staffing ports.StaffingGapBySiteClient
}

// NewLaborReallocationOutlook builds the read side; the staffing port is
// required.
func NewLaborReallocationOutlook(staffing ports.StaffingGapBySiteClient) (*LaborReallocationOutlook, error) {
	if staffing == nil {
		return nil, errors.New("labor reallocation outlook: the workforce-management staffing port is required")
	}
	return &LaborReallocationOutlook{staffing: staffing}, nil
}

// Execute validates the input, reads the gaps and applies the policy. The
// target reading is mandatory: if it cannot be read the call fails (an
// upstream failure, or ErrInvalidInput when workforce-management rejects the
// input). An origin path that cannot be read only screens out the associates
// standing on it, and says so.
func (uc *LaborReallocationOutlook) Execute(ctx context.Context, in LaborReallocationInput) (ReallocationOutlook, error) {
	if err := validateReallocationInput(in); err != nil {
		return ReallocationOutlook{}, err
	}
	raw, err := uc.staffing.GetStaffingGapForSite(ctx, in.SiteCode, in.ShiftId, in.ToPathId)
	if err != nil {
		if errors.Is(err, ports.ErrUpstreamInvalidInput) {
			return ReallocationOutlook{}, fmt.Errorf("%w: labor_reallocation: %w", ErrInvalidInput, err)
		}
		return ReallocationOutlook{}, fmt.Errorf("labor_reallocation: reading the staffing gap of %q: %w", in.ToPathId, err)
	}
	target := toPathStaffing(in.ToPathId, raw)

	origins := map[string]policy.PathStaffing{}
	for _, c := range in.Candidates {
		from := c.FromPathId
		if from == "" || from == in.ToPathId {
			continue
		}
		if _, done := origins[from]; done {
			continue
		}
		if g, err := uc.staffing.GetStaffingGapForSite(ctx, in.SiteCode, in.ShiftId, from); err == nil {
			origins[from] = toPathStaffing(from, g)
		}
	}
	return ReallocationOutlook{
		SiteCode: in.SiteCode,
		ShiftId:  in.ShiftId,
		Target:   target,
		Result:   policy.ProposeLaborReallocation(target, origins, in.Candidates, policy.ReallocationScope{SiteCode: in.SiteCode, ShiftId: in.ShiftId}),
	}, nil
}

// toPathStaffing keeps the path id the caller asked about; the readings are
// the upstream's, unmodified.
func toPathStaffing(pathId string, g ports.StaffingGap) policy.PathStaffing {
	return policy.PathStaffing{PathId: pathId, PlannedHeads: g.PlannedHeads, ActiveHeads: g.ActiveHeads, Understaffed: g.Understaffed}
}

func validateReallocationInput(in LaborReallocationInput) error {
	for _, f := range []struct{ name, v string }{{"siteCode", in.SiteCode}, {"shiftId", in.ShiftId}, {"toPathId", in.ToPathId}} {
		if err := validateReallocationID(f.name, f.v, true); err != nil {
			return err
		}
	}
	if n := len(in.Candidates); n == 0 || n > MaxReallocationCandidates {
		return fmt.Errorf("%w: candidates must hold between 1 and %d associates, got %d", ErrInvalidInput, MaxReallocationCandidates, n)
	}
	for i, c := range in.Candidates {
		if err := validateReallocationID(fmt.Sprintf("candidates[%d].associateId", i), c.AssociateId, true); err != nil {
			return err
		}
		if err := validateReallocationID(fmt.Sprintf("candidates[%d].fromPathId", i), c.FromPathId, false); err != nil {
			return err
		}
	}
	return nil
}

// validateReallocationID rejects blank (when required), over-long and
// control-character values: they end up in logs, spans and an event.
func validateReallocationID(name, v string, required bool) error {
	if strings.TrimSpace(v) == "" {
		if required {
			return fmt.Errorf("%w: %s is required", ErrInvalidInput, name)
		}
		if v != "" {
			return fmt.Errorf("%w: %s must not be blank", ErrInvalidInput, name)
		}
		return nil
	}
	if v != strings.TrimSpace(v) {
		return fmt.Errorf("%w: %s must not carry leading or trailing whitespace", ErrInvalidInput, name)
	}
	if utf8.RuneCountInString(v) > maxReallocationIDRunes {
		return fmt.Errorf("%w: %s is longer than %d characters", ErrInvalidInput, name, maxReallocationIDRunes)
	}
	if strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s must not contain control characters", ErrInvalidInput, name)
	}
	return nil
}
