package restclient

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// OrderManagement implements ports.OrderManagementClient by calling
// order-management's GET /orders/{id}.
type OrderManagement struct {
	baseURL string
	client  *http.Client
}

func NewOrderManagement(baseURL string, timeout time.Duration) *OrderManagement {
	return &OrderManagement{baseURL: baseURL, client: newHTTPClient(timeout)}
}

var _ ports.OrderManagementClient = (*OrderManagement)(nil)

func (c *OrderManagement) GetOrder(ctx context.Context, orderId string) (*ports.OrderDTO, error) {
	var out ports.OrderDTO
	if err := httpGetJSON(ctx, c.client, c.baseURL, "/orders/"+url.PathEscape(orderId), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InventoryReservations implements ports.InventoryReservationsClient by
// calling inventory-storage's GET /reservations?demandRef=.
type InventoryReservations struct {
	baseURL string
	client  *http.Client
}

func NewInventoryReservations(baseURL string, timeout time.Duration) *InventoryReservations {
	return &InventoryReservations{baseURL: baseURL, client: newHTTPClient(timeout)}
}

var _ ports.InventoryReservationsClient = (*InventoryReservations)(nil)

func (c *InventoryReservations) GetReservationsByDemandRef(ctx context.Context, demandRef string) ([]ports.ReservationDTO, error) {
	q := url.Values{"demandRef": {demandRef}}
	return listAll[ports.ReservationDTO](ctx, c.client, c.baseURL, "/reservations", q)
}

// WorkUnits implements ports.WorkUnitsClient by calling
// wes-work-planning's GET /work-units?reference=.
type WorkUnits struct {
	baseURL string
	client  *http.Client
}

func NewWorkUnits(baseURL string, timeout time.Duration) *WorkUnits {
	return &WorkUnits{baseURL: baseURL, client: newHTTPClient(timeout)}
}

var _ ports.WorkUnitsClient = (*WorkUnits)(nil)

func (c *WorkUnits) GetWorkUnitsByReference(ctx context.Context, reference string) ([]ports.WorkUnitDTO, error) {
	q := url.Values{"reference": {reference}}
	return listAll[ports.WorkUnitDTO](ctx, c.client, c.baseURL, "/work-units", q)
}

// TasksByOrder implements ports.TasksByOrderClient by calling
// fulfillment-execution's GET /tasks?orderRef=.
type TasksByOrder struct {
	baseURL string
	client  *http.Client
}

func NewTasksByOrder(baseURL string, timeout time.Duration) *TasksByOrder {
	return &TasksByOrder{baseURL: baseURL, client: newHTTPClient(timeout)}
}

var _ ports.TasksByOrderClient = (*TasksByOrder)(nil)

func (c *TasksByOrder) GetTasksByOrderRef(ctx context.Context, orderRef string) ([]ports.TaskDTO, error) {
	q := url.Values{"orderRef": {orderRef}}
	return listAll[ports.TaskDTO](ctx, c.client, c.baseURL, "/tasks", q)
}
