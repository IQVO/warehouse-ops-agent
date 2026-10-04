// Package usecases: CapacityOutlook (ADR 0013) — the daily brief's
// warehouse-planning section: what a path's composed capacity is over the
// next N hours, which step bottlenecks it and what binds that step.
package usecases

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// CapacityOutlook asks warehouse-planning (read tool
// get_process_path_capacity ONLY) for each monitored path's capacity over
// [now, now+Horizon) and hands the reading to policy.SummarizeCapacityOutlook.
//
// It is FAIL-OPEN and ADDITIVE:
//
//   - A nil *CapacityOutlook, or one with a nil Planning port, is "not
//     configured": Execute returns nil, so the DailyBrief omits the section
//     entirely and its output is byte-for-byte what it was before this
//     use case existed.
//   - When configured, Execute NEVER fails the brief. A path with no
//     warehouse-planning mapping, an unreachable server, a tool error
//     (including "no covering capacity window", planning's
//     missing-step-capacity) or a contract-violating reading comes back as
//     an outlook with OmittedReason set.
//
// It invents nothing: the process-path id, the location (the path's
// facility-layout site code, which is warehouse-planning's `location`) and
// the optional units_per_order / packages_per_order workload factors all come
// from the operator-supplied PathTarget. An unset factor is OMITTED from the
// call -- never defaulted -- and planning itself reports a missing one as a
// tool error, which surfaces as the omitted reason.
type CapacityOutlook struct {
	Planning ports.WarehousePlanningClient
	// Horizon is how far ahead the window extends from now. It must be
	// positive; a non-positive value omits every outlook with a reason
	// rather than silently falling back to a default.
	Horizon time.Duration
}

// Execute returns the capacity outlook for one path target, or nil when the
// use case is not configured (see the type's doc comment).
func (uc *CapacityOutlook) Execute(ctx context.Context, target PathTarget, now time.Time) *policy.CapacityOutlook {
	if uc == nil || uc.Planning == nil {
		return nil
	}

	start := now.UTC().Truncate(time.Second)
	end := start.Add(uc.Horizon)

	omit := func(reason string) *policy.CapacityOutlook {
		o := policy.OmittedCapacityOutlook(target.PlanningPathId, start, end, reason)
		return &o
	}

	if target.PlanningPathId == "" {
		return omit("no planningPathId configured for this path (set it in DAILY_BRIEF_PATH_TARGETS)")
	}
	if target.SiteCode == "" {
		return omit("no siteCode configured for this path; warehouse-planning's location is the site code")
	}
	if uc.Horizon <= 0 {
		return omit("capacity outlook horizon must be positive")
	}

	result, err := uc.Planning.GetProcessPathCapacity(ctx, ports.ProcessPathCapacityRequest{
		PathId:           target.PlanningPathId,
		Location:         target.SiteCode,
		WindowStart:      start.Format(time.RFC3339),
		WindowEnd:        end.Format(time.RFC3339),
		UnitsPerOrder:    target.UnitsPerOrder,
		PackagesPerOrder: target.PackagesPerOrder,
	})
	if err != nil {
		return omit("warehouse-planning get_process_path_capacity: " + err.Error())
	}

	steps := make([]policy.CapacityStepFact, 0, len(result.StepBreakdown))
	for _, s := range result.StepBreakdown {
		steps = append(steps, policy.CapacityStepFact{
			Step:              s.Step,
			NormalizedRate:    s.NormalizedRate,
			BindingConstraint: s.BindingConstraint,
		})
	}
	o := policy.SummarizeCapacityOutlook(target.PlanningPathId, start, end, policy.PathCapacityFact{
		NormalizedRate: result.NormalizedRate,
		NormalizedUnit: result.NormalizedUnit,
		BottleneckStep: result.BottleneckStep,
		Steps:          steps,
		Warnings:       result.Warnings,
	})
	return &o
}
