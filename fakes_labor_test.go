package main_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Fakes for the labor-reallocation act slice (ADR 0029): workforce-management's
// site-scoped staffing reads and the in-memory stand-in for its assign_labor
// A2A skill.

// fakeLaborStaffing answers get_staffing_gap by path.
type fakeLaborStaffing struct {
	gaps map[string]ports.StaffingGap
}

func newFakeLaborStaffing() *fakeLaborStaffing {
	return &fakeLaborStaffing{gaps: map[string]ports.StaffingGap{}}
}

func (f *fakeLaborStaffing) set(path string, planned, active int) {
	f.gaps[path] = ports.StaffingGap{PathId: path, PlannedHeads: planned, ActiveHeads: active, Understaffed: active < planned}
}

func (f *fakeLaborStaffing) GetStaffingGapForSite(_ context.Context, _, _, pathId string) (ports.StaffingGap, error) {
	g, ok := f.gaps[pathId]
	if !ok {
		return ports.StaffingGap{}, fmt.Errorf("workforce-management reported no staffing for %q", pathId)
	}
	return g, nil
}

// fakeLaborPeer is the stand-in for workforce-management's assign_labor skill.
type fakeLaborPeer struct {
	calls []ports.AssignLaborRequest
	err   error
}

func (f *fakeLaborPeer) AssignLabor(_ context.Context, req ports.AssignLaborRequest) (ports.LaborAssignment, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return ports.LaborAssignment{}, f.err
	}
	return ports.LaborAssignment{AssociateId: req.AssociateId, PathId: req.PathId, AssignmentId: req.AssociateId + "@" + req.PathId}, nil
}

var (
	errLaborRule     = fmt.Errorf("%w: associate lacks the required certification", ports.ErrAssignmentRejected)
	errLaborNotFound = fmt.Errorf("%w: associate not found", ports.ErrAssignmentNotFound)
	errLaborDown     = errors.Join(ports.ErrWorkforceAgentUnreachable, errors.New("connection refused"))
)
