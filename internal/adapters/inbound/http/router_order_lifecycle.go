package http

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// getOrderLifecycle handles GET /console/orders/{id}/lifecycle. It never
// panics on an unwired OrderLifecycle (503, matching
// getFlowBalanceException's convention), and an order-management 404
// (the order genuinely doesn't exist) is reported as 404 rather than a
// generic 500. Everything else, per usecases.OrderLifecycle.Execute's
// contract and ADR 0002, degrades that one stage to null in a 200
// response -- Execute's only error return is ports.ErrNotFound, so there
// is deliberately no upstream-failure (502) branch here; surfacing
// upstream failures as 502 would contradict the partial-tolerant design.
func (h *Handlers) getOrderLifecycle(w http.ResponseWriter, r *http.Request) {
	if h.OrderLifecycle == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "order lifecycle not configured")
		return
	}
	orderId := chi.URLParam(r, "id")

	result, err := h.OrderLifecycle.Execute(r.Context(), orderId)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeProblem(w, r, http.StatusNotFound, "order not found")
			return
		}
		// Defensive only: Execute does not return any other error today.
		// If that contract ever widens, fail loudly as an internal error
		// rather than mislabelling it an upstream (502) failure.
		writeProblem(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, toOrderLifecycleDTO(result))
}

// orderLifecycleDTO's field names/shapes are hand-kept in sync with
// warehouse-console's src/features/order-lifecycle/types.ts OrderLifecycle
// interface -- the console is this endpoint's only consumer today.
type orderLifecycleDTO struct {
	OrderId         string               `json:"orderId"`
	OrderManagement *orderStageDTO       `json:"orderManagement"`
	Inventory       *inventoryStageDTO   `json:"inventory"`
	Planning        *planningStageDTO    `json:"planning"`
	Fulfillment     *fulfillmentStageDTO `json:"fulfillment"`
}

type orderStageDTO struct {
	Status               string         `json:"status"`
	AllowPartialShipment bool           `json:"allowPartialShipment"`
	PromiseDate          *string        `json:"promiseDate"`
	Lines                []orderLineDTO `json:"lines"`
	ReceivedAt           *string        `json:"receivedAt"`
}

type orderLineDTO struct {
	LineNo   int    `json:"lineNo"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

type inventoryStageDTO struct {
	Reservations []reservationLineDTO `json:"reservations"`
}

type reservationLineDTO struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

type planningStageDTO struct {
	WorkUnits []workUnitLineDTO `json:"workUnits"`
}

type workUnitLineDTO struct {
	WorkUnitId string `json:"workUnitId"`
	PathId     string `json:"pathId"`
	Status     string `json:"status"`
	Fragile    bool   `json:"fragile"`
	GiftWrap   bool   `json:"giftWrap"`
}

type fulfillmentStageDTO struct {
	Tasks         []taskLineDTO `json:"tasks"`
	PackageSealed bool          `json:"packageSealed"`
	LabelApplied  bool          `json:"labelApplied"`
}

type taskLineDTO struct {
	TaskId            string  `json:"taskId"`
	TaskType          string  `json:"taskType"`
	Status            string  `json:"status"`
	StationId         *string `json:"stationId"`
	WeightDiscrepancy bool    `json:"weightDiscrepancy"`
	LeaseExpiredCount int     `json:"leaseExpiredCount"`
}

func toOrderLifecycleDTO(r usecases.OrderLifecycleResult) orderLifecycleDTO {
	dto := orderLifecycleDTO{OrderId: r.OrderId}

	if r.OrderManagement != nil {
		lines := make([]orderLineDTO, 0, len(r.OrderManagement.Lines))
		for _, l := range r.OrderManagement.Lines {
			lines = append(lines, orderLineDTO{
				LineNo:   l.LineNo,
				SKU:      l.SKU,
				Quantity: l.Quantity,
				Status:   l.Status,
			})
		}
		dto.OrderManagement = &orderStageDTO{
			Status:               r.OrderManagement.Status,
			AllowPartialShipment: r.OrderManagement.AllowPartialShipment,
			PromiseDate:          r.OrderManagement.PromiseDate,
			Lines:                lines,
		}
	}

	if r.Inventory != nil {
		reservations := make([]reservationLineDTO, 0, len(r.Inventory))
		for _, res := range r.Inventory {
			reservations = append(reservations, reservationLineDTO{
				SKU:      res.SKU,
				Quantity: res.Quantity,
				Status:   res.Status,
			})
		}
		dto.Inventory = &inventoryStageDTO{Reservations: reservations}
	}

	if r.Planning != nil {
		units := make([]workUnitLineDTO, 0, len(r.Planning))
		for _, wu := range r.Planning {
			units = append(units, workUnitLineDTO{
				WorkUnitId: wu.Id,
				PathId:     wu.PathId,
				Status:     wu.State,
				Fragile:    false, // wes's WorkUnitDTO doesn't carry a fragile flag today; see PR notes.
				GiftWrap:   wu.GiftWrap,
			})
		}
		dto.Planning = &planningStageDTO{WorkUnits: units}
	}

	if r.Fulfillment != nil {
		tasks := make([]taskLineDTO, 0, len(r.Fulfillment))
		sealed := false
		for _, t := range r.Fulfillment {
			tasks = append(tasks, taskLineDTO{
				TaskId:    t.Id,
				TaskType:  t.Type,
				Status:    t.Status,
				StationId: t.LeaseStationId,
			})
			if t.Type == "SLAM" && t.Status == "COMPLETED" {
				sealed = true
			}
		}
		dto.Fulfillment = &fulfillmentStageDTO{
			Tasks:         tasks,
			PackageSealed: sealed,
			LabelApplied:  sealed,
		}
	}

	return dto
}
