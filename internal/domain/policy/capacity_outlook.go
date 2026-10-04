// Package policy: capacity-outlook correlation rule (ADR 0013).
//
// A capacity outlook is the daily brief's read of warehouse-planning's
// composed process-path capacity over the next N hours, shown NEXT TO the
// path's existing backlog/staffing/queue/stuck facts. It is purely
// informational: it never feeds deriveExceptions, so the open-exception
// output of a brief is identical with or without it.
//
// Everything here is a pure value or function (no ports, no I/O). The
// outlook is fail-open by construction: any reason it cannot be produced is
// carried as CapacityOutlook.OmittedReason on an otherwise-empty outlook, so
// a brief is never failed or reshaped by planning being unreachable, having
// no covering capacity window, or the path not being mapped to planning.
package policy

import "time"

// capacityOutlookOrderUnit is the only unit a path-capacity rate may be
// reported in: warehouse-planning normalizes every path to ORDER per hour.
// Anything else is rejected, never converted.
const capacityOutlookOrderUnit = "ORDER"

// CapacityStepFact is one process step's contribution to a path's capacity,
// mirroring one step_breakdown entry of warehouse-planning's
// get_process_path_capacity. NormalizedRate is ORDER per hour.
type CapacityStepFact struct {
	Step              string
	NormalizedRate    float64
	BindingConstraint string
}

// PathCapacityFact mirrors warehouse-planning's get_process_path_capacity
// result, as consumed by the outlook. NormalizedUnit must be "ORDER".
type PathCapacityFact struct {
	NormalizedRate float64
	NormalizedUnit string
	BottleneckStep string
	Steps          []CapacityStepFact
	Warnings       []string
}

// CapacityOutlook is the capacity section of one PathBrief. Exactly one of
// two shapes is ever produced:
//
//   - OmittedReason == "": the planned capacity of PlanningPathId over
//     [WindowStart, WindowEnd): NormalizedRate (ORDER/hour), the
//     BottleneckStep, the BindingConstraint of that step (LABOR, STATION,
//     ...; empty when planning's breakdown did not name it), every step and
//     planning's own Warnings.
//   - OmittedReason != "": the section could not be produced and says why;
//     all capacity fields are zero values and must not be read as a rate.
type CapacityOutlook struct {
	PlanningPathId    string
	WindowStart       time.Time
	WindowEnd         time.Time
	NormalizedRate    float64
	BottleneckStep    string
	BindingConstraint string
	Steps             []CapacityStepFact
	Warnings          []string
	OmittedReason     string
}

// OmittedCapacityOutlook builds the fail-open shape: no capacity figures,
// only the reason the section was left out.
func OmittedCapacityOutlook(planningPathId string, windowStart, windowEnd time.Time, reason string) CapacityOutlook {
	return CapacityOutlook{
		PlanningPathId: planningPathId,
		WindowStart:    windowStart,
		WindowEnd:      windowEnd,
		OmittedReason:  reason,
	}
}

// SummarizeCapacityOutlook turns a successful path-capacity reading into a
// CapacityOutlook. A reading that violates warehouse-planning's published
// contract is rejected into an omitted outlook instead of being repaired:
// a non-ORDER unit (never converted) or a negative rate (never clamped).
// The binding constraint is looked up from the step_breakdown entry of the
// reported bottleneck step; if planning named a bottleneck that its own
// breakdown does not contain, the constraint stays empty and a warning says
// so rather than guessing one.
func SummarizeCapacityOutlook(planningPathId string, windowStart, windowEnd time.Time, fact PathCapacityFact) CapacityOutlook {
	if fact.NormalizedUnit != capacityOutlookOrderUnit {
		return OmittedCapacityOutlook(planningPathId, windowStart, windowEnd,
			"warehouse-planning returned unit \""+fact.NormalizedUnit+"\", expected \""+capacityOutlookOrderUnit+"\"; reading rejected")
	}
	if fact.NormalizedRate < 0 {
		return OmittedCapacityOutlook(planningPathId, windowStart, windowEnd,
			"warehouse-planning returned a negative normalized rate; reading rejected")
	}

	outlook := CapacityOutlook{
		PlanningPathId: planningPathId,
		WindowStart:    windowStart,
		WindowEnd:      windowEnd,
		NormalizedRate: fact.NormalizedRate,
		BottleneckStep: fact.BottleneckStep,
		Steps:          fact.Steps,
		Warnings:       append([]string(nil), fact.Warnings...),
	}

	found := false
	for _, s := range fact.Steps {
		if s.Step == fact.BottleneckStep {
			outlook.BindingConstraint = s.BindingConstraint
			found = true
			break
		}
	}
	if !found {
		outlook.Warnings = append(outlook.Warnings,
			"bottleneck step \""+fact.BottleneckStep+"\" is not in warehouse-planning's step_breakdown; binding constraint unknown")
	}
	return outlook
}
