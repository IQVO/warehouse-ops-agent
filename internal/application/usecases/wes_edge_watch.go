// Package usecases: WesEdgeWatch (ADR 0034) -- the daily brief's WES-edge
// watch: pick-face-replenishment's open replenishment requests (counted by
// priority, with the oldest pending age) and task-interleaving's interleaving
// hint lifecycle counts.
package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

const (
	// wesEdgePageSize is the list page size both WMS-edge contexts share:
	// each one's own maximum, so a scan needs as few calls as possible.
	wesEdgePageSize = 500
	// wesEdgeMaxPages bounds each scan to 10 pages (5,000 records). Like
	// the sibling outlooks' page bound it is a per-request work bound,
	// not a business input: when more records remain the counts are a
	// lower bound and the section says so.
	wesEdgeMaxPages = 10
	// replenishmentOpenState is pick-face-replenishment's own state literal
	// for a request waiting to be moved from reserve to the forward pick
	// face.
	replenishmentOpenState = "Requested"
)

// WesEdgeWatch is the ADR 0034 read-only use case over
// pick-face-replenishment's list_replenishments and task-interleaving's
// list_hints tools. It invents nothing: priorities, states and instants are
// the upstreams' published facts and "now" comes from the clock; there is no
// operator input and no threshold to configure.
//
// A nil *WesEdgeWatch, or one whose two ports are both nil, is "not
// configured" (both *_MCP_ENDPOINT unset, ADR 0023): Execute returns nil and
// the daily brief omits the section entirely. Each half is independently
// fail-open: a nil pick-face-replenishment port omits the replenishment half
// and a nil task-interleaving port omits the interleaving half. A configured
// half NEVER fails the brief: an unreachable upstream, a failed scan, a
// contract-breaking published fact or the page bound degrades to explicit
// unavailable lines on that half, never an error and never a silent
// "nothing to report".
type WesEdgeWatch struct {
	// PFR is the pick-face-replenishment port; nil means the
	// replenishment half is not wired.
	PFR ports.PickFaceReplenishmentClient
	// Interleaving is the task-interleaving port; nil means the
	// interleaving half is not wired.
	Interleaving ports.TaskInterleavingClient
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Execute builds the WES-edge watch section, or returns nil when neither
// upstream is configured (see the type's doc comment).
func (uc *WesEdgeWatch) Execute(ctx context.Context, now time.Time) *policy.WesEdgeWatchSection {
	if uc == nil || (uc.PFR == nil && uc.Interleaving == nil) {
		return nil
	}
	section := &policy.WesEdgeWatchSection{}
	if uc.PFR != nil {
		section.Replenishment = uc.replenishment(ctx, now)
	}
	if uc.Interleaving != nil {
		section.Interleaving = uc.interleaving(ctx)
	}
	return section
}

// replenishment scans the open (Requested) replenishment requests and
// summarizes them: counted by priority, the oldest pending one first.
func (uc *WesEdgeWatch) replenishment(ctx context.Context, now time.Time) *policy.ReplenishmentWatchSide {
	facts, problems, complete, err := uc.scanOpenReplenishments(ctx)
	if err != nil {
		return &policy.ReplenishmentWatchSide{
			Open:        []policy.OpenReplenishmentCount{},
			Unavailable: []string{"pick-face-replenishment " + err.Error()},
		}
	}
	if !complete {
		problems = append(problems, fmt.Sprintf(
			"pick-face-replenishment list_replenishments: page bound of %d pages reached with more Requested replenishments pending; the counts are a lower bound", wesEdgeMaxPages))
	}
	side := policy.SummarizeOpenReplenishments(facts, now)
	side.Unavailable = problems
	return &side
}

// scanOpenReplenishments pages list_replenishments filtered to the Requested
// state. problems carries the per-request degradation lines (a request whose
// requested_at broke the contract is dropped, never defaulted); a list error
// fails the whole half, like the sibling outlooks' list calls. complete is
// false when the page bound was reached with a cursor still pending.
func (uc *WesEdgeWatch) scanOpenReplenishments(ctx context.Context) ([]policy.ReplenishmentRequestFact, []string, bool, error) {
	var facts []policy.ReplenishmentRequestFact
	var problems []string
	cursor := ""
	for page := 0; page < wesEdgeMaxPages; page++ {
		result, err := uc.PFR.ListReplenishments(ctx, ports.ReplenishmentListQuery{
			State:  replenishmentOpenState,
			Cursor: cursor,
			Limit:  wesEdgePageSize,
		})
		if err != nil {
			return nil, nil, false, fmt.Errorf("list_replenishments (%s): %w", replenishmentOpenState, err)
		}
		for _, r := range result.Items {
			fact, problem := openReplenishmentFact(r)
			if problem != "" {
				problems = append(problems, problem)
				continue
			}
			facts = append(facts, fact)
		}
		if result.NextCursor == "" {
			return facts, problems, true, nil
		}
		cursor = result.NextCursor
	}
	return facts, problems, false, nil
}

// openReplenishmentFact builds the policy fact for one Requested
// replenishment. A Requested request without a parseable requested_at
// breaks the contract (there is no "pending since" without it): the request
// comes back as the problem line naming it, never as a fact defaulted to the
// epoch.
func openReplenishmentFact(r ports.ReplenishmentRequest) (policy.ReplenishmentRequestFact, string) {
	if r.RequestedAt == "" {
		return policy.ReplenishmentRequestFact{}, fmt.Sprintf("pick-face-replenishment published no requested_at of request %s", r.RequestID)
	}
	requestedAt, err := time.Parse(time.RFC3339, r.RequestedAt)
	if err != nil {
		return policy.ReplenishmentRequestFact{}, fmt.Sprintf("pick-face-replenishment published an unparseable requested_at of request %s %q: %v", r.RequestID, r.RequestedAt, err)
	}
	return policy.ReplenishmentRequestFact{
		RequestID:   r.RequestID,
		SiteID:      r.SiteID,
		SKU:         r.SKU,
		Priority:    r.Priority,
		RequestedAt: requestedAt,
	}, ""
}

// interleaving counts the hints in each lifecycle state: one bounded scan
// per state, so each count is that state's own complete count (a single
// unbounded scan, newest first, could not promise that). A state whose scan
// failed is absent from Counts and named on Unavailable — never counted as
// zero.
func (uc *WesEdgeWatch) interleaving(ctx context.Context) *policy.InterleavingWatchSide {
	side := &policy.InterleavingWatchSide{Counts: []policy.InterleavingHintCount{}}
	for _, state := range policy.HintLifecycleStates {
		count, complete, err := uc.countHints(ctx, state)
		if err != nil {
			side.Unavailable = append(side.Unavailable, fmt.Sprintf("task-interleaving list_hints (%s): %v", state, err))
			continue
		}
		if !complete {
			side.Unavailable = append(side.Unavailable, fmt.Sprintf(
				"task-interleaving list_hints (%s): page bound of %d pages reached; the count is a lower bound", state, wesEdgeMaxPages))
		}
		side.Counts = append(side.Counts, policy.InterleavingHintCount{State: state, Count: count})
	}
	return side
}

// countHints pages list_hints filtered to one state, counting the items.
// complete is false when the page bound was reached with a cursor still
// pending.
func (uc *WesEdgeWatch) countHints(ctx context.Context, state string) (int, bool, error) {
	count := 0
	cursor := ""
	for page := 0; page < wesEdgeMaxPages; page++ {
		result, err := uc.Interleaving.ListHints(ctx, ports.HintListQuery{
			State:  state,
			Cursor: cursor,
			Limit:  wesEdgePageSize,
		})
		if err != nil {
			return 0, false, err
		}
		count += len(result.Items)
		if result.NextCursor == "" {
			return count, true, nil
		}
		cursor = result.NextCursor
	}
	return count, false, nil
}
