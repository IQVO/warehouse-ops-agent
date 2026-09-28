package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// fakeFulfillmentExecution is a table-driven-friendly fake of
// ports.FulfillmentExecutionClient — no live MCP server involved.
type fakeFulfillmentExecution struct {
	stuck    ports.StuckTasksResult
	stuckErr error
}

func (f fakeFulfillmentExecution) GetQueueStatus(context.Context, string) (ports.QueueStatus, error) {
	return ports.QueueStatus{}, errors.New("not used by this use case")
}

func (f fakeFulfillmentExecution) FindClaimableWork(context.Context, string) (ports.ClaimableWorkResult, error) {
	return ports.ClaimableWorkResult{}, errors.New("not used by this use case")
}

func (f fakeFulfillmentExecution) DiagnoseStuckTasks(context.Context, int) (ports.StuckTasksResult, error) {
	if f.stuckErr != nil {
		return ports.StuckTasksResult{}, f.stuckErr
	}
	return f.stuck, nil
}

var _ ports.FulfillmentExecutionClient = fakeFulfillmentExecution{}

// fakeInventoryStorage is a table-driven-friendly fake of
// ports.InventoryStorageClient.
type fakeInventoryStorage struct {
	availability    ports.Availability
	availabilityErr error
	occupancy       ports.BinOccupancy
	occupancyErr    error
}

func (f fakeInventoryStorage) CheckAvailability(context.Context, string) (ports.Availability, error) {
	if f.availabilityErr != nil {
		return ports.Availability{}, f.availabilityErr
	}
	return f.availability, nil
}

func (f fakeInventoryStorage) GetBinOccupancy(context.Context, string) (ports.BinOccupancy, error) {
	if f.occupancyErr != nil {
		return ports.BinOccupancy{}, f.occupancyErr
	}
	return f.occupancy, nil
}

var _ ports.InventoryStorageClient = fakeInventoryStorage{}

// strandedReservationCase is one row of the DetectStrandedReservation
// correlation table.
type strandedReservationCase struct {
	name            string
	fe              fakeFulfillmentExecution
	inv             fakeInventoryStorage
	req             StrandedReservationRequest
	wantErr         bool
	wantDetected    bool
	wantAction      policy.StrandedReservationAction
	wantBlastRadius bool
}

func TestDetectStrandedReservation_Execute(t *testing.T) {
	tests := []strandedReservationCase{
		{
			name: "rejects unknown task type",
			req: StrandedReservationRequest{
				TaskType: policy.TaskType("BOGUS"),
			},
			wantErr: true,
		},
		{
			name: "degrades to hold when fulfillment-execution is unavailable",
			fe:   fakeFulfillmentExecution{stuckErr: errors.New("connect: dial tcp: connection refused")},
			inv:  fakeInventoryStorage{availability: ports.Availability{SKU: "SKU-1", Usable: 0}},
			req: StrandedReservationRequest{
				TaskType:           policy.TaskTypePick,
				SKU:                "SKU-1",
				MinUsableThreshold: 5,
			},
			wantDetected: false,
			wantAction:   policy.ActionHold,
		},
		{
			name: "degrades to hold when inventory-storage availability is unavailable",
			fe: fakeFulfillmentExecution{stuck: ports.StuckTasksResult{
				Count: 1,
				Tasks: []ports.StuckTask{{TaskId: "t-1", Type: "PICK", Reason: "lease already expired"}},
			}},
			inv: fakeInventoryStorage{availabilityErr: errors.New("connect: timeout")},
			req: StrandedReservationRequest{
				TaskType:           policy.TaskTypePick,
				SKU:                "SKU-1",
				MinUsableThreshold: 5,
			},
			wantDetected: false,
			wantAction:   policy.ActionHold,
		},
		{
			name: "holds when usable stock is above threshold",
			fe: fakeFulfillmentExecution{stuck: ports.StuckTasksResult{
				Count: 1,
				Tasks: []ports.StuckTask{{TaskId: "t-1", Type: "PICK", Reason: "lease already expired"}},
			}},
			inv: fakeInventoryStorage{availability: ports.Availability{SKU: "SKU-1", Usable: 100}},
			req: StrandedReservationRequest{
				TaskType:           policy.TaskTypePick,
				SKU:                "SKU-1",
				MinUsableThreshold: 5,
			},
			wantDetected: false,
			wantAction:   policy.ActionHold,
		},
		{
			name: "detects but holds when no reservation/bin candidate was supplied",
			fe: fakeFulfillmentExecution{stuck: ports.StuckTasksResult{
				Count: 1,
				Tasks: []ports.StuckTask{{TaskId: "t-1", Type: "PICK", Reason: "lease already expired"}},
			}},
			inv: fakeInventoryStorage{availability: ports.Availability{SKU: "SKU-1", Usable: 0}},
			req: StrandedReservationRequest{
				TaskType:           policy.TaskTypePick,
				SKU:                "SKU-1",
				MinUsableThreshold: 5,
				// ReservationId and BinId intentionally empty.
			},
			wantDetected: true,
			wantAction:   policy.ActionHold,
		},
		{
			name: "detects but holds when bin occupancy lookup fails (blast radius unavailable)",
			fe: fakeFulfillmentExecution{stuck: ports.StuckTasksResult{
				Count: 1,
				Tasks: []ports.StuckTask{{TaskId: "t-1", Type: "PICK", Reason: "lease already expired"}},
			}},
			inv: fakeInventoryStorage{
				availability: ports.Availability{SKU: "SKU-1", Usable: 0},
				occupancyErr: errors.New("connect: timeout"),
			},
			req: StrandedReservationRequest{
				TaskType:           policy.TaskTypePick,
				SKU:                "SKU-1",
				MinUsableThreshold: 5,
				ReservationId:      "R-1",
				BinId:              "BIN-A1",
			},
			wantDetected: true,
			wantAction:   policy.ActionHold,
		},
		{
			name: "recommends revoke_reservation with blast radius when fully correlated",
			fe: fakeFulfillmentExecution{stuck: ports.StuckTasksResult{
				Count: 2,
				Tasks: []ports.StuckTask{
					{TaskId: "t-1", Type: "PICK", LeaseStationId: "ST-1", Reason: "lease already expired"},
					{TaskId: "t-2", Type: "PACK", LeaseStationId: "ST-2", Reason: "lease already expired"},
				},
			}},
			inv: fakeInventoryStorage{
				availability: ports.Availability{SKU: "SKU-1", Usable: 2},
				occupancy: ports.BinOccupancy{
					BinId:     "BIN-A1",
					UnitCount: 1,
					Reserved:  12,
					Lines: []ports.BinOccupancyLine{
						{StockUnitId: "SU-1", SKU: "SKU-1", OnHand: 12, Reserved: 12, Usable: 0, State: "RESERVED"},
					},
				},
			},
			req: StrandedReservationRequest{
				TaskType:           policy.TaskTypePick,
				SKU:                "SKU-1",
				MinUsableThreshold: 5,
				ReservationId:      "R-1",
				BinId:              "BIN-A1",
			},
			wantDetected:    true,
			wantAction:      policy.ActionRevokeReservation,
			wantBlastRadius: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runStrandedReservationCase(t, tc)
		})
	}
}

// runStrandedReservationCase executes one table case against the fakes
// and asserts the outcome's error/detected/action shape.
func runStrandedReservationCase(t *testing.T, tc strandedReservationCase) {
	t.Helper()
	uc := DetectStrandedReservation{
		FulfillmentExecution: tc.fe,
		InventoryStorage:     tc.inv,
	}

	got, err := uc.Execute(context.Background(), tc.req)
	if tc.wantErr {
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		return
	}
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Detected != tc.wantDetected {
		t.Fatalf("Detected = %v, want %v", got.Detected, tc.wantDetected)
	}
	if got.Action != tc.wantAction {
		t.Fatalf("Action = %q, want %q", got.Action, tc.wantAction)
	}
	hasBlastRadius := got.BlastRadius != nil
	if hasBlastRadius != tc.wantBlastRadius {
		t.Fatalf("BlastRadius present = %v, want %v (got %+v)", hasBlastRadius, tc.wantBlastRadius, got.BlastRadius)
	}
	if tc.wantBlastRadius {
		assertRevokeBlastRadius(t, got, tc.req.BinId)
	}
}

// assertRevokeBlastRadius checks the fully-correlated revoke outcome's
// mandatory blast radius and evidence trail.
func assertRevokeBlastRadius(t *testing.T, got policy.StrandedReservationException, wantBinId string) {
	t.Helper()
	if got.BlastRadius.BinId != wantBinId {
		t.Fatalf("BlastRadius.BinId = %q, want %q", got.BlastRadius.BinId, wantBinId)
	}
	if got.BlastRadius.QuantityFreed != 12 {
		t.Fatalf("BlastRadius.QuantityFreed = %d, want 12", got.BlastRadius.QuantityFreed)
	}
	if len(got.Evidence) == 0 {
		t.Fatal("expected a non-empty evidence trail on a revoke recommendation")
	}
}
