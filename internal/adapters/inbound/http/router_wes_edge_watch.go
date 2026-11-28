package http

import (
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// wesEdgeWatchDTO is the optional WES-edge section of the daily brief (ADR
// 0034): the pick-face-replenishment and task-interleaving halves, each
// present only when its upstream is configured. Unavailable carries each
// half's explicit degradation lines and MUST be read as "this half is
// incomplete", never "all clear".
type wesEdgeWatchDTO struct {
	Replenishment *replenishmentWatchDTO `json:"replenishment,omitempty"`
	Interleaving  *interleavingWatchDTO  `json:"interleaving,omitempty"`
}

// replenishmentWatchDTO is the pick-face-replenishment half: the open
// (Requested) requests counted by priority (largest count first), with the
// oldest pending one.
type replenishmentWatchDTO struct {
	Total       int                         `json:"total"`
	Open        []openReplenishmentCountDTO `json:"open"`
	Oldest      *oldestReplenishmentDTO     `json:"oldest,omitempty"`
	Unavailable []string                    `json:"unavailable,omitempty"`
}

// openReplenishmentCountDTO is the number of open requests at one priority.
type openReplenishmentCountDTO struct {
	Priority string `json:"priority"`
	Count    int    `json:"count"`
}

// oldestReplenishmentDTO is the open request pending longest.
type oldestReplenishmentDTO struct {
	RequestID   string    `json:"requestId"`
	SiteID      string    `json:"siteId"`
	SKU         string    `json:"sku"`
	Priority    string    `json:"priority"`
	RequestedAt time.Time `json:"requestedAt"`
	AgeSeconds  int64     `json:"ageSeconds"`
}

// interleavingWatchDTO is the task-interleaving half: the hint counts by
// lifecycle state, Active first. A zero count is a fact; a state whose scan
// failed is absent and named on unavailable.
type interleavingWatchDTO struct {
	Counts      []interleavingHintCountDTO `json:"counts"`
	Unavailable []string                   `json:"unavailable,omitempty"`
}

// interleavingHintCountDTO is the number of hints in one lifecycle state.
type interleavingHintCountDTO struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

func toWesEdgeWatchDTO(s *policy.WesEdgeWatchSection) *wesEdgeWatchDTO {
	if s == nil {
		return nil
	}
	return &wesEdgeWatchDTO{
		Replenishment: toReplenishmentWatchDTO(s.Replenishment),
		Interleaving:  toInterleavingWatchDTO(s.Interleaving),
	}
}

func toReplenishmentWatchDTO(s *policy.ReplenishmentWatchSide) *replenishmentWatchDTO {
	if s == nil {
		return nil
	}
	dto := &replenishmentWatchDTO{
		Total:       s.Total,
		Open:        make([]openReplenishmentCountDTO, 0, len(s.Open)),
		Unavailable: s.Unavailable,
	}
	for _, c := range s.Open {
		dto.Open = append(dto.Open, openReplenishmentCountDTO{Priority: c.Priority, Count: c.Count})
	}
	if s.Oldest != nil {
		dto.Oldest = &oldestReplenishmentDTO{
			RequestID:   s.Oldest.RequestID,
			SiteID:      s.Oldest.SiteID,
			SKU:         s.Oldest.SKU,
			Priority:    s.Oldest.Priority,
			RequestedAt: s.Oldest.RequestedAt,
			AgeSeconds:  int64(s.Oldest.Age.Seconds()),
		}
	}
	return dto
}

func toInterleavingWatchDTO(s *policy.InterleavingWatchSide) *interleavingWatchDTO {
	if s == nil {
		return nil
	}
	dto := &interleavingWatchDTO{
		Counts:      make([]interleavingHintCountDTO, 0, len(s.Counts)),
		Unavailable: s.Unavailable,
	}
	for _, c := range s.Counts {
		dto.Counts = append(dto.Counts, interleavingHintCountDTO{State: c.State, Count: c.Count})
	}
	return dto
}
