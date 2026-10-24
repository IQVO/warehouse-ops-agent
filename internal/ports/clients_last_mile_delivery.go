// Package ports (this file): the outbound port for the last-mile-delivery
// bounded context (retail network), the fleet's owner of delivery-station
// waves, their induction stops and the delivery-service-partner routes
// that discharge them.
//
// last-mile-delivery's MCP server is READ-ONLY (its own governance test
// pins its three tools, every one annotated readOnlyHint). This port
// mirrors all three: list_waves, get_wave_stops and get_route.
// last-mile-delivery's writes (induct packages, open or dispatch a wave)
// are upstream of it and have no method here. The zero-write fitness
// test (internal/architecture/zerowrite) fails the build if the
// mcpclient adapter ever names a write-verb tool, and the contract
// snapshot test in mcpclient pins the published tools against
// last-mile-delivery's published registry.
package ports

import (
	"context"
)

// LastMileDeliveryClient is the outbound port for last-mile-delivery's
// three published read tools.
type LastMileDeliveryClient interface {
	// ListWaves calls list_waves: the delivery waves of one station for
	// one service date, ordered by induct cutoff.
	ListWaves(ctx context.Context, stationNodeID, serviceDate string) (DeliveryWaves, error)
	// GetWaveStops calls get_wave_stops: the packages of one wave grouped
	// by their opaque stop key.
	GetWaveStops(ctx context.Context, waveID string) (WaveStops, error)
	// GetRoute calls get_route: one delivery-service-partner route with
	// its stops and the per-package attempts made on it.
	GetRoute(ctx context.Context, routeID string) (DeliveryRoute, error)
}

// --- last-mile-delivery read-model DTOs --------------------------------------
//
// Field-for-field mirrors of last-mile-delivery's MCP output views
// (internal/adapters/inbound/mcp/tools.go in that repo). Instants stay
// the RFC 3339 strings the server publishes; a consuming use case parses
// them. The contract snapshot test in mcpclient checks every json tag
// below against the published output schemas in
// testdata/last_mile_delivery_tools.golden.json.

// DeliveryWave mirrors waveView (one list_waves item). State is OPEN,
// INDUCTION_CLOSED, DISPATCHED, COMPLETED or CANCELLED.
type DeliveryWave struct {
	WaveID         string `json:"waveId"`
	StationNodeID  string `json:"stationNodeId"`
	ServiceDate    string `json:"serviceDate"`
	InductCutoffAt string `json:"inductCutoffAt"`
	WindowStart    string `json:"windowStart"`
	WindowEnd      string `json:"windowEnd"`
	StopCapacity   int    `json:"stopCapacity"`
	AssignedStops  int    `json:"assignedStops"`
	RemainingStops int    `json:"remainingStops"`
	State          string `json:"state"`
	CancelReason   string `json:"cancelReason,omitempty"`
}

// DeliveryWaves mirrors the list_waves result.
type DeliveryWaves struct {
	Items []DeliveryWave `json:"items"`
}

// WaveStop mirrors one stop of a wave: the keyed-hash stop key, the
// packages inducted under it and (when label data was received) the
// destination postal prefix.
type WaveStop struct {
	StopKey                 string   `json:"stopKey"`
	TrackingNumbers         []string `json:"trackingNumbers"`
	DestinationPostalPrefix string   `json:"destinationPostalPrefix,omitempty"`
}

// WaveStops mirrors the get_wave_stops result.
type WaveStops struct {
	Items []WaveStop `json:"items"`
}

// DeliveryRoute mirrors routeView (get_route). State is PLANNED,
// OUT_FOR_DELIVERY or COMPLETED.
type DeliveryRoute struct {
	RouteID       string         `json:"routeId"`
	WaveID        string         `json:"waveId"`
	DspID         string         `json:"dspId"`
	StationNodeID string         `json:"stationNodeId"`
	State         string         `json:"state"`
	StopCount     int            `json:"stopCount"`
	Stops         []RouteStop    `json:"stops"`
	Attempts      []RouteAttempt `json:"attempts"`
}

// RouteStop mirrors one numbered stop of a route: the packages it
// carries and, once the stop is done, the outcome derived from their
// attempts.
type RouteStop struct {
	StopNo          int      `json:"stopNo"`
	TrackingNumbers []string `json:"trackingNumbers"`
	Outcome         string   `json:"outcome,omitempty"`
}

// RouteAttempt mirrors one delivery attempt of one package on a route.
type RouteAttempt struct {
	RouteID        string `json:"routeId"`
	TrackingNumber string `json:"trackingNumber"`
	Outcome        string `json:"outcome"`
	At             string `json:"at"`
	FailureReason  string `json:"failureReason,omitempty"`
}
