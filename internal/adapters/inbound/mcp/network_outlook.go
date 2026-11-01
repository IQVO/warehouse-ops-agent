package mcp

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// --- get_network_order_outlook (ADR 0026) ------------------------------------
//
// The order outlook's DTOs carry the decision-relevant projection of each
// upstream's facts. Where an upstream publishes more detail than an
// outlook needs (a promise's revisions and witness path, a journey's full
// scan trail) the summary is carried and the drill-down stays behind that
// upstream's own tool — the outlook is one picture, not a data dump.

type networkOrderOutlookInput struct {
	OrderID string `json:"orderId" jsonschema:"the customer order id to assemble the retail-network picture for"`
}

type networkOrderOutlookOutput struct {
	OrderID     string `json:"orderId"`
	GeneratedAt string `json:"generatedAt"`

	Order    inboundSection[networkOrderDTO]      `json:"order"`
	Tracking inboundSection[networkTrackingDTO]   `json:"tracking"`
	ShipTo   inboundSection[networkShipToDTO]     `json:"shipTo"`
	Promises inboundSection[networkPromiseDTO]    `json:"promises"`
	Sourcing inboundSection[networkAssignmentDTO] `json:"sourcing"`
	Journeys inboundSection[networkJourneyDTO]    `json:"journeys"`
}

type networkOrderDTO struct {
	OrderID                 string                 `json:"orderId"`
	Status                  string                 `json:"status"`
	ShopperID               string                 `json:"shopperId"`
	DestinationPostalPrefix string                 `json:"destinationPostalPrefix"`
	PlacedAt                string                 `json:"placedAt"`
	CancelledAt             string                 `json:"cancelledAt,omitempty"`
	Lines                   []networkOrderLineDTO  `json:"lines"`
	Groups                  []networkOrderGroupDTO `json:"groups"`
}

type networkOrderLineDTO struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type networkOrderGroupDTO struct {
	GroupNo           int    `json:"groupNo"`
	PromiseID         string `json:"promiseId"`
	Tier              string `json:"tier"`
	ShipBy            string `json:"shipBy"`
	DeliverByEarliest string `json:"deliverByEarliest"`
	DeliverByLatest   string `json:"deliverByLatest"`
	LineNos           []int  `json:"lineNos"`
	Status            string `json:"status"`
	CancelReason      string `json:"cancelReason,omitempty"`
}

type networkTrackingDTO struct {
	OrderID string                   `json:"orderId"`
	Status  string                   `json:"status"`
	Groups  []networkTrackedGroupDTO `json:"groups"`
}

type networkTrackedGroupDTO struct {
	GroupNo            int                        `json:"groupNo"`
	PromiseID          string                     `json:"promiseId"`
	Status             string                     `json:"status"`
	PromiseVerdict     string                     `json:"promiseVerdict,omitempty"`
	AtRiskReason       string                     `json:"atRiskReason,omitempty"`
	LateByMinutes      *int                       `json:"lateByMinutes,omitempty"`
	ShippedAt          string                     `json:"shippedAt,omitempty"`
	LostAt             string                     `json:"lostAt,omitempty"`
	ReturnedAt         string                     `json:"returnedAt,omitempty"`
	OutcomePackage     string                     `json:"outcomePackage,omitempty"`
	UnsourceableReason string                     `json:"unsourceableReason,omitempty"`
	Packages           []networkTrackedPackageDTO `json:"packages"`
}

type networkTrackedPackageDTO struct {
	TrackingNumber  string `json:"trackingNumber"`
	State           string `json:"state,omitempty"`
	Carrier         string `json:"carrier,omitempty"`
	LastScanAt      string `json:"lastScanAt,omitempty"`
	LastScanNodeID  string `json:"lastScanNodeId,omitempty"`
	ExceptionReason string `json:"exceptionReason,omitempty"`
	DeliveredAt     string `json:"deliveredAt,omitempty"`
}

type networkShipToDTO struct {
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}

type networkPromiseDTO struct {
	PromiseID         string                      `json:"promiseId"`
	GroupNo           int                         `json:"groupNo"`
	State             string                      `json:"state"`
	Tier              string                      `json:"tier"`
	ShipBy            string                      `json:"shipBy"`
	DeliverByEarliest string                      `json:"deliverByEarliest"`
	DeliverByLatest   string                      `json:"deliverByLatest"`
	AtRiskReason      string                      `json:"atRiskReason,omitempty"`
	Judgement         *networkPromiseJudgementDTO `json:"judgement,omitempty"`
	Version           int                         `json:"version"`
}

type networkPromiseJudgementDTO struct {
	Outcome       string `json:"outcome"`
	Cause         string `json:"cause,omitempty"`
	DeliveredAt   string `json:"deliveredAt,omitempty"`
	LateByMinutes *int   `json:"lateByMinutes,omitempty"`
}

type networkAssignmentDTO struct {
	AssignmentID      string   `json:"assignmentId"`
	GroupNo           int      `json:"groupNo"`
	State             string   `json:"state"`
	CandidateNodeID   string   `json:"candidateNodeId,omitempty"`
	PickupDepartureID string   `json:"pickupDepartureId,omitempty"`
	PickupCutoffAt    string   `json:"pickupCutoffAt,omitempty"`
	ETA               string   `json:"eta,omitempty"`
	TrackingNumbers   []string `json:"trackingNumbers"`
	LockReason        string   `json:"lockReason,omitempty"`
	CancelReason      string   `json:"cancelReason,omitempty"`
}

type networkJourneyDTO struct {
	TrackingNumber string `json:"trackingNumber"`
	State          string `json:"state,omitempty"`
	PoNumber       string `json:"poNumber,omitempty"`
	AssignmentID   string `json:"assignmentId,omitempty"`
	Tier           string `json:"tier,omitempty"`
	HandedOverAt   string `json:"handedOverAt,omitempty"`
	DeliveredAt    string `json:"deliveredAt,omitempty"`
	ScanCount      int    `json:"scanCount,omitempty"`
	LastScanAt     string `json:"lastScanAt,omitempty"`
	LastScanNodeID string `json:"lastScanNodeId,omitempty"`
	// Error is non-empty when this journey could not be read: unknown,
	// never "delivered".
	Error string `json:"error,omitempty"`
}

func (d Deps) getNetworkOrderOutlook(ctx context.Context, in networkOrderOutlookInput) (networkOrderOutlookOutput, error) {
	res, err := d.NetworkOrderOutlook.Execute(ctx, in.OrderID)
	if err != nil {
		return networkOrderOutlookOutput{}, err
	}
	out := networkOrderOutlookOutput{
		OrderID:     res.OrderID,
		GeneratedAt: inboundInstant(res.GeneratedAt),
	}

	orders := make([]networkOrderDTO, 0, len(res.Order.Items))
	for _, o := range res.Order.Items {
		orders = append(orders, toNetworkOrderDTO(o))
	}
	out.Order = newNetworkSection(res.Order.OutlookSectionStatus, orders)

	tracking := make([]networkTrackingDTO, 0, len(res.Tracking.Items))
	for _, t := range res.Tracking.Items {
		tracking = append(tracking, toNetworkTrackingDTO(t))
	}
	out.Tracking = newNetworkSection(res.Tracking.OutlookSectionStatus, tracking)

	shipTo := make([]networkShipToDTO, 0, len(res.ShipTo.Items))
	for _, s := range res.ShipTo.Items {
		shipTo = append(shipTo, networkShipToDTO{
			Name: s.Name, Line1: s.Line1, Line2: s.Line2, City: s.City,
			Region: s.Region, PostalCode: s.PostalCode, CountryCode: s.CountryCode, Phone: s.Phone,
		})
	}
	out.ShipTo = newNetworkSection(res.ShipTo.OutlookSectionStatus, shipTo)

	promises := make([]networkPromiseDTO, 0, len(res.Promises.Items))
	for _, p := range res.Promises.Items {
		promises = append(promises, toNetworkPromiseDTO(p))
	}
	out.Promises = newNetworkSection(res.Promises.OutlookSectionStatus, promises)

	sourcing := make([]networkAssignmentDTO, 0, len(res.Sourcing.Items))
	for _, a := range res.Sourcing.Items {
		sourcing = append(sourcing, toNetworkAssignmentDTO(a))
	}
	out.Sourcing = newNetworkSection(res.Sourcing.OutlookSectionStatus, sourcing)

	journeys := make([]networkJourneyDTO, 0, len(res.Journeys.Items))
	for _, j := range res.Journeys.Items {
		journeys = append(journeys, networkJourneyDTO{
			TrackingNumber: j.TrackingNumber, State: j.State, PoNumber: j.PoNumber,
			AssignmentID: j.AssignmentID, Tier: j.Tier,
			HandedOverAt: j.HandedOverAt, DeliveredAt: j.DeliveredAt,
			ScanCount: j.ScanCount, LastScanAt: j.LastScanAt, LastScanNodeID: j.LastScanNodeID,
			Error: j.Error,
		})
	}
	out.Journeys = newNetworkSection(res.Journeys.OutlookSectionStatus, journeys)
	return out, nil
}

func toNetworkOrderDTO(o ports.CustomerOrder) networkOrderDTO {
	lines := make([]networkOrderLineDTO, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, networkOrderLineDTO{LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	groups := make([]networkOrderGroupDTO, 0, len(o.Groups))
	for _, g := range o.Groups {
		lineNos := g.LineNos
		if lineNos == nil {
			lineNos = []int{}
		}
		groups = append(groups, networkOrderGroupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier,
			ShipBy: g.ShipBy, DeliverByEarliest: g.DeliverByEarliest, DeliverByLatest: g.DeliverByLatest,
			LineNos: lineNos, Status: g.Status, CancelReason: g.CancelReason,
		})
	}
	return networkOrderDTO{
		OrderID: o.OrderID, Status: o.Status, ShopperID: o.ShopperID,
		DestinationPostalPrefix: o.DestinationPostalPrefix, PlacedAt: o.PlacedAt, CancelledAt: o.CancelledAt,
		Lines: lines, Groups: groups,
	}
}

func toNetworkTrackingDTO(t ports.OrderTracking) networkTrackingDTO {
	groups := make([]networkTrackedGroupDTO, 0, len(t.Groups))
	for _, g := range t.Groups {
		packages := make([]networkTrackedPackageDTO, 0, len(g.Packages))
		for _, p := range g.Packages {
			packages = append(packages, networkTrackedPackageDTO{
				TrackingNumber: p.TrackingNumber, State: p.State, Carrier: p.Carrier,
				LastScanAt: p.LastScanAt, LastScanNodeID: p.LastScanNodeID,
				ExceptionReason: p.ExceptionReason, DeliveredAt: p.DeliveredAt,
			})
		}
		groups = append(groups, networkTrackedGroupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Status: g.Status,
			PromiseVerdict: g.PromiseVerdict, AtRiskReason: g.AtRiskReason, LateByMinutes: g.LateByMinutes,
			ShippedAt: g.ShippedAt, LostAt: g.LostAt, ReturnedAt: g.ReturnedAt,
			OutcomePackage: g.OutcomePackage, UnsourceableReason: g.UnsourceableReason,
			Packages: packages,
		})
	}
	return networkTrackingDTO{OrderID: t.OrderID, Status: t.Status, Groups: groups}
}

func toNetworkPromiseDTO(p ports.DeliveryPromise) networkPromiseDTO {
	var judgement *networkPromiseJudgementDTO
	if p.Judgement != nil {
		judgement = &networkPromiseJudgementDTO{
			Outcome: p.Judgement.Outcome, Cause: p.Judgement.Cause,
			DeliveredAt: p.Judgement.DeliveredAt, LateByMinutes: p.Judgement.LateByMinutes,
		}
	}
	return networkPromiseDTO{
		PromiseID: p.PromiseID, GroupNo: p.GroupNo, State: p.State,
		Tier: p.Terms.Tier, ShipBy: p.Terms.ShipBy,
		DeliverByEarliest: p.Terms.DeliverByEarliest, DeliverByLatest: p.Terms.DeliverByLatest,
		AtRiskReason: p.AtRiskReason, Judgement: judgement, Version: p.Version,
	}
}

func toNetworkAssignmentDTO(a ports.FulfillmentAssignment) networkAssignmentDTO {
	trackingNumbers := a.TrackingNumbers
	if trackingNumbers == nil {
		trackingNumbers = []string{}
	}
	dto := networkAssignmentDTO{
		AssignmentID: a.AssignmentID, GroupNo: a.GroupNo, State: a.State,
		PickupDepartureID: a.PickupDepartureID, TrackingNumbers: trackingNumbers,
		LockReason: a.LockReason, CancelReason: a.CancelReason,
	}
	if a.Candidate != nil {
		dto.CandidateNodeID = a.Candidate.NodeID
		dto.PickupCutoffAt = a.Candidate.PickupCutoffAt
		dto.ETA = a.Candidate.ETA
		if a.Candidate.PickupDepartureID != "" {
			dto.PickupDepartureID = a.Candidate.PickupDepartureID
		}
	}
	return dto
}

// newNetworkSection maps the outlook envelope onto the wire section the
// inbound tools already publish (same fields, same meaning as
// newInboundSection in inbound_outlook.go).
func newNetworkSection[T any](status usecases.OutlookSectionStatus, items []T) inboundSection[T] {
	return inboundSection[T]{Omitted: status.Omitted, Complete: status.Complete, Count: len(items), Items: items}
}
