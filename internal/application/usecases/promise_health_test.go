package usecases_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func TestPromiseHealth_NotConfigured_ReturnsNilSection(t *testing.T) {
	var uc *usecases.PromiseHealth
	if got := uc.Execute(context.Background(), slotWatchNow()); got != nil {
		t.Fatalf("a nil use case must produce no section, got %+v", got)
	}
	if got := (&usecases.PromiseHealth{}).Execute(context.Background(), slotWatchNow()); got != nil {
		t.Fatalf("a nil order-management port must produce no section, got %+v", got)
	}
}

func TestPromiseHealth_SurfacesTheToolsKPIsForTodaySoFar(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	om := &fakeOrderMgmtMCP{health: ports.PromiseHealth{
		PromiseBasisCapability:    70,
		PromiseBasisLeadTime:      25,
		PromiseBasisNetwork:       5,
		OrdersAllocatedTotal:      100,
		OrdersSplitShipment:       10,
		SplitShipmentRate:         0.1,
		OrdersRepromised:          4,
		RepromiseRate:             0.04,
		PromiseToCutoffGapSeconds: 5400,
	}}
	uc := &usecases.PromiseHealth{OrderMgmt: om}

	section := uc.Execute(context.Background(), now)
	if section == nil {
		t.Fatal("a configured promise-health use case must produce a section")
	}
	// The window must be today so far: [start of the calendar day, now).
	if !om.query.From.Equal(time.Date(2026, 10, 17, 0, 0, 0, 0, now.Location())) {
		t.Fatalf("query from = %v, want the start of today", om.query.From)
	}
	if !om.query.To.Equal(now) || om.query.PathId != "" {
		t.Fatalf("query to = %v pathId = %q, want now with no path filter", om.query.To, om.query.PathId)
	}
	if !section.From.Equal(om.query.From) || !section.To.Equal(now) {
		t.Fatalf("section must echo the window it asked for: %+v", section)
	}
	if section.PromiseBasisCapability != 70 || section.OrdersAllocatedTotal != 100 ||
		section.SplitShipmentRate != 0.1 || section.RepromiseRate != 0.04 || section.PromiseToCutoffGapSeconds != 5400 {
		t.Fatalf("the section must carry exactly what the tool returned: %+v", section)
	}
	if len(section.Unavailable) != 0 {
		t.Fatalf("a healthy read carries no unavailable lines, got %v", section.Unavailable)
	}
}

func TestPromiseHealth_UnreachableUpstreamDegradesToUnavailableLine(t *testing.T) {
	om := &fakeOrderMgmtMCP{err: errors.New("connect: connection refused")}
	uc := &usecases.PromiseHealth{OrderMgmt: om}

	section := uc.Execute(context.Background(), slotWatchNow())
	if section == nil {
		t.Fatal("an unreachable upstream must still produce the section")
	}
	if len(section.Unavailable) != 1 || !strings.Contains(section.Unavailable[0], "get_promise_health") || !strings.Contains(section.Unavailable[0], "connection refused") {
		t.Fatalf("an unreachable order-management must surface as exactly one explicit unavailable line, got %v", section.Unavailable)
	}
	if section.OrdersAllocatedTotal != 0 || section.RepromiseRate != 0 {
		t.Fatalf("no KPIs can be reported when the call failed, got %+v", section)
	}
}
