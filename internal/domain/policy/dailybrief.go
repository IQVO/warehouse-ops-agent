// Package policy: daily-brief (E3) correlation rules.
//
// This file adds the pure, side-effect-free types and correlation logic
// behind the synthesized daily operational brief: per-path facts gathered
// from the five bounded contexts (via internal/ports client interfaces, one
// layer out in internal/application/usecases), and a small set of
// deterministic rules that flag a path as an open exception worth a human's
// attention.
//
// Every type here is a plain value — no ports import, no I/O — per the
// hexagonal dependency rule this package enforces on itself (see this
// package's doc.go and internal/architecture/architecture_test.go's
// "domain (policy) has no internal dependencies except domain").
package policy

import "time"

// PathTarget identifies one process path to include in the daily brief,
// binding together each upstream context's own naming for "the same"
// path: wes-work-planning's PathId, fulfillment-execution's ProcessPath
// queue name (PICK/PACK/SLAM), and workforce-management's
// (BuildingId, ShiftId, PathId) staffing-gap key, grouped under the
// facility-layout SiteCode it physically belongs to.
//
// These identifiers are NOT guaranteed equal across contexts — each
// bounded context owns its own ubiquitous language — so wiring which wes
// PathId corresponds to which fe queue and which workforce-management path
// is a deployment-time configuration concern (internal/config), never
// something this policy package infers.
type PathTarget struct {
	SiteCode    string
	PathId      string
	ProcessPath string
	BuildingId  string
	ShiftId     string
}

// BacklogFact mirrors the wes-work-planning get_backlog_telemetry facts
// this policy correlates over. A nil *BacklogFact on a PathBrief means the
// upstream call did not succeed for this path, not that the backlog is
// empty.
type BacklogFact struct {
	BacklogDepth       int
	WIP                int
	OverAlarmThreshold bool
}

// StaffingFact mirrors the workforce-management get_staffing_gap facts.
type StaffingFact struct {
	PlannedHeads int
	ActiveHeads  int
	Understaffed bool
}

// QueueFact mirrors the fulfillment-execution get_queue_status facts.
type QueueFact struct {
	Depth int
}

// StuckTasksFact mirrors the fulfillment-execution diagnose_stuck_tasks
// facts, already filtered to the tasks belonging to one path's process-path
// queue.
type StuckTasksFact struct {
	Count int
}

// Severity is a coarse ranking for an OpenException, so a host (or a human
// scanning the daily brief) can triage without re-deriving it from the
// evidence trail.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// ExceptionKind names which correlation rule fired.
type ExceptionKind string

const (
	// ExceptionFlowBalanceRisk fires when a path shows more than one
	// independent signal of flow imbalance at once (backlog over its alarm
	// threshold, understaffed, or tasks stuck) — the E1 shape, correlated
	// locally by this daily-brief slice pending the dedicated E1 decision
	// policy (see the sibling "T2" kanban card) landing as its own,
	// reusable package.
	ExceptionFlowBalanceRisk ExceptionKind = "flow_balance_risk"
)

// OpenException is one path's synthesized, human-gated exception: WHAT
// tripped (Kind), HOW BADLY (Severity), and the full evidence trail (which
// upstream readings drove the call) — never a silent recommendation. This
// slice never executes a write; it only surfaces the exception.
type OpenException struct {
	Kind     ExceptionKind
	SiteCode string
	PathId   string
	Severity Severity
	Summary  string
	Evidence []string
}

// PathBrief is one path's synthesized read model within a DailyBrief: the
// raw facts gathered for it (nil where the upstream source did not answer),
// which upstream sources were unavailable, and the exceptions this policy
// derived from whichever facts DID come back — partial data still produces
// a partial, typed brief, never a hard failure.
type PathBrief struct {
	Target      PathTarget
	Backlog     *BacklogFact
	Staffing    *StaffingFact
	Queue       *QueueFact
	Stuck       *StuckTasksFact
	Unavailable []string
	Exceptions  []OpenException

	// CapacityOutlook is warehouse-planning's capacity for this path over
	// the next N hours (ADR 0013). nil means the outlook is not configured
	// (the DTOs then omit it entirely); a non-nil outlook with an
	// OmittedReason means it was configured but could not be produced.
	CapacityOutlook *CapacityOutlook
}

// SynthesizePathBrief assembles one path's facts into a PathBrief and
// derives its exceptions. Any fact left nil is simply excluded from the
// correlation — a path with zero facts available yields zero exceptions,
// never a panic.
func SynthesizePathBrief(target PathTarget, backlog *BacklogFact, staffing *StaffingFact, queue *QueueFact, stuck *StuckTasksFact, unavailable []string) PathBrief {
	brief := PathBrief{
		Target:      target,
		Backlog:     backlog,
		Staffing:    staffing,
		Queue:       queue,
		Stuck:       stuck,
		Unavailable: unavailable,
	}
	brief.Exceptions = deriveExceptions(brief)
	return brief
}

// deriveExceptions applies the flow-balance-risk correlation rule: a path
// is flagged when at least two independent signals fire together. A single
// signal alone (e.g. only understaffed, with backlog healthy and nothing
// stuck) is treated as ordinary operating noise, not an exception — this
// mirrors the "correlate, don't alert on one metric" guardrail from the
// proposal's E1 design.
func deriveExceptions(brief PathBrief) []OpenException {
	var evidence []string
	signals := 0

	if brief.Backlog != nil && brief.Backlog.OverAlarmThreshold {
		signals++
		evidence = append(evidence, "wes-work-planning get_backlog_telemetry: over alarm threshold "+
			"(backlogDepth="+itoa(brief.Backlog.BacklogDepth)+", wip="+itoa(brief.Backlog.WIP)+")")
	}
	if brief.Staffing != nil && brief.Staffing.Understaffed {
		signals++
		evidence = append(evidence, "workforce-management get_staffing_gap: understaffed "+
			"(planned="+itoa(brief.Staffing.PlannedHeads)+", active="+itoa(brief.Staffing.ActiveHeads)+")")
	}
	if brief.Stuck != nil && brief.Stuck.Count > 0 {
		signals++
		evidence = append(evidence, "fulfillment-execution diagnose_stuck_tasks: "+itoa(brief.Stuck.Count)+" stuck task(s)")
	}

	if signals < 2 {
		return nil
	}

	severity := SeverityWarning
	if signals >= 3 {
		severity = SeverityCritical
	}

	return []OpenException{{
		Kind:     ExceptionFlowBalanceRisk,
		SiteCode: brief.Target.SiteCode,
		PathId:   brief.Target.PathId,
		Severity: severity,
		Summary:  itoa(signals) + " independent flow-imbalance signal(s) correlated for path " + brief.Target.PathId,
		Evidence: evidence,
	}}
}

// DailyBrief is the full synthesized daily operational brief: every
// monitored path's PathBrief, the flattened list of open exceptions across
// all paths (ranked critical-first), and when it was generated.
type DailyBrief struct {
	GeneratedAt    time.Time
	Sites          []SiteBrief
	OpenExceptions []OpenException
}

// SiteBrief groups every monitored PathBrief under the facility-layout site
// it belongs to.
type SiteBrief struct {
	SiteCode string
	SiteName string
	Paths    []PathBrief
}

// itoa avoids pulling in strconv's full surface for one call site; kept
// local and trivial so this package's only import stays "time".
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
