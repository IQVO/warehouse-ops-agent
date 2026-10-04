// Package usecases: DailyBrief (E3) — synthesizes the daily operational
// brief across all monitored sites/paths.
package usecases

import (
	"context"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// PathTarget is one process path this use case monitors, binding together
// each upstream context's own naming for "the same" path (see
// internal/domain/policy.PathTarget for why those names are not guaranteed
// equal across contexts). It is deployment-time configuration — the
// composition root (cmd/agent) builds the []PathTarget slice from the
// environment (see internal/config) and passes it in; this package never
// imports internal/config directly, to keep the hexagonal dependency rule
// (application depends only on domain, application, and ports) intact.
type PathTarget struct {
	SiteCode    string
	PathId      string
	ProcessPath string
	BuildingId  string
	ShiftId     string

	// PlanningPathId, UnitsPerOrder and PackagesPerOrder bind this path to
	// warehouse-planning for the optional capacity outlook (ADR 0013). All
	// three are operator-supplied deployment facts: PlanningPathId is the
	// id warehouse-planning's register_process_path knows (a multi-step
	// path such as PICK→REBIN→PACK, NOT wes-work-planning's PathId), and the
	// two factors are the workload conversions it needs for UNIT/PACKAGE
	// steps. Unset means "not configured": never defaulted.
	PlanningPathId   string
	UnitsPerOrder    *float64
	PackagesPerOrder *float64
}

// DailyBrief orchestrates the five outbound MCP-client ports (built in T1)
// to synthesize one DailyBrief: per configured PathTarget, it gathers
// backlog telemetry (wes), staffing gap (workforce-management), queue
// status and stuck-task diagnostics (fulfillment-execution), hands the
// gathered facts to the domain/policy correlation rule, and assembles the
// result grouped by facility-layout site.
//
// It NEVER calls a write tool and never mutates any upstream state — this
// is a pure read-side/decision-support use case, per the repo's ADR-0001
// and every T-card's guardrails.
type DailyBrief struct {
	Facility   ports.FacilityLayoutClient
	Wes        ports.WesWorkPlanningClient
	Fe         ports.FulfillmentExecutionClient
	Wfm        ports.WorkforceManagementClient
	Targets    []PathTarget
	Now        func() time.Time
	WithinSecs int // window passed to diagnose_stuck_tasks; 0 = only already-expired leases.

	// Outlook is the optional warehouse-planning capacity section (ADR
	// 0013). nil means "not configured": no section is added and the brief
	// is identical to one produced before the outlook existed.
	Outlook *CapacityOutlook
}

// now returns the injected clock, defaulting to time.Now so production
// callers need not set it.
func (uc *DailyBrief) now() time.Time {
	if uc.Now != nil {
		return uc.Now()
	}
	return time.Now()
}

// Execute synthesizes the full daily brief. It NEVER returns an error for a
// single upstream failure: each path's PathBrief degrades to whichever
// facts DID come back (guardrail §5, "partial availability... degrade to a
// partial/typed result, not a hard panic"). It returns an error only if it
// cannot resolve which sites exist at all (facility-layout list_sites
// itself unavailable) — even that is reported as a typed result with zero
// sites rather than propagated, so a caller always gets a DailyBrief back.
func (uc *DailyBrief) Execute(ctx context.Context) policy.DailyBrief {
	brief := policy.DailyBrief{GeneratedAt: uc.now()}

	siteNames := map[string]string{}
	if uc.Facility != nil {
		if sites, err := uc.Facility.ListSites(ctx); err == nil {
			for _, s := range sites.Sites {
				siteNames[s.Code] = s.Name
			}
		}
	}

	bySite := map[string]*policy.SiteBrief{}
	var order []string
	for _, target := range uc.Targets {
		pb := uc.synthesizeOne(ctx, target)
		brief.OpenExceptions = append(brief.OpenExceptions, pb.Exceptions...)

		sb, ok := bySite[target.SiteCode]
		if !ok {
			sb = &policy.SiteBrief{SiteCode: target.SiteCode, SiteName: siteNames[target.SiteCode]}
			bySite[target.SiteCode] = sb
			order = append(order, target.SiteCode)
		}
		sb.Paths = append(sb.Paths, pb)
	}

	for _, code := range order {
		brief.Sites = append(brief.Sites, *bySite[code])
	}
	brief.OpenExceptions = rankBySeverity(brief.OpenExceptions)

	return brief
}

// synthesizeOne gathers every upstream fact for one path, independently
// tolerating each source's failure, and hands the assembled facts to the
// domain policy correlation rule.
func (uc *DailyBrief) synthesizeOne(ctx context.Context, target PathTarget) policy.PathBrief {
	var unavailable []string

	var backlog *policy.BacklogFact
	if uc.Wes != nil {
		if bt, err := uc.Wes.GetBacklogTelemetry(ctx, target.PathId); err == nil {
			backlog = &policy.BacklogFact{
				BacklogDepth:       bt.BacklogDepth,
				WIP:                bt.WIP,
				OverAlarmThreshold: bt.OverAlarmThreshold,
			}
		} else {
			unavailable = append(unavailable, "wes-work-planning: "+err.Error())
		}
	} else {
		unavailable = append(unavailable, "wes-work-planning: client not configured")
	}

	var staffing *policy.StaffingFact
	if uc.Wfm != nil {
		if sg, err := uc.Wfm.GetStaffingGap(ctx, target.BuildingId, target.ShiftId, target.PathId); err == nil {
			staffing = &policy.StaffingFact{
				PlannedHeads: sg.PlannedHeads,
				ActiveHeads:  sg.ActiveHeads,
				Understaffed: sg.Understaffed,
			}
		} else {
			unavailable = append(unavailable, "workforce-management: "+err.Error())
		}
	} else {
		unavailable = append(unavailable, "workforce-management: client not configured")
	}

	var queue *policy.QueueFact
	if uc.Fe != nil {
		if qs, err := uc.Fe.GetQueueStatus(ctx, target.ProcessPath); err == nil {
			queue = &policy.QueueFact{Depth: qs.Depth}
		} else {
			unavailable = append(unavailable, "fulfillment-execution get_queue_status: "+err.Error())
		}
	} else {
		unavailable = append(unavailable, "fulfillment-execution: client not configured")
	}

	var stuck *policy.StuckTasksFact
	if uc.Fe != nil {
		if st, err := uc.Fe.DiagnoseStuckTasks(ctx, uc.WithinSecs); err == nil {
			stuck = &policy.StuckTasksFact{Count: countForPath(st, target.ProcessPath)}
		} else {
			unavailable = append(unavailable, "fulfillment-execution diagnose_stuck_tasks: "+err.Error())
		}
	}

	pathTarget := policy.PathTarget{
		SiteCode:    target.SiteCode,
		PathId:      target.PathId,
		ProcessPath: target.ProcessPath,
		BuildingId:  target.BuildingId,
		ShiftId:     target.ShiftId,
	}
	pb := policy.SynthesizePathBrief(pathTarget, backlog, staffing, queue, stuck, unavailable)
	// Informational only: never feeds the exception rules and never adds an
	// `unavailable` entry (a nil outlook leaves pb untouched).
	pb.CapacityOutlook = uc.Outlook.Execute(ctx, target, uc.now())
	return pb
}

// countForPath filters diagnose_stuck_tasks' flat task list down to the
// tasks whose type matches this path's fulfillment-execution process-path
// queue name. fulfillment-execution's diagnose_stuck_tasks is queue-wide
// (not path-scoped), so this filter is what makes the per-path count
// meaningful; a task whose Type does not match target.ProcessPath is not
// this path's problem.
func countForPath(result ports.StuckTasksResult, processPath string) int {
	if processPath == "" {
		return result.Count
	}
	n := 0
	for _, t := range result.Tasks {
		if t.Type == processPath {
			n++
		}
	}
	return n
}

// rankBySeverity returns exceptions ordered critical-first, then
// warning, then info, preserving relative order within the same severity
// (a stable sort) so the daily brief reads worst-first without reshuffling
// paths that tie.
func rankBySeverity(exceptions []policy.OpenException) []policy.OpenException {
	rank := func(s policy.Severity) int {
		switch s {
		case policy.SeverityCritical:
			return 0
		case policy.SeverityWarning:
			return 1
		default:
			return 2
		}
	}
	out := make([]policy.OpenException, len(exceptions))
	copy(out, exceptions)
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && rank(out[j-1].Severity) > rank(out[j].Severity) {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}
