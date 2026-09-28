package policy

import "fmt"

// TaskType mirrors fulfillment-execution's task type enum (see
// fulfillment-execution/internal/domain/task/task.go: Pick, Pack, Slam).
// warehouse-ops-agent never imports that package (see
// internal/architecture/architecture_test.go); this is a tool-boundary
// value type that must be validated on every entry point, since it can
// originate from untrusted model/caller input.
type TaskType string

const (
	TaskTypePick TaskType = "PICK"
	TaskTypePack TaskType = "PACK"
	TaskTypeSlam TaskType = "SLAM"
)

// Valid reports whether t is one of the task types this policy recognizes.
// Per the untrusted-input guardrail (PROPOSAL §5), an unknown value is
// always rejected — never silently defaulted or coerced.
func (t TaskType) Valid() bool {
	switch t {
	case TaskTypePick, TaskTypePack, TaskTypeSlam:
		return true
	default:
		return false
	}
}

// StrandedReservationAction is the recommended action this policy can
// emit. Both members are recommendations-only: the E2 use case never
// executes either itself; a human (or a later, separately-gated act-slice)
// decides whether to actually call inventory-storage's revoke_reservation
// write tool.
type StrandedReservationAction string

const (
	// ActionRevokeReservation recommends freeing usable stock by revoking
	// the named reservation.
	ActionRevokeReservation StrandedReservationAction = "revoke_reservation"
	// ActionHold recommends no action: either no correlated exception was
	// found, or the evidence needed to safely recommend a write is
	// incomplete (degraded upstream, missing blast radius).
	ActionHold StrandedReservationAction = "hold"
)

// EvidenceEntry is one line of the mandatory evidence trail: which upstream
// tool call produced it, and a human-readable summary of the reading. Every
// StrandedReservationException this policy returns carries at least one of
// these — the guardrail (PROPOSAL §5) requires every decision to show its
// work.
type EvidenceEntry struct {
	Tool    string
	Summary string
}

// StuckTaskSignal is the pure-domain shape of one expired/expiring-lease
// task read from fulfillment-execution's diagnose_stuck_tasks tool. The
// application layer maps ports.StuckTask into this shape at the port
// boundary so this package never depends on internal/ports.
type StuckTaskSignal struct {
	TaskId         string
	Type           TaskType
	LeaseStationId string
	Reason         string
}

// AvailabilitySignal is the pure-domain shape of inventory-storage's
// check_availability reading for the SKU under suspicion. A nil
// *AvailabilitySignal on Inputs means that upstream call did not succeed —
// the policy must degrade, not panic.
type AvailabilitySignal struct {
	SKU    string
	Usable int
}

// BinLine is one stock-unit line within the bin blast radius, mirroring
// inventory-storage's get_bin_occupancy tool output.
type BinLine struct {
	StockUnitId string
	SKU         string
	Reserved    int
	Usable      int
	State       string
}

// BlastRadius is the mandatory "what would this write touch" picture that
// must accompany a revoke_reservation recommendation: which SKU, which
// bin, how much reserved quantity for that SKU would return to usable, and
// the full per-line bin snapshot for context. Built from
// inventory-storage's get_bin_occupancy tool BEFORE any write executes.
type BlastRadius struct {
	SKU           string
	BinId         string
	ReservationId string
	QuantityFreed int
	BinLines      []BinLine
}

// StrandedReservationException is the E2 decision object this policy
// emits: whether a stranded reservation was detected, the ranked
// recommended action, why (Rationale), the full evidence trail, and — when
// a revoke is recommended — the blast radius that must be shown before
// anyone acts on it.
type StrandedReservationException struct {
	Detected      bool
	Action        StrandedReservationAction
	ReservationId string
	Rationale     string
	Evidence      []EvidenceEntry
	BlastRadius   *BlastRadius
}

// Inputs bundles the facts this pure policy correlates. Every field is a
// value already read by the application-layer use case from the outbound
// MCP-client ports; this package performs no I/O of its own. A nil pointer
// field means that upstream read did not succeed or was not attempted —
// the caller-side degrade path (PROPOSAL §5: "partial upstream
// availability") — never a hard failure.
type Inputs struct {
	// TaskType is the process/task type whose expired leases correlate
	// with the suspected stranded reservation (e.g. PICK). Untrusted
	// caller input — validated by Evaluate.
	TaskType TaskType

	// StuckTasks are fulfillment-execution's diagnose_stuck_tasks
	// results. StuckTasksAvailable is false when that call failed
	// (degrade case); StuckTasks is then ignored.
	StuckTasks          []StuckTaskSignal
	StuckTasksAvailable bool

	// MinUsableThreshold is the usable-quantity ceiling at or below which
	// inventory-storage's check_availability reading counts as a
	// shortfall correlated with the expired leases.
	MinUsableThreshold int

	// Availability is inventory-storage's check_availability reading for
	// the SKU under suspicion. nil means that call failed or was skipped
	// — degrade, do not treat as "no shortfall".
	Availability *AvailabilitySignal

	// ReservationId is the candidate reservation this policy would
	// recommend revoking once a shortfall correlates with expired
	// leases. Required to reach ActionRevokeReservation.
	ReservationId string

	// Bin is inventory-storage's get_bin_occupancy reading for the bin
	// holding the reservation's stock — the blast radius. nil means that
	// call failed, was skipped, or was never requested; a revoke can
	// never be recommended without it (PROPOSAL §5: blast radius is
	// mandatory before recommending a write).
	Bin *BlastRadius
}

// Evaluate runs the E2 correlation rule: fulfillment-execution's
// expired/expiring leases for a task type, correlated with
// inventory-storage's usable-stock shortfall for the affected SKU, →  a
// ranked revoke_reservation recommendation carrying its evidence trail and
// blast radius. Pure function: no I/O, no side effects.
//
// Degrade path: any missing upstream signal (StuckTasksAvailable=false,
// Availability=nil, or — once a shortfall is otherwise confirmed — Bin=nil)
// yields a typed ActionHold result with an evidence entry explaining the
// gap, never a panic or an unqualified recommendation.
func Evaluate(in Inputs) (StrandedReservationException, error) {
	if !in.TaskType.Valid() {
		return StrandedReservationException{}, fmt.Errorf("policy: unknown task type %q", in.TaskType)
	}

	var evidence []EvidenceEntry

	if !in.StuckTasksAvailable {
		evidence = addEvidence(evidence, "fulfillment-execution.diagnose_stuck_tasks", "unavailable — degrading to hold, no stuck-task evidence")
		return holdException(false, "", "fulfillment-execution's expired-lease signal is unavailable; degrading to hold rather than recommending on partial evidence", evidence), nil
	}

	expired := stuckByType(in.StuckTasks, in.TaskType)
	evidence = addEvidence(evidence, "fulfillment-execution.diagnose_stuck_tasks", fmt.Sprintf("%d %s task(s) with expired/expiring leases", len(expired), in.TaskType))

	if len(expired) == 0 {
		return holdException(false, "", fmt.Sprintf("no expired/expiring %s leases; nothing to correlate", in.TaskType), evidence), nil
	}

	if in.Availability == nil {
		evidence = addEvidence(evidence, "inventory-storage.check_availability", "unavailable — degrading to hold, cannot assess shortfall")
		return holdException(false, "", "inventory-storage's availability signal is unavailable; degrading to hold rather than recommending on partial evidence", evidence), nil
	}

	evidence = addEvidence(evidence, "inventory-storage.check_availability", fmt.Sprintf("SKU %s usable=%d (shortfall threshold <=%d)", in.Availability.SKU, in.Availability.Usable, in.MinUsableThreshold))

	if in.Availability.Usable > in.MinUsableThreshold {
		return holdException(false, "", fmt.Sprintf("SKU %s usable stock (%d) is above the shortfall threshold (%d); expired leases do not correlate with a shortfall", in.Availability.SKU, in.Availability.Usable, in.MinUsableThreshold), evidence), nil
	}

	// Both signals correlate: expired leases for this task type AND a
	// usable-stock shortfall for the SKU they would be releasing back to
	// the pool for. A revoke_reservation recommendation requires a
	// candidate reservation id and its blast radius before it can be
	// ranked — both are mandatory, never inferred. A missing candidate is
	// not caller error (the correlating scan may run before a specific
	// reservation is identified); degrade to a typed hold that still
	// surfaces the detected correlation, per the "partial upstream
	// availability" guardrail.
	if in.ReservationId == "" {
		evidence = addEvidence(evidence, "policy.stranded_reservation", "no candidate reservationId supplied — cannot recommend a revoke without one")
		return holdException(true, "", "a shortfall correlates with expired leases, but no candidate reservationId was supplied; holding rather than recommending a write without one", evidence), nil
	}

	if in.Bin == nil {
		evidence = addEvidence(evidence, "inventory-storage.get_bin_occupancy", "unavailable — degrading to hold, blast radius unknown")
		return holdException(true, in.ReservationId, "a shortfall correlates with expired leases, but the blast radius (bin occupancy) is unavailable; holding rather than recommending a write without it", evidence), nil
	}

	evidence = addEvidence(evidence, "inventory-storage.get_bin_occupancy", fmt.Sprintf("bin %s holds %d unit(s) of SKU %s reserved that would return to usable", in.Bin.BinId, in.Bin.QuantityFreed, in.Bin.SKU))

	return revokeException(in, expired, evidence), nil
}

// addEvidence appends one line to the mandatory evidence trail: which
// upstream tool call produced it and the human-readable reading.
func addEvidence(entries []EvidenceEntry, tool, summary string) []EvidenceEntry {
	return append(entries, EvidenceEntry{Tool: tool, Summary: summary})
}

// holdException is the shared shape of every degrade / no-correlation
// exit: a typed ActionHold result carrying the evidence accumulated so
// far. detected and reservationId distinguish a confirmed-but-held
// correlation from a plain no-correlation.
func holdException(detected bool, reservationId, rationale string, evidence []EvidenceEntry) StrandedReservationException {
	return StrandedReservationException{
		Detected:      detected,
		Action:        ActionHold,
		ReservationId: reservationId,
		Rationale:     rationale,
		Evidence:      evidence,
	}
}

// revokeException is the fully-correlated exit: the ranked
// ActionRevokeReservation recommendation with its rationale, the complete
// evidence trail, and the mandatory blast radius shown before anyone acts.
func revokeException(in Inputs, expired []StuckTaskSignal, evidence []EvidenceEntry) StrandedReservationException {
	return StrandedReservationException{
		Detected:      true,
		Action:        ActionRevokeReservation,
		ReservationId: in.ReservationId,
		Rationale: fmt.Sprintf(
			"%d expired-lease %s task(s) correlate with a usable-stock shortfall for SKU %s (usable=%d <= threshold %d); revoking reservation %q in bin %s would free %d unit(s) back to usable",
			len(expired), in.TaskType, in.Availability.SKU, in.Availability.Usable, in.MinUsableThreshold, in.ReservationId, in.Bin.BinId, in.Bin.QuantityFreed,
		),
		Evidence:    evidence,
		BlastRadius: in.Bin,
	}
}

func stuckByType(tasks []StuckTaskSignal, t TaskType) []StuckTaskSignal {
	out := make([]StuckTaskSignal, 0, len(tasks))
	for _, task := range tasks {
		if task.Type == t {
			out = append(out, task)
		}
	}
	return out
}
