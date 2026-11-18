package ports

import (
	"context"
	"time"
)

// DockSchedulingClient is the outbound port for dock-scheduling's published
// READ tools (list_appointments, get_appointment, get_door_availability,
// get_dock_calendar, list_carriers, get_carrier, list_at_risk_appointments).
// dock-scheduling's MCP server also publishes seven L3 write tools
// (book_appointment, reschedule_appointment, cancel_appointment,
// register_carrier, suspend_carrier, reinstate_carrier, report_eta); this
// port deliberately has no method for any of them (ADR 0032, decision
// D-Y4: every agent write stays human-approved). Wired but not yet
// consumed by any use case.
type DockSchedulingClient interface {
	// ListAppointments pages through appointments ordered by window start.
	ListAppointments(ctx context.Context, q DockAppointmentQuery) (DockAppointmentPage, error)
	// GetAppointment reads one appointment by id (appt-<uuid>).
	GetAppointment(ctx context.Context, appointmentID string) (DockAppointment, error)
	// GetDoorAvailability reads the free and busy windows of one door in
	// [from, to). A zero minutes is omitted so dock-scheduling applies the
	// site's slot granularity.
	GetDoorAvailability(ctx context.Context, doorCode string, from, to time.Time, minutes int) (DockDoorAvailability, error)
	// GetDockCalendar reads a site's settings, operating hours and closures.
	GetDockCalendar(ctx context.Context, siteID string) (DockCalendar, error)
	// ListCarriers pages through the carrier registry; an empty status
	// lists every carrier.
	ListCarriers(ctx context.Context, status string, limit int, cursor string) (DockCarrierPage, error)
	// GetCarrier reads one registered carrier by SCAC.
	GetCarrier(ctx context.Context, scac string) (DockCarrier, error)
	// ListAtRiskAppointments pages through BOOKED appointments flagged at
	// risk, optionally narrowed to one site. Advisory only.
	ListAtRiskAppointments(ctx context.Context, siteID string, limit int, cursor string) (DockAppointmentPage, error)
}

// DockAppointmentQuery carries list_appointments' optional filters. A zero
// field is omitted so dock-scheduling applies its own default (no filter;
// 100 per page). AtRisk true lists only at-risk appointments; false means
// no filter, as upstream.
type DockAppointmentQuery struct {
	SiteID      string
	DoorCode    string
	State       string
	Direction   string
	CarrierSCAC string
	AtRisk      bool
	From        time.Time
	To          time.Time
	Limit       int
	Cursor      string
}

// DockArrival mirrors the recorded arrival of an appointment.
type DockArrival struct {
	CheckedInAt *time.Time `json:"checkedInAt"`
	Implicit    bool       `json:"implicit"`
	Status      string     `json:"status"`
	VisitID     *string    `json:"visitId"`
}

// DockETA mirrors the carrier's latest reported ETA.
type DockETA struct {
	ETAAt      time.Time `json:"etaAt"`
	ReportedAt time.Time `json:"reportedAt"`
	Source     string    `json:"source"`
}

// DockAppointment mirrors dock-scheduling's get_appointment result (the
// REST body of GET /appointments/{id}) field-for-field.
type DockAppointment struct {
	AppointmentID      string       `json:"appointmentId"`
	Arrival            *DockArrival `json:"arrival"`
	ASNNumbers         []string     `json:"asnNumbers"`
	AtRisk             bool         `json:"atRisk"`
	AtRiskCause        *string      `json:"atRiskCause"`
	CancellationReason *string      `json:"cancellationReason"`
	Carrier            string       `json:"carrier"`
	CarrierSCAC        *string      `json:"carrierScac"`
	CompletedAt        *time.Time   `json:"completedAt"`
	CPTID              *string      `json:"cptId"`
	CreatedAt          time.Time    `json:"createdAt"`
	Direction          string       `json:"direction"`
	DoorCode           string       `json:"doorCode"`
	ETA                *DockETA     `json:"eta"`
	ExpectedSealNumber *string      `json:"expectedSealNumber"`
	Provenance         string       `json:"provenance"`
	ShipmentRefs       []string     `json:"shipmentRefs"`
	SiteID             string       `json:"siteId"`
	State              string       `json:"state"`
	UpdatedAt          time.Time    `json:"updatedAt"`
	Version            int          `json:"version"`
	WindowEnd          time.Time    `json:"windowEnd"`
	WindowStart        time.Time    `json:"windowStart"`
}

// DockAppointmentPage mirrors one page of list_appointments /
// list_at_risk_appointments; NextCursor is empty on the last page.
type DockAppointmentPage struct {
	Items      []DockAppointment `json:"items"`
	NextCursor string            `json:"nextCursor"`
}

// DockSpan mirrors one free or busy window of a door.
type DockSpan struct {
	End   time.Time `json:"end"`
	Kind  *string   `json:"kind"`
	Ref   *string   `json:"ref"`
	Start time.Time `json:"start"`
}

// DockDoorAvailability mirrors get_door_availability's result.
type DockDoorAvailability struct {
	Busy               []DockSpan `json:"busy"`
	DoorCode           string     `json:"doorCode"`
	FreeWindows        []DockSpan `json:"freeWindows"`
	From               time.Time  `json:"from"`
	GranularityMinutes int        `json:"granularityMinutes"`
	SiteID             string     `json:"siteId"`
	To                 time.Time  `json:"to"`
}

// DockClosure mirrors one door closure of a dock calendar.
type DockClosure struct {
	ClosureID string    `json:"closureId"`
	DoorCode  string    `json:"doorCode"`
	From      time.Time `json:"from"`
	Lifted    bool      `json:"lifted"`
	Reason    string    `json:"reason"`
	SiteID    string    `json:"siteId"`
	To        time.Time `json:"to"`
}

// DockOperatingHours mirrors one declared operating-hours entry; a nil
// DoorCode applies to the whole site.
type DockOperatingHours struct {
	ClosesAt string  `json:"closesAt"`
	DoorCode *string `json:"doorCode"`
	OpensAt  string  `json:"opensAt"`
	Timezone string  `json:"timezone"`
	Weekday  string  `json:"weekday"`
}

// DockCalendarSettings mirrors a site's booking settings.
type DockCalendarSettings struct {
	ArrivalToleranceMinutes int `json:"arrivalToleranceMinutes"`
	MaxWindowMinutes        int `json:"maxWindowMinutes"`
	NoShowGraceMinutes      int `json:"noShowGraceMinutes"`
	SlotGranularityMinutes  int `json:"slotGranularityMinutes"`
}

// DockCalendar mirrors get_dock_calendar's result.
type DockCalendar struct {
	Closures       []DockClosure        `json:"closures"`
	OperatingHours []DockOperatingHours `json:"operatingHours"`
	Settings       DockCalendarSettings `json:"settings"`
	SiteID         string               `json:"siteId"`
	Version        int                  `json:"version"`
}

// DockCarrier mirrors a registered carrier (get_carrier, list_carriers).
type DockCarrier struct {
	CreatedAt        time.Time  `json:"createdAt"`
	MC               *string    `json:"mc"`
	Name             string     `json:"name"`
	SCAC             string     `json:"scac"`
	Status           string     `json:"status"`
	SuspendedAt      *time.Time `json:"suspendedAt"`
	SuspensionReason *string    `json:"suspensionReason"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	USDOT            *string    `json:"usdot"`
	Version          int        `json:"version"`
}

// DockCarrierPage mirrors one page of list_carriers.
type DockCarrierPage struct {
	Items      []DockCarrier `json:"items"`
	NextCursor string        `json:"nextCursor"`
}
