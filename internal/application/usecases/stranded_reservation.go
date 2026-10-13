package usecases

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// StrandedReservationRequest is the caller-supplied input to
// DetectStrandedReservation. TaskType, SKU, ReservationId, and BinId
// originate outside this module (an operator, a scheduled scan, or a
// higher host agent) and are therefore untrusted: TaskType is validated by
// policy.Evaluate; ReservationId/BinId/SKU are opaque identifiers passed
// straight through to the read-only MCP tools that already validate them
// on their own side.
type StrandedReservationRequest struct {
	// TaskType is the process/task type whose expired leases are the
	// suspected correlation signal (e.g. policy.TaskTypePick).
	TaskType policy.TaskType
	// WithinSeconds bounds fulfillment-execution's diagnose_stuck_tasks
	// window (0 = already-expired only).
	WithinSeconds int
	// SKU is the stock-keeping unit under suspicion of being stranded.
	SKU string
	// MinUsableThreshold is the usable-quantity ceiling at or below which
	// a shortfall is considered correlated with the expired leases.
	MinUsableThreshold int
	// ReservationId is the candidate reservation this use case would
	// recommend revoking if the correlation confirms a stranded
	// reservation. May be empty if no candidate is yet known — the
	// policy will still report Detected for visibility, but never a
	// revoke recommendation without one.
	ReservationId string
	// BinId is the bin holding the reservation's stock, used to build the
	// mandatory blast radius via get_bin_occupancy before any revoke is
	// recommended. May be empty if not yet known.
	BinId string
}

// DetectStrandedReservation is the E2 use case: it gathers facts from
// fulfillment-execution (diagnose_stuck_tasks) and inventory-storage
// (check_availability, get_bin_occupancy) through the outbound MCP-client
// ports built in T1, and hands them to the pure domain policy
// (internal/domain/policy.Evaluate) to produce a
// StrandedReservationException recommendation.
//
// This use case never calls a write tool — it only reads. Per the
// guardrails: no aggregate/DB writes, and it degrades to a partial/typed
// result rather than panicking when an upstream call fails.
type DetectStrandedReservation struct {
	FulfillmentExecution ports.FulfillmentExecutionClient
	InventoryStorage     ports.InventoryStorageClient
}

// Execute runs the correlation. Errors from upstream tool calls are never
// propagated as a hard failure of this use case — each missing signal is
// folded into the policy Inputs as an unavailable/nil field, and
// policy.Evaluate degrades accordingly (PROPOSAL §5: "partial upstream
// availability"). The only error this method returns is a policy-level
// validation error (e.g. an unrecognized TaskType), which is a caller
// input error, not an upstream availability problem.
func (uc DetectStrandedReservation) Execute(ctx context.Context, req StrandedReservationRequest) (policy.StrandedReservationException, error) {
	in := policy.Inputs{
		TaskType:           req.TaskType,
		MinUsableThreshold: req.MinUsableThreshold,
		ReservationId:      req.ReservationId,
	}

	// A nil port means the upstream is not configured (the composition
	// root builds no MCP client for an empty endpoint): the signal is
	// unavailable, exactly like a failed call.
	if uc.FulfillmentExecution != nil {
		if stuck, err := uc.FulfillmentExecution.DiagnoseStuckTasks(ctx, req.WithinSeconds); err == nil {
			in.StuckTasksAvailable = true
			in.StuckTasks = toStuckTaskSignals(stuck)
		}
	}
	if uc.InventoryStorage != nil {
		uc.gatherInventorySignals(ctx, req, &in)
	}

	return policy.Evaluate(in)
}

// gatherInventorySignals reads availability and bin occupancy from
// inventory-storage into in; a failed read leaves the field nil.
func (uc DetectStrandedReservation) gatherInventorySignals(ctx context.Context, req StrandedReservationRequest, in *policy.Inputs) {
	if req.SKU != "" {
		if avail, err := uc.InventoryStorage.CheckAvailability(ctx, req.SKU); err == nil {
			in.Availability = &policy.AvailabilitySignal{SKU: avail.SKU, Usable: avail.Usable}
		}
	}

	if req.ReservationId != "" && req.BinId != "" {
		if occ, err := uc.InventoryStorage.GetBinOccupancy(ctx, req.BinId); err == nil {
			in.Bin = toBlastRadius(occ, req.SKU, req.ReservationId)
		}
	}
}

// toStuckTaskSignals maps the outbound-port DTO into the pure domain shape,
// keeping ports out of the domain package's import graph. Any stuck-task
// entry whose Type does not match one of the policy's known TaskType
// values is carried through unchanged: policy.Evaluate only ever filters
// by an already-validated TaskType from the request, so an unrecognized
// upstream type simply never matches and is harmlessly excluded from the
// correlation rather than rejected here.
func toStuckTaskSignals(result ports.StuckTasksResult) []policy.StuckTaskSignal {
	out := make([]policy.StuckTaskSignal, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		out = append(out, policy.StuckTaskSignal{
			TaskId:         task.TaskId,
			Type:           policy.TaskType(task.Type),
			LeaseStationId: task.LeaseStationId,
			Reason:         task.Reason,
		})
	}
	return out
}

// toBlastRadius maps inventory-storage's get_bin_occupancy reading into the
// mandatory blast-radius shape for the SKU under suspicion: which bin,
// which reservation, how much reserved quantity for that SKU would return
// to usable, and the full per-line snapshot for context.
func toBlastRadius(occ ports.BinOccupancy, sku, reservationId string) *policy.BlastRadius {
	lines := make([]policy.BinLine, 0, len(occ.Lines))
	freed := 0
	for _, line := range occ.Lines {
		lines = append(lines, policy.BinLine{
			StockUnitId: line.StockUnitId,
			SKU:         line.SKU,
			Reserved:    line.Reserved,
			Usable:      line.Usable,
			State:       line.State,
		})
		if line.SKU == sku {
			freed += line.Reserved
		}
	}
	return &policy.BlastRadius{
		SKU:           sku,
		BinId:         occ.BinId,
		ReservationId: reservationId,
		QuantityFreed: freed,
		BinLines:      lines,
	}
}
