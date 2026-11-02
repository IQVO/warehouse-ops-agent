package mcp

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// --- detect_stranded_reservation ---------------------------------------

// strandedReservationInput's fields mirror
// usecases.StrandedReservationRequest exactly: taskType and skuCode/
// minUsableThreshold anchor the correlation, reservationId/binId are
// optional candidates the tool would recommend revoking (with a mandatory
// blast radius) if the correlation confirms a stranded reservation. All
// fields are untrusted caller input, passed straight through to the
// use case's outbound port calls (each of which validates its own
// arguments on the upstream side) -- this tool never resolves or guesses
// a candidate reservation/bin on the caller's behalf.
type strandedReservationInput struct {
	TaskType           string `json:"taskType" jsonschema:"the process/task type whose expired leases are the suspected correlation signal: PICK, PACK, or SLAM"`
	WithinSeconds      int    `json:"withinSeconds,omitempty" jsonschema:"bounds fulfillment-execution's diagnose_stuck_tasks window; 0 (default) means already-expired only"`
	SKU                string `json:"sku" jsonschema:"the stock-keeping unit under suspicion of being stranded"`
	MinUsableThreshold int    `json:"minUsableThreshold" jsonschema:"the usable-quantity ceiling at or below which a shortfall is considered correlated with the expired leases"`
	ReservationId      string `json:"reservationId,omitempty" jsonschema:"the candidate reservation this tool would recommend revoking if the correlation confirms a stranded reservation; omit if no candidate is yet known"`
	BinId              string `json:"binId,omitempty" jsonschema:"the bin holding the reservation's stock, used to build the mandatory blast radius before any revoke is recommended; omit if not yet known"`
}

type binLineDTO struct {
	StockUnitId string `json:"stockUnitId"`
	SKU         string `json:"sku"`
	Reserved    int    `json:"reserved"`
	Usable      int    `json:"usable"`
	State       string `json:"state"`
}

type blastRadiusDTO struct {
	SKU           string       `json:"sku"`
	BinId         string       `json:"binId"`
	ReservationId string       `json:"reservationId"`
	QuantityFreed int          `json:"quantityFreed"`
	BinLines      []binLineDTO `json:"binLines"`
}

type strandedReservationEvidenceDTO struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

type strandedReservationOutput struct {
	Detected      bool                             `json:"detected"`
	Action        string                           `json:"action"`
	ReservationId string                           `json:"reservationId,omitempty"`
	Rationale     string                           `json:"rationale"`
	Evidence      []strandedReservationEvidenceDTO `json:"evidence"`
	BlastRadius   *blastRadiusDTO                  `json:"blastRadius,omitempty"`
}

func (d Deps) detectStrandedReservation(ctx context.Context, in strandedReservationInput) (strandedReservationOutput, error) {
	result, err := d.StrandedReservation.Execute(ctx, usecases.StrandedReservationRequest{
		TaskType:           policy.TaskType(in.TaskType),
		WithinSeconds:      in.WithinSeconds,
		SKU:                in.SKU,
		MinUsableThreshold: in.MinUsableThreshold,
		ReservationId:      in.ReservationId,
		BinId:              in.BinId,
	})
	if err != nil {
		return strandedReservationOutput{}, err
	}

	evidence := make([]strandedReservationEvidenceDTO, 0, len(result.Evidence))
	for _, e := range result.Evidence {
		evidence = append(evidence, strandedReservationEvidenceDTO{Tool: e.Tool, Summary: e.Summary})
	}

	out := strandedReservationOutput{
		Detected:      result.Detected,
		Action:        string(result.Action),
		ReservationId: result.ReservationId,
		Rationale:     result.Rationale,
		Evidence:      evidence,
	}
	if result.BlastRadius != nil {
		lines := make([]binLineDTO, 0, len(result.BlastRadius.BinLines))
		for _, l := range result.BlastRadius.BinLines {
			lines = append(lines, binLineDTO{
				StockUnitId: l.StockUnitId,
				SKU:         l.SKU,
				Reserved:    l.Reserved,
				Usable:      l.Usable,
				State:       l.State,
			})
		}
		out.BlastRadius = &blastRadiusDTO{
			SKU:           result.BlastRadius.SKU,
			BinId:         result.BlastRadius.BinId,
			ReservationId: result.BlastRadius.ReservationId,
			QuantityFreed: result.BlastRadius.QuantityFreed,
			BinLines:      lines,
		}
	}
	return out, nil
}
