package mcp

import (
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type networkScorecardDTO struct {
	ScorecardID string                     `json:"scorecardId"`
	Window      string                     `json:"window"`
	State       string                     `json:"state"`
	Grade       string                     `json:"grade"`
	Metrics     networkScorecardMetricsDTO `json:"metrics"`
	ComputedAt  string                     `json:"computedAt"`
}

type networkScorecardMetricsDTO struct {
	OnTimeHandoverRate      float64 `json:"onTimeHandoverRate"`
	LateAckRate             float64 `json:"lateAckRate"`
	RejectRate              float64 `json:"rejectRate"`
	ExpiryRate              float64 `json:"expiryRate"`
	PromiseKeptRate         float64 `json:"promiseKeptRate"`
	FeedFreshnessP95Minutes int     `json:"feedFreshnessP95Minutes"`
	Observations            int     `json:"observations"`
}

type networkListingDTO struct {
	ListingID         string `json:"listingId"`
	CatalogItemID     string `json:"catalogItemId"`
	State             string `json:"state"`
	AvailableQty      int    `json:"availableQty"`
	Basis             string `json:"basis"`
	AsOf              string `json:"asOf,omitempty"`
	FeedID            string `json:"feedId,omitempty"`
	SuppressionReason string `json:"suppressionReason,omitempty"`
}

type networkInventoryFeedDTO struct {
	FeedID       string `json:"feedId"`
	NodeID       string `json:"nodeId"`
	FullUpdate   bool   `json:"fullUpdate"`
	AsOf         string `json:"asOf"`
	State        string `json:"state"`
	LineCount    int    `json:"lineCount"`
	AppliedCount int    `json:"appliedCount"`
	StaleCount   int    `json:"staleCount"`
	ZeroedCount  int    `json:"zeroedCount"`
	ReceivedAt   string `json:"receivedAt"`
	AppliedAt    string `json:"appliedAt,omitempty"`
	FailureCode  string `json:"failureCode,omitempty"`
}

type networkVendorNodeDTO struct {
	NodeID              string `json:"nodeId"`
	VendorID            string `json:"vendorId"`
	VendorNodeCode      string `json:"vendorNodeCode"`
	NodeType            string `json:"nodeType"`
	Status              string `json:"status"`
	Subtype             string `json:"subtype,omitempty"`
	RegionID            string `json:"regionId,omitempty"`
	Timezone            string `json:"timezone,omitempty"`
	HandlingTimeMinutes int    `json:"handlingTimeMinutes,omitempty"`
	ProfileRevision     int    `json:"profileRevision,omitempty"`
}

type networkVendorScoreDTO struct {
	Window                  string   `json:"window"`
	Grade                   string   `json:"grade"`
	Observations            int      `json:"observations"`
	ComputedAt              string   `json:"computedAt"`
	OnTimeHandoverRate      *float64 `json:"onTimeHandoverRate,omitempty"`
	LateAckRate             *float64 `json:"lateAckRate,omitempty"`
	RejectRate              *float64 `json:"rejectRate,omitempty"`
	ExpiryRate              *float64 `json:"expiryRate,omitempty"`
	PromiseKeptRate         *float64 `json:"promiseKeptRate,omitempty"`
	FeedFreshnessP95Minutes *float64 `json:"feedFreshnessP95Minutes,omitempty"`
}

type networkDepartureDTO struct {
	DepartureID       string `json:"departureId"`
	LaneID            string `json:"laneId"`
	Kind              string `json:"kind"`
	OriginNodeID      string `json:"originNodeId"`
	DestinationNodeID string `json:"destinationNodeId"`
	CutoffAt          string `json:"cutoffAt"`
	DepartsAt         string `json:"departsAt"`
	ArrivesAt         string `json:"arrivesAt"`
	CapacityPackages  int    `json:"capacityPackages"`
	BookedPackages    int    `json:"bookedPackages"`
	RemainingPackages int    `json:"remainingPackages"`
	State             string `json:"state"`
	DepartedAt        string `json:"departedAt,omitempty"`
	ArrivedAt         string `json:"arrivedAt,omitempty"`
	CancelledAt       string `json:"cancelledAt,omitempty"`
	CancelReason      string `json:"cancelReason,omitempty"`
}

type networkWaveDTO struct {
	WaveID         string `json:"waveId"`
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

func toNetworkScorecardDTOs(scorecards []ports.NodeScorecard) []networkScorecardDTO {
	out := make([]networkScorecardDTO, 0, len(scorecards))
	for _, s := range scorecards {
		out = append(out, networkScorecardDTO{
			ScorecardID: s.ScorecardID, Window: s.Window, State: s.State, Grade: s.Grade,
			Metrics: networkScorecardMetricsDTO{
				OnTimeHandoverRate:      s.Metrics.OnTimeHandoverRate,
				LateAckRate:             s.Metrics.LateAckRate,
				RejectRate:              s.Metrics.RejectRate,
				ExpiryRate:              s.Metrics.ExpiryRate,
				PromiseKeptRate:         s.Metrics.PromiseKeptRate,
				FeedFreshnessP95Minutes: s.Metrics.FeedFreshnessP95Minutes,
				Observations:            s.Metrics.Observations,
			},
			ComputedAt: s.ComputedAt,
		})
	}
	return out
}

func toNetworkListingDTOs(listings []ports.Listing) []networkListingDTO {
	out := make([]networkListingDTO, 0, len(listings))
	for _, l := range listings {
		out = append(out, networkListingDTO{
			ListingID: l.ListingID, CatalogItemID: l.CatalogItemID, State: l.State,
			AvailableQty:      l.Availability.AvailableQty,
			Basis:             l.Availability.Basis,
			AsOf:              l.Availability.AsOf,
			FeedID:            l.Availability.FeedID,
			SuppressionReason: l.SuppressionReason,
		})
	}
	return out
}

func toNetworkInventoryFeedDTOs(feeds []ports.InventoryFeed) []networkInventoryFeedDTO {
	out := make([]networkInventoryFeedDTO, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, networkInventoryFeedDTO{
			FeedID: f.FeedID, NodeID: f.NodeID, FullUpdate: f.FullUpdate, AsOf: f.AsOf,
			State: f.State, LineCount: f.LineCount, AppliedCount: f.AppliedCount,
			StaleCount: f.StaleCount, ZeroedCount: f.ZeroedCount, ReceivedAt: f.ReceivedAt,
			AppliedAt: f.AppliedAt, FailureCode: f.FailureCode,
		})
	}
	return out
}

func toNetworkVendorNodeDTOs(vendors []ports.VendorNode) []networkVendorNodeDTO {
	out := make([]networkVendorNodeDTO, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, networkVendorNodeDTO{
			NodeID: v.NodeID, VendorID: v.VendorID, VendorNodeCode: v.VendorNodeCode,
			NodeType: v.NodeType, Status: v.Status, Subtype: v.Subtype, RegionID: v.RegionID,
			Timezone: v.Timezone, HandlingTimeMinutes: v.HandlingTimeMinutes, ProfileRevision: v.ProfileRevision,
		})
	}
	return out
}

func toNetworkVendorScoreDTOs(windows []ports.VendorScorecardWindow) []networkVendorScoreDTO {
	out := make([]networkVendorScoreDTO, 0, len(windows))
	for _, w := range windows {
		out = append(out, networkVendorScoreDTO{
			Window: w.Window, Grade: w.Grade, Observations: w.Observations, ComputedAt: w.ComputedAt,
			OnTimeHandoverRate:      w.OnTimeHandoverRate,
			LateAckRate:             w.LateAckRate,
			RejectRate:              w.RejectRate,
			ExpiryRate:              w.ExpiryRate,
			PromiseKeptRate:         w.PromiseKeptRate,
			FeedFreshnessP95Minutes: w.FeedFreshnessP95Minutes,
		})
	}
	return out
}

func toNetworkDepartureDTOs(departures []ports.LinehaulDeparture) []networkDepartureDTO {
	out := make([]networkDepartureDTO, 0, len(departures))
	for _, dep := range departures {
		out = append(out, networkDepartureDTO{
			DepartureID: dep.DepartureID, LaneID: dep.LaneID, Kind: dep.Kind,
			OriginNodeID: dep.OriginNodeID, DestinationNodeID: dep.DestinationNodeID,
			CutoffAt: dep.CutoffAt, DepartsAt: dep.DepartsAt, ArrivesAt: dep.ArrivesAt,
			CapacityPackages: dep.CapacityPackages, BookedPackages: dep.BookedPackages,
			RemainingPackages: dep.RemainingPackages, State: dep.State,
			DepartedAt: dep.DepartedAt, ArrivedAt: dep.ArrivedAt,
			CancelledAt: dep.CancelledAt, CancelReason: dep.CancelReason,
		})
	}
	return out
}

func toNetworkWaveDTOs(waves []ports.DeliveryWave) []networkWaveDTO {
	out := make([]networkWaveDTO, 0, len(waves))
	for _, w := range waves {
		out = append(out, networkWaveDTO{
			WaveID: w.WaveID, ServiceDate: w.ServiceDate, InductCutoffAt: w.InductCutoffAt,
			WindowStart: w.WindowStart, WindowEnd: w.WindowEnd,
			StopCapacity: w.StopCapacity, AssignedStops: w.AssignedStops, RemainingStops: w.RemainingStops,
			State: w.State, CancelReason: w.CancelReason,
		})
	}
	return out
}
