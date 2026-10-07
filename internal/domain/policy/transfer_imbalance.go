package policy

import (
	"fmt"
	"sort"
	"strings"
)

// SiteBalance classifies one site's capacity against its demand over the
// plan window, using NIP's own capacity_headroom (capacity minus demand,
// negative = short). No threshold is invented: short means headroom < 0.
type SiteBalance string

const (
	SiteShort   SiteBalance = "short"
	SiteCovered SiteBalance = "covered"
)

// SiteSimulationSignal is the pure-domain shape of one site of NIP's
// simulate_transfer_options output.
type SiteSimulationSignal struct {
	Site               string
	OriginEnabled      bool
	DestinationEnabled bool
	TotalDemand        int
	CapacityOverWindow float64
	CapacityHeadroom   int
	WindowStart        string
	WindowEnd          string
}

// SiteImbalance is the explained reading of one site.
type SiteImbalance struct {
	SiteSimulationSignal
	Balance     SiteBalance
	Explanation string
}

// NetworkImbalance is the advisory explanation of NIP's simulation. It never
// proposes a transfer quantity or route: it names which sites are short,
// which have headroom and may originate, and what an operator can check
// next in network-inventory-planning.
type NetworkImbalance struct {
	// Advisory echoes NIP's own flag (the simulation reserves and moves
	// nothing).
	Advisory bool
	AsOf     string
	// Imbalanced is true when at least one site is short.
	Imbalanced bool
	// Sites is ordered: short sites first (most short first), then covered
	// sites (most headroom first); ties by site id.
	Sites     []SiteImbalance
	Summary   string
	NextCheck string
}

// ExplainImbalance explains NIP's simulation facts. Pure function.
func ExplainImbalance(advisory bool, asOf string, sites []SiteSimulationSignal) NetworkImbalance {
	out := NetworkImbalance{Advisory: advisory, AsOf: asOf, Sites: make([]SiteImbalance, 0, len(sites))}

	for _, s := range sites {
		si := SiteImbalance{SiteSimulationSignal: s, Balance: SiteCovered}
		if s.CapacityHeadroom < 0 {
			si.Balance = SiteShort
		}
		si.Explanation = explainSite(s, si.Balance)
		out.Sites = append(out.Sites, si)
	}
	sort.SliceStable(out.Sites, func(i, j int) bool { return siteBefore(out.Sites[i], out.Sites[j]) })
	short, donors, stuckShort := partitionSites(out.Sites)
	out.Imbalanced = len(short) > 0

	// if-chains rather than a tagless switch: the repo's mutation gate
	// (gremlins) cannot attribute coverage to `case` conditions, so a switch
	// here would read as "not covered" although the tests exercise every arm.
	if len(sites) == 0 {
		out.Summary = "the simulation returned no sites; there is nothing to explain"
		out.NextCheck = "Check that sites are enabled and their capacity and demand facts are published in network-inventory-planning."
		return out
	}
	if !out.Imbalanced {
		out.Summary = fmt.Sprintf("no imbalance: every one of the %d site(s) has capacity that covers its demand over the plan window", len(sites))
		out.NextCheck = "No action to consider; re-read the simulation when demand or the published plan changes."
		return out
	}
	out.Summary = imbalanceSummary(short, donors)
	out.NextCheck = imbalanceNextCheck(short, donors, stuckShort)
	return out
}

// siteBefore orders short sites first (most short first), then covered sites
// (most headroom first); ties break by site id so the output is deterministic.
func siteBefore(a, b SiteImbalance) bool {
	if (a.Balance == SiteShort) != (b.Balance == SiteShort) {
		return a.Balance == SiteShort
	}
	if a.CapacityHeadroom != b.CapacityHeadroom {
		if a.Balance == SiteShort {
			return a.CapacityHeadroom < b.CapacityHeadroom
		}
		return a.CapacityHeadroom > b.CapacityHeadroom
	}
	return a.Site < b.Site
}

// partitionSites splits the ordered sites into the short ones, the donors
// (covered, strictly positive headroom, enabled to originate) and the short
// ones that are also not enabled to receive. Zero headroom is covered but has
// nothing to give, so it is never a donor.
func partitionSites(sites []SiteImbalance) (short, donors, stuckShort []SiteImbalance) {
	for _, si := range sites {
		if si.Balance == SiteShort {
			short = append(short, si)
			if !si.DestinationEnabled {
				stuckShort = append(stuckShort, si)
			}
			continue
		}
		if si.OriginEnabled && si.CapacityHeadroom > 0 {
			donors = append(donors, si)
		}
	}
	return short, donors, stuckShort
}

func explainSite(s SiteSimulationSignal, b SiteBalance) string {
	gap := fmt.Sprintf("demand %d against capacity %.1f over the window %s to %s", s.TotalDemand, s.CapacityOverWindow, s.WindowStart, s.WindowEnd)
	if b == SiteShort {
		why := fmt.Sprintf("short by %d: %s", -s.CapacityHeadroom, gap)
		if !s.DestinationEnabled {
			why += "; it is not enabled to receive transfers"
		}
		return why
	}
	why := fmt.Sprintf("covered with headroom %d: %s", s.CapacityHeadroom, gap)
	if s.CapacityHeadroom <= 0 {
		return why
	}
	if s.OriginEnabled {
		return why + "; it is enabled to originate transfers"
	}
	return why + "; it is not enabled to originate transfers"
}

func siteList(sites []SiteImbalance) string {
	parts := make([]string, 0, len(sites))
	for _, s := range sites {
		parts = append(parts, fmt.Sprintf("%s (%+d)", s.Site, s.CapacityHeadroom))
	}
	return strings.Join(parts, ", ")
}

func imbalanceSummary(short, donors []SiteImbalance) string {
	msg := fmt.Sprintf("%d site(s) short over the plan window: %s", len(short), siteList(short))
	if len(donors) == 0 {
		return msg + "; no site with headroom is enabled to originate transfers"
	}
	return msg + fmt.Sprintf("; site(s) with headroom that are enabled to originate: %s", siteList(donors))
}

func imbalanceNextCheck(short, donors, stuckShort []SiteImbalance) string {
	if len(donors) == 0 {
		return "Check whether a site with headroom can be enabled to originate in network-inventory-planning, or whether the plan's capacity or demand facts for the short site(s) are wrong; " +
			"this agent proposes no transfer."
	}
	if len(stuckShort) > 0 {
		return fmt.Sprintf("Check why %s cannot receive transfers (destination disabled) before considering any transfer toward it; the decision to propose or approve one is the operator's in network-inventory-planning.", siteList(stuckShort))
	}
	return fmt.Sprintf("Review with the planner whether a transfer from %s toward %s is warranted; "+
		"proposing and approving transfers is the operator's decision in network-inventory-planning, and this agent proposes no quantity.", siteList(donors), siteList(short))
}
