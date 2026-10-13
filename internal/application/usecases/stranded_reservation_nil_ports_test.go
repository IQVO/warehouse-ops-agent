package usecases

import (
	"context"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// TestDetectStrandedReservation_UnconfiguredUpstreams_DegradeToHold: the
// composition root builds no MCP client for an empty endpoint, so either
// port may be nil. A nil port is an unavailable signal, never a panic.
func TestDetectStrandedReservation_UnconfiguredUpstreams_DegradeToHold(t *testing.T) {
	req := StrandedReservationRequest{TaskType: policy.TaskTypePick, SKU: "SKU-1", MinUsableThreshold: 5, ReservationId: "r-1", BinId: "B-1"}

	cases := []struct {
		name string
		uc   DetectStrandedReservation
	}{
		{"both nil", DetectStrandedReservation{}},
		{"fulfillment-execution nil", DetectStrandedReservation{InventoryStorage: &fakeInventoryStorage{availability: ports.Availability{SKU: "SKU-1", Usable: 0}}}},
		{"inventory-storage nil", DetectStrandedReservation{FulfillmentExecution: &fakeFulfillmentExecution{stuck: ports.StuckTasksResult{
			Count: 1,
			Tasks: []ports.StuckTask{{TaskId: "t-1", Type: "PICK", Reason: "lease already expired"}},
		}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.uc.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got.Detected || got.Action != policy.ActionHold {
				t.Fatalf("a missing upstream must degrade to an undetected hold, got %+v", got)
			}
		})
	}
}
