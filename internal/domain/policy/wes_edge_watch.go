// Package policy: WES-edge watch rules (ADR 0034's section).
//
// Pure, side-effect-free summary of the two WMS-edge contexts' published
// facts: pick-face-replenishment's open replenishment requests grouped by
// priority with the oldest pending age, and task-interleaving's interleaving
// hint lifecycle counts. The facts are gathered one layer out
// (internal/application/usecases); this file only judges them.
package policy

import "time"

// ReplenishmentRequestFact is one open (Requested) replenishment request as
// the watch judges it: its identity, site, SKU, priority and the instant it
// was requested.
type ReplenishmentRequestFact struct {
	RequestID   string
	SiteID      string
	SKU         string
	Priority    string
	RequestedAt time.Time
}

// OpenReplenishmentCount is the number of open requests at one priority.
type OpenReplenishmentCount struct {
	Priority string
	Count    int
}

// OldestReplenishment is the open request pending longest: the one the
// operator should look at first.
type OldestReplenishment struct {
	RequestID   string
	SiteID      string
	SKU         string
	Priority    string
	RequestedAt time.Time
	Age         time.Duration
}

// ReplenishmentWatchSide is the pick-face-replenishment half of the WES-edge
// section: every open request counted by priority, plus the oldest pending
// one. Open is empty-but-present when the scan ran and found nothing; a
// request whose published facts broke the contract is dropped (named on
// Unavailable, never defaulted). Unavailable carries the half's explicit
// degradation lines (an unreachable upstream, a failed scan, a broken
// request or the page bound) and MUST be read as "this half is incomplete",
// not "nothing to report".
type ReplenishmentWatchSide struct {
	Open        []OpenReplenishmentCount
	Total       int
	Oldest      *OldestReplenishment
	Unavailable []string
}

// The hint lifecycle states task-interleaving publishes, in lifecycle order.
const (
	HintActive     = "Active"
	HintConsumed   = "Consumed"
	HintExpired    = "Expired"
	HintSuperseded = "Superseded"
)

// HintLifecycleStates is the fixed order the watch counts and reports the
// four states in: the live state first, then each terminal one.
var HintLifecycleStates = []string{HintActive, HintConsumed, HintExpired, HintSuperseded}

// InterleavingHintCount is the number of hints in one lifecycle state. A
// zero count is a fact ("no active hints"), not a missing reading; a state
// whose scan failed is simply absent from Counts and named on Unavailable.
type InterleavingHintCount struct {
	State string
	Count int
}

// InterleavingWatchSide is the task-interleaving half of the WES-edge
// section: the hint counts by lifecycle state. Unavailable carries the
// half's explicit degradation lines (an unreachable upstream, a failed scan
// or a state's page bound) and MUST be read as "incomplete", never as
// "nothing to report".
type InterleavingWatchSide struct {
	Counts      []InterleavingHintCount
	Unavailable []string
}

// WesEdgeWatchSection is the daily brief's WES-edge section (ADR 0034): the
// replenishment and interleaving halves. Each half is nil when its upstream
// is not configured (the DTOs then omit it entirely); a non-nil half with
// Unavailable lines means it was configured but could not be fully read.
type WesEdgeWatchSection struct {
	Replenishment *ReplenishmentWatchSide
	Interleaving  *InterleavingWatchSide
}

// SummarizeOpenReplenishments groups the open requests by priority and finds
// the one pending longest. Priorities come back largest count first, ties by
// priority name: pick-face-replenishment publishes priority as a free string
// (no enum in its registry), so the summary invents no urgency ranking — it
// only orders the groups deterministically. A fact whose RequestedAt is the
// zero time (a Requested request published without requested_at) is never
// summarized — the use case drops it with an explicit unavailable line
// before building the fact.
func SummarizeOpenReplenishments(facts []ReplenishmentRequestFact, now time.Time) ReplenishmentWatchSide {
	side := ReplenishmentWatchSide{Open: []OpenReplenishmentCount{}}
	counts := map[string]int{}
	for _, f := range facts {
		if f.RequestedAt.IsZero() {
			continue
		}
		counts[f.Priority]++
		side.Total++
		if side.Oldest == nil || f.RequestedAt.Before(side.Oldest.RequestedAt) {
			side.Oldest = &OldestReplenishment{
				RequestID:   f.RequestID,
				SiteID:      f.SiteID,
				SKU:         f.SKU,
				Priority:    f.Priority,
				RequestedAt: f.RequestedAt,
				Age:         now.Sub(f.RequestedAt),
			}
		}
	}
	for priority, count := range counts {
		side.Open = append(side.Open, OpenReplenishmentCount{Priority: priority, Count: count})
	}
	sortOpenReplenishmentCounts(side.Open)
	return side
}

// sortOpenReplenishmentCounts orders the priority groups largest count
// first, ties by priority name, so the summary reads deterministically
// without inventing an urgency ranking.
func sortOpenReplenishmentCounts(counts []OpenReplenishmentCount) {
	for i := 1; i < len(counts); i++ {
		j := i
		for j > 0 && openReplenishmentCountBefore(counts[j], counts[j-1]) {
			counts[j-1], counts[j] = counts[j], counts[j-1]
			j--
		}
	}
}

// openReplenishmentCountBefore says whether a sorts before b: larger count
// first, ties broken by priority name.
func openReplenishmentCountBefore(a, b OpenReplenishmentCount) bool {
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.Priority < b.Priority
}
