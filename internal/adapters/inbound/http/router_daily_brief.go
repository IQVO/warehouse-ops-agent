package http

import (
	"net/http"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

type dailyBriefDTO struct {
	GeneratedAt    time.Time          `json:"generatedAt"`
	Sites          []siteBriefDTO     `json:"sites"`
	OpenExceptions []openExceptionDTO `json:"openExceptions"`

	// SlotPlanWatch is present only when slotting-optimization is
	// configured (ADR 0024); absent otherwise, so the brief is unchanged.
	SlotPlanWatch *slotPlanWatchDTO `json:"slotPlanWatch,omitempty"`
	// PromiseHealth is present only when order-management is configured
	// (ADR 0024); absent otherwise, so the brief is unchanged.
	PromiseHealth *promiseHealthDTO `json:"promiseHealth,omitempty"`
}

type siteBriefDTO struct {
	SiteCode string         `json:"siteCode"`
	SiteName string         `json:"siteName"`
	Paths    []pathBriefDTO `json:"paths"`
}

type pathBriefDTO struct {
	PathId      string             `json:"pathId"`
	ProcessPath string             `json:"processPath"`
	Backlog     *backlogFactDTO    `json:"backlog,omitempty"`
	Staffing    *staffingFactDTO   `json:"staffing,omitempty"`
	Queue       *queueFactDTO      `json:"queue,omitempty"`
	Stuck       *stuckTasksFactDTO `json:"stuck,omitempty"`
	Unavailable []string           `json:"unavailable,omitempty"`
	Exceptions  []openExceptionDTO `json:"exceptions,omitempty"`

	// CapacityOutlook is present only when warehouse-planning is configured
	// (ADR 0013); absent otherwise, so the brief is unchanged.
	CapacityOutlook *capacityOutlookDTO `json:"capacityOutlook,omitempty"`
}

type backlogFactDTO struct {
	BacklogDepth       int  `json:"backlogDepth"`
	WIP                int  `json:"wip"`
	OverAlarmThreshold bool `json:"overAlarmThreshold"`
}

type staffingFactDTO struct {
	PlannedHeads int  `json:"plannedHeads"`
	ActiveHeads  int  `json:"activeHeads"`
	Understaffed bool `json:"understaffed"`
}

type queueFactDTO struct {
	Depth int `json:"depth"`
}

type stuckTasksFactDTO struct {
	Count int `json:"count"`
}

type openExceptionDTO struct {
	Kind     string   `json:"kind"`
	SiteCode string   `json:"siteCode"`
	PathId   string   `json:"pathId"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
}

// slotPlanWatchDTO is the optional slotting-optimization section of the
// daily brief (ADR 0024). Stale is empty-but-present when the watch ran and
// found nothing; Unavailable carries the section's explicit degradation
// lines and MUST be read as "incomplete", never "all clear". noApproved
// says slotting-optimization has no Approved plan at all.
type slotPlanWatchDTO struct {
	StaleAfter  time.Duration      `json:"staleAfterNs"`
	Stale       []staleSlotPlanDTO `json:"stale"`
	Checked     int                `json:"checked"`
	NoApproved  bool               `json:"noApproved,omitempty"`
	Unavailable []string           `json:"unavailable,omitempty"`
}

// staleSlotPlanDTO is one flagged plan: id, site, approval instant, age and
// how much of the plan's moves has been executed.
type staleSlotPlanDTO struct {
	PlanId        string    `json:"planId"`
	SiteId        string    `json:"siteId"`
	ApprovedAt    time.Time `json:"approvedAt"`
	AgeSeconds    int64     `json:"ageSeconds"`
	MovesExecuted int       `json:"movesExecuted"`
	MovesTotal    int       `json:"movesTotal"`
}

func toSlotPlanWatchDTO(s *policy.SlotPlanWatchSection) *slotPlanWatchDTO {
	if s == nil {
		return nil
	}
	dto := &slotPlanWatchDTO{
		StaleAfter:  s.StaleAfter,
		Checked:     s.Checked,
		NoApproved:  s.NoApproved,
		Unavailable: s.Unavailable,
	}
	for _, p := range s.Stale {
		dto.Stale = append(dto.Stale, staleSlotPlanDTO{
			PlanId:        p.PlanId,
			SiteId:        p.SiteId,
			ApprovedAt:    p.ApprovedAt,
			AgeSeconds:    int64(p.Age.Seconds()),
			MovesExecuted: p.MovesExecuted,
			MovesTotal:    p.MovesTotal,
		})
	}
	return dto
}

// promiseHealthDTO is the optional order-management promise-health section
// of the daily brief (ADR 0024): the tool's own KPI fields over [from, to).
// Unavailable carries the section's explicit degradation line and MUST be
// read as "unknown", never "all clear".
type promiseHealthDTO struct {
	From                      time.Time `json:"from"`
	To                        time.Time `json:"to"`
	PromiseBasisCapability    int       `json:"promiseBasisCapability"`
	PromiseBasisLeadTime      int       `json:"promiseBasisLeadTime"`
	PromiseBasisNetwork       int       `json:"promiseBasisNetwork"`
	OrdersAllocatedTotal      int       `json:"ordersAllocatedTotal"`
	OrdersSplitShipment       int       `json:"ordersSplitShipment"`
	SplitShipmentRate         float64   `json:"splitShipmentRate"`
	OrdersRepromised          int       `json:"ordersRepromised"`
	RepromiseRate             float64   `json:"repromiseRate"`
	PromiseToCutoffGapSeconds float64   `json:"promiseToCutoffGapSeconds"`
	Unavailable               []string  `json:"unavailable,omitempty"`
}

func toPromiseHealthDTO(s *policy.PromiseHealthSection) *promiseHealthDTO {
	if s == nil {
		return nil
	}
	return &promiseHealthDTO{
		From:                      s.From,
		To:                        s.To,
		PromiseBasisCapability:    s.PromiseBasisCapability,
		PromiseBasisLeadTime:      s.PromiseBasisLeadTime,
		PromiseBasisNetwork:       s.PromiseBasisNetwork,
		OrdersAllocatedTotal:      s.OrdersAllocatedTotal,
		OrdersSplitShipment:       s.OrdersSplitShipment,
		SplitShipmentRate:         s.SplitShipmentRate,
		OrdersRepromised:          s.OrdersRepromised,
		RepromiseRate:             s.RepromiseRate,
		PromiseToCutoffGapSeconds: s.PromiseToCutoffGapSeconds,
		Unavailable:               s.Unavailable,
	}
}

func toDailyBriefDTO(b policy.DailyBrief) dailyBriefDTO {
	dto := dailyBriefDTO{
		GeneratedAt:    b.GeneratedAt,
		OpenExceptions: toOpenExceptionDTOs(b.OpenExceptions),
		SlotPlanWatch:  toSlotPlanWatchDTO(b.SlotPlanWatch),
		PromiseHealth:  toPromiseHealthDTO(b.PromiseHealth),
	}
	for _, s := range b.Sites {
		dto.Sites = append(dto.Sites, toSiteBriefDTO(s))
	}
	return dto
}

func toSiteBriefDTO(s policy.SiteBrief) siteBriefDTO {
	dto := siteBriefDTO{SiteCode: s.SiteCode, SiteName: s.SiteName}
	for _, p := range s.Paths {
		dto.Paths = append(dto.Paths, toPathBriefDTO(p))
	}
	return dto
}

func toPathBriefDTO(p policy.PathBrief) pathBriefDTO {
	dto := pathBriefDTO{
		PathId:      p.Target.PathId,
		ProcessPath: p.Target.ProcessPath,
		Unavailable: p.Unavailable,
		Exceptions:  toOpenExceptionDTOs(p.Exceptions),
	}
	if p.Backlog != nil {
		dto.Backlog = &backlogFactDTO{
			BacklogDepth:       p.Backlog.BacklogDepth,
			WIP:                p.Backlog.WIP,
			OverAlarmThreshold: p.Backlog.OverAlarmThreshold,
		}
	}
	if p.Staffing != nil {
		dto.Staffing = &staffingFactDTO{
			PlannedHeads: p.Staffing.PlannedHeads,
			ActiveHeads:  p.Staffing.ActiveHeads,
			Understaffed: p.Staffing.Understaffed,
		}
	}
	if p.Queue != nil {
		dto.Queue = &queueFactDTO{Depth: p.Queue.Depth}
	}
	if p.Stuck != nil {
		dto.Stuck = &stuckTasksFactDTO{Count: p.Stuck.Count}
	}
	dto.CapacityOutlook = toCapacityOutlookDTO(p.CapacityOutlook)
	return dto
}

// capacityOutlookDTO is the optional warehouse-planning capacity section of
// a path brief (ADR 0013). Either OmittedReason is set (the section could not
// be produced; no capacity figure is present) or the rate/bottleneck fields
// are. NormalizedRate is ORDER per hour.
type capacityOutlookDTO struct {
	PlanningPathId    string            `json:"planningPathId,omitempty"`
	WindowStart       time.Time         `json:"windowStart"`
	WindowEnd         time.Time         `json:"windowEnd"`
	NormalizedRate    *float64          `json:"normalizedRate,omitempty"`
	BottleneckStep    string            `json:"bottleneckStep,omitempty"`
	BindingConstraint string            `json:"bindingConstraint,omitempty"`
	Steps             []capacityStepDTO `json:"steps,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	OmittedReason     string            `json:"omittedReason,omitempty"`
}

type capacityStepDTO struct {
	Step              string  `json:"step"`
	NormalizedRate    float64 `json:"normalizedRate"`
	BindingConstraint string  `json:"bindingConstraint,omitempty"`
}

func toCapacityOutlookDTO(o *policy.CapacityOutlook) *capacityOutlookDTO {
	if o == nil {
		return nil
	}
	dto := &capacityOutlookDTO{
		PlanningPathId: o.PlanningPathId,
		WindowStart:    o.WindowStart,
		WindowEnd:      o.WindowEnd,
		OmittedReason:  o.OmittedReason,
	}
	if o.OmittedReason != "" {
		return dto
	}
	rate := o.NormalizedRate
	dto.NormalizedRate = &rate
	dto.BottleneckStep = o.BottleneckStep
	dto.BindingConstraint = o.BindingConstraint
	dto.Warnings = o.Warnings
	for _, s := range o.Steps {
		dto.Steps = append(dto.Steps, capacityStepDTO{Step: s.Step, NormalizedRate: s.NormalizedRate, BindingConstraint: s.BindingConstraint})
	}
	return dto
}

func toOpenExceptionDTOs(exceptions []policy.OpenException) []openExceptionDTO {
	out := make([]openExceptionDTO, 0, len(exceptions))
	for _, e := range exceptions {
		out = append(out, openExceptionDTO{
			Kind:     string(e.Kind),
			SiteCode: e.SiteCode,
			PathId:   e.PathId,
			Severity: string(e.Severity),
			Summary:  e.Summary,
			Evidence: e.Evidence,
		})
	}
	return out
}

// getDailyBrief handles GET /daily-brief.
func (h *Handlers) getDailyBrief(w http.ResponseWriter, r *http.Request) {
	brief := h.DailyBrief.Execute(r.Context())
	writeJSON(w, toDailyBriefDTO(brief))
}
