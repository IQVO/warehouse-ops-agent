package ports

import "time"

// The DTOs below mirror yard-management's published output shapes
// field-for-field (the REST bodies), hand-mirrored and never imported from
// the upstream (ADR 0001); the pinned registry golden in
// internal/adapters/outbound/mcpclient/testdata walks every one of them.

// YardLocation is a spot or door reference.
type YardLocation struct {
	Code string `json:"code"`
	Kind string `json:"kind"`
}

// YardSeal is a trailer's seal; Class and Number are nil until recorded.
type YardSeal struct {
	Class  *string `json:"class"`
	Number *string `json:"number"`
	State  string  `json:"state"`
}

// YardTrailer mirrors get_trailer's result and each list_trailers item.
type YardTrailer struct {
	AppointmentID              *string       `json:"appointmentId"`
	ArrivedAt                  time.Time     `json:"arrivedAt"`
	ASNNumbers                 []string      `json:"asnNumbers"`
	CarrierSCAC                *string       `json:"carrierScac"`
	DepartedAt                 *time.Time    `json:"departedAt"`
	DoorWork                   *string       `json:"doorWork"`
	HazmatClass                *string       `json:"hazmatClass"`
	Hold                       *string       `json:"hold"`
	Kind                       string        `json:"kind"`
	LoadState                  string        `json:"loadState"`
	Location                   *YardLocation `json:"location"`
	OpenMoveID                 *string       `json:"openMoveId"`
	OutboundRef                *string       `json:"outboundRef"`
	RequiresTemperatureControl bool          `json:"requiresTemperatureControl"`
	Seal                       YardSeal      `json:"seal"`
	SecuredAt                  *time.Time    `json:"securedAt"`
	SecuredBy                  *string       `json:"securedBy"`
	SecurerRef                 *string       `json:"securerRef"`
	SiteID                     string        `json:"siteId"`
	State                      string        `json:"state"`
	TrailerID                  string        `json:"trailerId"`
	UNNumber                   *string       `json:"unNumber"`
	UnitNumber                 string        `json:"unitNumber"`
	UnitNumberScheme           *string       `json:"unitNumberScheme"`
	Version                    int           `json:"version"`
	VisitID                    *string       `json:"visitId"`
}

// YardTrailerPage mirrors one page of list_trailers; NextCursor is empty on
// the last page.
type YardTrailerPage struct {
	Items      []YardTrailer `json:"items"`
	NextCursor string        `json:"nextCursor"`
}

// YardMovePriority mirrors a move's priority band and optional due time.
type YardMovePriority struct {
	Band  int        `json:"band"`
	DueBy *time.Time `json:"dueBy"`
}

// YardMove mirrors a yard move (list_moves items, get_yard_board openMoves).
type YardMove struct {
	ActualLocation *YardLocation    `json:"actualLocation"`
	AppointmentID  *string          `json:"appointmentId"`
	ClaimedAt      *time.Time       `json:"claimedAt"`
	CompletedAt    *time.Time       `json:"completedAt"`
	From           *YardLocation    `json:"from"`
	HostlerRef     *string          `json:"hostlerRef"`
	LeaseExpiresAt *time.Time       `json:"leaseExpiresAt"`
	MoveID         string           `json:"moveId"`
	Performer      string           `json:"performer"`
	Priority       YardMovePriority `json:"priority"`
	Reason         string           `json:"reason"`
	RequestedAt    time.Time        `json:"requestedAt"`
	SiteID         string           `json:"siteId"`
	State          string           `json:"state"`
	To             YardLocation     `json:"to"`
	TrailerID      string           `json:"trailerId"`
	Version        int              `json:"version"`
}

// YardMovePage mirrors one page of list_moves.
type YardMovePage struct {
	Items      []YardMove `json:"items"`
	NextCursor string     `json:"nextCursor"`
}

// YardBoardSlot is one spot or door of the yard board with the trailer
// parked or docked there, if any.
type YardBoardSlot struct {
	Closed   bool         `json:"closed"`
	Code     string       `json:"code"`
	DockFlow *string      `json:"dockFlow"`
	Kind     string       `json:"kind"`
	Trailer  *YardTrailer `json:"trailer"`
}

// YardBoard mirrors get_yard_board's result.
type YardBoard struct {
	AsOf      time.Time       `json:"asOf"`
	Doors     []YardBoardSlot `json:"doors"`
	InMove    []YardTrailer   `json:"inMove"`
	OpenMoves []YardMove      `json:"openMoves"`
	SiteID    string          `json:"siteId"`
	Spots     []YardBoardSlot `json:"spots"`
}

// YardVisit mirrors get_visit's result and each list_visits item.
type YardVisit struct {
	AppointmentID   *string    `json:"appointmentId"`
	ArrivedAt       time.Time  `json:"arrivedAt"`
	Carrier         string     `json:"carrier"`
	CarrierFlag     *string    `json:"carrierFlag"`
	CarrierSCAC     *string    `json:"carrierScac"`
	DepartedAt      *time.Time `json:"departedAt"`
	DriverRef       *string    `json:"driverRef"`
	Purpose         string     `json:"purpose"`
	RejectionReason *string    `json:"rejectionReason"`
	SiteID          string     `json:"siteId"`
	State           string     `json:"state"`
	TractorPlate    string     `json:"tractorPlate"`
	TrailersIn      []string   `json:"trailersIn"`
	TrailersOut     []string   `json:"trailersOut"`
	Version         int        `json:"version"`
	VisitID         string     `json:"visitId"`
}

// YardVisitPage mirrors one page of list_visits.
type YardVisitPage struct {
	Items      []YardVisit `json:"items"`
	NextCursor string      `json:"nextCursor"`
}

// YardInspectionScope says which trailers a required inspection applies to.
type YardInspectionScope struct {
	Purposes     []string `json:"purposes"`
	TrailerKinds []string `json:"trailerKinds"`
}

// YardRequiredInspection is one inspection a policy requires at a gate.
type YardRequiredInspection struct {
	AppliesTo  YardInspectionScope `json:"appliesTo"`
	Gate       string              `json:"gate"`
	TemplateID string              `json:"templateId"`
}

// YardPolicy mirrors get_yard_policy's result.
type YardPolicy struct {
	AllowHazmatOnUnclassifiedSpots bool                     `json:"allowHazmatOnUnclassifiedSpots"`
	Declared                       bool                     `json:"declared"`
	DwellThresholdMinutes          map[string]int           `json:"dwellThresholdMinutes"`
	FreeTimeMinutes                *int                     `json:"freeTimeMinutes"`
	HoldingSpotCodes               []string                 `json:"holdingSpotCodes"`
	LeaseSeconds                   int                      `json:"leaseSeconds"`
	MinimumSealClass               *string                  `json:"minimumSealClass"`
	ParkingZoneIDs                 []string                 `json:"parkingZoneIds"`
	RequireAppointment             bool                     `json:"requireAppointment"`
	RequireSecuringBeforeDoorWork  bool                     `json:"requireSecuringBeforeDoorWork"`
	RequiredInspections            []YardRequiredInspection `json:"requiredInspections"`
	SealMismatchAction             string                   `json:"sealMismatchAction"`
	SiteID                         string                   `json:"siteId"`
	SuspendedCarrierAction         string                   `json:"suspendedCarrierAction"`
	Version                        int                      `json:"version"`
}

// YardDwellRow is one trailer kind's dwell figures in a report.
type YardDwellRow struct {
	AvgDriverWaitMinutes *float64 `json:"avgDriverWaitMinutes"`
	AvgDwellMinutes      float64  `json:"avgDwellMinutes"`
	AvgProcessingMinutes *float64 `json:"avgProcessingMinutes"`
	Kind                 string   `json:"kind"`
	MaxDwellMinutes      int      `json:"maxDwellMinutes"`
	P50DwellMinutes      *float64 `json:"p50DwellMinutes"`
	P90DwellMinutes      *float64 `json:"p90DwellMinutes"`
	Trailers             int      `json:"trailers"`
}

// YardDwellReport mirrors get_yard_dwell_report's result. Rows are empty
// until yard-management wires its analytics reader; LastEventAt is nil until
// the first event.
type YardDwellReport struct {
	From        time.Time      `json:"from"`
	LastEventAt *time.Time     `json:"lastEventAt"`
	Rows        []YardDwellRow `json:"rows"`
	SiteID      *string        `json:"siteId"`
	To          time.Time      `json:"to"`
}

// YardInspectionItem is one checklist item of an inspection.
type YardInspectionItem struct {
	AllowNotApplicable bool     `json:"allowNotApplicable"`
	EvidenceRefs       []string `json:"evidenceRefs"`
	HoldReason         string   `json:"holdReason"`
	ItemCode           string   `json:"itemCode"`
	Note               *string  `json:"note"`
	Required           bool     `json:"required"`
	Result             *string  `json:"result"`
	Text               string   `json:"text"`
}

// YardInspection mirrors get_inspection's result and each
// list_trailer_inspections item.
type YardInspection struct {
	CompletedAt     *time.Time           `json:"completedAt"`
	ConditionGrade  *string              `json:"conditionGrade"`
	Gate            string               `json:"gate"`
	InspectionID    string               `json:"inspectionId"`
	Items           []YardInspectionItem `json:"items"`
	OpenedAt        time.Time            `json:"openedAt"`
	PerformerRef    *string              `json:"performerRef"`
	SiteID          string               `json:"siteId"`
	Status          string               `json:"status"`
	TemplateID      string               `json:"templateId"`
	TemplateVersion int                  `json:"templateVersion"`
	TrailerID       string               `json:"trailerId"`
	Version         int                  `json:"version"`
	VisitID         *string              `json:"visitId"`
}

// YardInspectionPage mirrors one page of list_trailer_inspections.
type YardInspectionPage struct {
	Items      []YardInspection `json:"items"`
	NextCursor string           `json:"nextCursor"`
}
