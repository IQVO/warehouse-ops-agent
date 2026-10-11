package policy

import (
	"fmt"
	"sort"
	"strings"
)

// TransferState mirrors network-inventory-planning's transfer state enum (its
// get_transfer / list_transfers tool descriptions). warehouse-ops-agent never
// imports that module; this is a tool-boundary value type. States read FROM
// upstream are tolerated when unknown (see TriageTransfer); states supplied
// BY a caller are validated with ParseStuckTransferState.
type TransferState string

const (
	TransferDraft         TransferState = "DRAFT"
	TransferProposed      TransferState = "PROPOSED"
	TransferApproved      TransferState = "APPROVED"
	TransferAllocating    TransferState = "ALLOCATING"
	TransferAllocated     TransferState = "ALLOCATED"
	TransferPicked        TransferState = "PICKED"
	TransferInTransit     TransferState = "IN_TRANSIT"
	TransferArrived       TransferState = "ARRIVED"
	TransferReceived      TransferState = "RECEIVED"
	TransferUnfulfillable TransferState = "UNFULFILLABLE"
	TransferCancelled     TransferState = "CANCELLED"
)

// nonTerminalTransferStates are the states find_stuck_transfers can report:
// RECEIVED, UNFULFILLABLE and CANCELLED are finished and never reported.
var nonTerminalTransferStates = []TransferState{
	TransferDraft, TransferProposed, TransferApproved, TransferAllocating,
	TransferAllocated, TransferPicked, TransferInTransit, TransferArrived,
}

// Terminal reports whether the state is a finished one.
func (s TransferState) Terminal() bool {
	switch s {
	case TransferReceived, TransferUnfulfillable, TransferCancelled:
		return true
	default:
		return false
	}
}

// ParseStuckTransferState validates a caller-supplied state filter for the
// stuck-transfer triage. Untrusted input: only the known NON-terminal states
// are accepted; anything else (including a terminal state, which can never be
// stuck) is rejected, never defaulted.
func ParseStuckTransferState(raw string) (TransferState, error) {
	for _, s := range nonTerminalTransferStates {
		if string(s) == raw {
			return s, nil
		}
	}
	names := make([]string, 0, len(nonTerminalTransferStates))
	for _, s := range nonTerminalTransferStates {
		names = append(names, string(s))
	}
	return "", fmt.Errorf("policy: unknown or terminal transfer state %q; a transfer can only be stuck in one of %s", raw, strings.Join(names, ", "))
}

// TransferCause is the advisory classification of why a non-terminal
// transfer is not advancing, derived from its state alone (the saga's next
// step is a function of the state).
type TransferCause string

const (
	// CauseInventoryReplyMissing: ALLOCATING -- NIP asked inventory-storage
	// to allocate and the reply has not arrived.
	CauseInventoryReplyMissing TransferCause = "inventory-reply-missing"
	// CauseFloorWorkNotProgressing: ALLOCATED / PICKED -- stock is reserved
	// but the pick/ship work on the floor is not advancing.
	CauseFloorWorkNotProgressing TransferCause = "floor-work-not-progressing"
	// CauseDestinationReceiptMissing: IN_TRANSIT / ARRIVED -- the goods left
	// the origin but the destination has not confirmed receipt.
	CauseDestinationReceiptMissing TransferCause = "destination-receipt-missing"
	// CauseUnclassified: a non-terminal state outside the three saga stages
	// above (DRAFT, PROPOSED, APPROVED) or a state this agent does not
	// recognise. No cause is guessed.
	CauseUnclassified TransferCause = "unclassified"
	// CauseNone: the transfer is finished; there is nothing to triage.
	CauseNone TransferCause = "none"
)

// TransferSignal is the pure-domain shape of one NIP transfer. The
// application layer maps ports.Transfer into it at the port boundary.
type TransferSignal struct {
	Id                string
	State             string
	SKU               string
	Quantity          int
	PickedQuantity    int
	OriginSiteId      string
	DestinationSiteId string
	ReservationId     string
	RejectionReason   string
}

// TransferTriage is the advisory read of one transfer: the cause class, a
// one-line reading and the next READ-ONLY check an operator can make. It is
// never an action: moving, approving or cancelling a transfer is an operator
// decision taken in network-inventory-planning itself.
type TransferTriage struct {
	Cause     TransferCause
	Summary   string
	NextCheck string
}

// TriageTransfer classifies a transfer by its state:
//
//	ALLOCATING          -> inventory reply missing
//	ALLOCATED, PICKED   -> floor work not progressing
//	IN_TRANSIT, ARRIVED -> destination receipt missing
//	RECEIVED, UNFULFILLABLE, CANCELLED -> finished (CauseNone)
//	anything else       -> unclassified (no cause is invented)
//
// Pure function: no I/O. An unknown state never fails; it is reported as
// unclassified with the raw state quoted.
func TriageTransfer(sig TransferSignal) TransferTriage {
	route := fmt.Sprintf("%d x %s from %s to %s", sig.Quantity, sig.SKU, sig.OriginSiteId, sig.DestinationSiteId)
	switch TransferState(sig.State) {
	case TransferAllocating:
		return TransferTriage{
			Cause:   CauseInventoryReplyMissing,
			Summary: fmt.Sprintf("allocation of %s was requested from inventory-storage and no reply has moved the transfer on", route),
			NextCheck: fmt.Sprintf("Check inventory-storage for a reservation or an allocation outcome for SKU %s at %s tied to transfer %s, "+
				"and check whether the allocation reply message is stuck or failed on the bus.", sig.SKU, sig.OriginSiteId, sig.Id),
		}
	case TransferAllocated, TransferPicked:
		return TransferTriage{
			Cause:   CauseFloorWorkNotProgressing,
			Summary: fmt.Sprintf("stock for %s is %s but the floor work (pick / hand-off) is not progressing", route, strings.ToLower(sig.State)),
			NextCheck: fmt.Sprintf("Check %s's fulfilment floor for the pick or outbound task of transfer %s%s: "+
				"an unclaimed or lease-expired task, a blocked station or missing staffing.", sig.OriginSiteId, sig.Id, reservationHint(sig.ReservationId)),
		}
	case TransferInTransit, TransferArrived:
		return TransferTriage{
			Cause:   CauseDestinationReceiptMissing,
			Summary: fmt.Sprintf("%s left the origin (%s) but the destination has not confirmed receipt", route, strings.ToLower(sig.State)),
			NextCheck: fmt.Sprintf("Check with %s whether the goods of transfer %s were physically received and whether the receipt was recorded in its inventory.",
				sig.DestinationSiteId, sig.Id),
		}
	case TransferReceived, TransferUnfulfillable, TransferCancelled:
		summary := fmt.Sprintf("transfer is finished (%s); nothing to triage", sig.State)
		if sig.RejectionReason != "" {
			summary += ": " + sig.RejectionReason
		}
		return TransferTriage{Cause: CauseNone, Summary: summary}
	default:
		return TransferTriage{
			Cause:     CauseUnclassified,
			Summary:   fmt.Sprintf("state %q is outside the allocation, floor and receipt stages this agent classifies", sig.State),
			NextCheck: fmt.Sprintf("Read the audit trail of transfer %s (get_transfer) to see which step last completed and what it is waiting for.", sig.Id),
		}
	}
}

func reservationHint(reservationID string) string {
	if reservationID == "" {
		return ""
	}
	return " (reservation " + reservationID + ")"
}

// CauseCount is one row of a per-cause tally.
type CauseCount struct {
	Cause TransferCause
	Count int
}

// TallyCauses counts triaged causes, ordered by descending count and then by
// cause name so the output is deterministic.
func TallyCauses(causes []TransferCause) []CauseCount {
	counts := map[TransferCause]int{}
	for _, c := range causes {
		counts[c]++
	}
	out := make([]CauseCount, 0, len(counts))
	for c, n := range counts {
		out = append(out, CauseCount{Cause: c, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Cause < out[j].Cause
	})
	return out
}
