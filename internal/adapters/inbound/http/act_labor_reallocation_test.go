package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type reallocStaffing struct{ gaps map[string]ports.StaffingGap }

func (s *reallocStaffing) GetStaffingGapForSite(_ context.Context, _, _, pathId string) (ports.StaffingGap, error) {
	g, ok := s.gaps[pathId]
	if !ok {
		return ports.StaffingGap{}, errors.New("no such path")
	}
	return g, nil
}

type reallocAssigner struct {
	calls atomic.Int32
	last  ports.AssignLaborRequest
	err   error
}

func (a *reallocAssigner) AssignLabor(_ context.Context, req ports.AssignLaborRequest) (ports.LaborAssignment, error) {
	a.calls.Add(1)
	a.last = req
	return ports.LaborAssignment{AssociateId: req.AssociateId, PathId: req.PathId, AssignmentId: req.AssociateId + "@" + req.PathId}, a.err
}

type reallocRig struct {
	router   http.Handler
	assigner *reallocAssigner
	staffing *reallocStaffing
	now      *time.Time
}

func newReallocRig(t *testing.T) *reallocRig {
	t.Helper()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
	if err != nil {
		t.Fatal(err)
	}
	st := &reallocStaffing{gaps: map[string]ports.StaffingGap{
		"pack": {PathId: "pack", PlannedHeads: 10, ActiveHeads: 7, Understaffed: true},
		"pick": {PathId: "pick", PlannedHeads: 5, ActiveHeads: 8},
	}}
	outlook, err := usecases.NewLaborReallocationOutlook(st)
	if err != nil {
		t.Fatal(err)
	}
	asg := &reallocAssigner{}
	uc, err := usecases.NewLaborReallocation(outlook, signer, asg, nil, clock)
	if err != nil {
		t.Fatal(err)
	}
	h := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief(), LaborReallocation: uc}
	return &reallocRig{router: inboundhttp.NewRouter(h, "warehouse-ops-agent-test"), assigner: asg, staffing: st, now: &now}
}

func (r *reallocRig) post(path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	r.router.ServeHTTP(rec, req)
	return rec
}

func (r *reallocRig) postJSON(path, body string) *httptest.ResponseRecorder {
	return r.post(path, "application/json", body)
}

const (
	reallocProposePath = "/labor-reallocation/proposals"
	reallocPropose     = `{"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1","fromPathId":"pick"}]}`
)

type proposalBody struct {
	SiteCode  string `json:"siteCode"`
	Target    map[string]any
	Proposals []struct {
		ProposalId  string           `json:"proposalId"`
		Rank        int              `json:"rank"`
		Token       string           `json:"token"`
		ExpiresAt   string           `json:"expiresAt"`
		AssociateId string           `json:"associateId"`
		FromPathId  string           `json:"fromPathId"`
		ToPathId    string           `json:"toPathId"`
		ReasonCodes []string         `json:"reasonCodes"`
		Evidence    []map[string]any `json:"evidence"`
		NextCheck   string           `json:"nextCheck"`
	} `json:"proposals"`
	Skipped []map[string]string `json:"skipped"`
}

func proposeOK(t *testing.T, r *reallocRig, body string) proposalBody {
	t.Helper()
	rec := r.postJSON(reallocProposePath, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("propose = %d %s", rec.Code, rec.Body)
	}
	var out proposalBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func approvePath(id string) string { return "/labor-reallocation/proposals/" + id + "/approve" }

func TestReallocationRoutes503WhenNotConfigured(t *testing.T) {
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}, "t")
	for _, p := range []string{reallocProposePath, approvePath("lrp-x")} {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "WORKFORCE_MANAGEMENT_A2A_URL") {
			t.Errorf("%s = %d %s, want 503 naming the env var", p, rec.Code, rec.Body)
		}
	}
}

func TestReallocationProposeReturnsRankedMovesWithTokens(t *testing.T) {
	rig := newReallocRig(t)
	out := proposeOK(t, rig, reallocPropose)
	if len(out.Proposals) != 1 || out.SiteCode != "WH1" {
		t.Fatalf("body = %+v", out)
	}
	p := out.Proposals[0]
	if p.Token == "" || !strings.HasPrefix(p.ProposalId, "lrp-") || p.Rank != 1 || p.AssociateId != "a1" || p.FromPathId != "pick" || p.ToPathId != "pack" || p.ExpiresAt != "2026-10-29T12:05:00Z" {
		t.Fatalf("proposal = %+v", p)
	}
	if len(p.ReasonCodes) != 2 || len(p.Evidence) != 2 || !strings.HasPrefix(p.NextCheck, "Read-only:") {
		t.Fatalf("reasons/evidence/nextCheck = %+v", p)
	}
	if rig.assigner.calls.Load() != 0 {
		t.Fatal("propose must not write")
	}
}

func TestReallocationProposeEchoesTheTargetReading(t *testing.T) {
	out := proposeOK(t, newReallocRig(t), reallocPropose)
	if out.Target["plannedHeads"] != float64(10) || out.Target["activeHeads"] != float64(7) || out.Target["understaffed"] != true || out.Target["pathId"] != "pack" {
		t.Fatalf("target = %v", out.Target)
	}
}

func TestReallocationProposeListsSkippedCandidates(t *testing.T) {
	rig := newReallocRig(t)
	out := proposeOK(t, rig, `{"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1","fromPathId":"pick"},{"associateId":"a2","fromPathId":"pack"}]}`)
	if len(out.Skipped) != 1 || out.Skipped[0]["associateId"] != "a2" || out.Skipped[0]["reason"] != "already_on_target_path" {
		t.Fatalf("skipped = %v", out.Skipped)
	}
}

func TestReallocationProposeStatusMapping(t *testing.T) {
	cases := map[string]struct {
		mutate func(*reallocRig)
		body   string
		want   int
	}{
		"target not understaffed is 409": {func(r *reallocRig) {
			r.staffing.gaps["pack"] = ports.StaffingGap{PathId: "pack", PlannedHeads: 4, ActiveHeads: 4}
		}, reallocPropose, http.StatusConflict},
		"nobody can move is 409": {func(r *reallocRig) {
			r.staffing.gaps["pick"] = ports.StaffingGap{PathId: "pick", PlannedHeads: 5, ActiveHeads: 5}
		}, reallocPropose, http.StatusConflict},
		"missing siteCode is 400":      {nil, `{"shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1"}]}`, http.StatusBadRequest},
		"no candidates is 400":         {nil, `{"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[]}`, http.StatusBadRequest},
		"upstream read failure is 502": {func(r *reallocRig) { delete(r.staffing.gaps, "pack") }, reallocPropose, http.StatusBadGateway},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newReallocRig(t)
			if tc.mutate != nil {
				tc.mutate(rig)
			}
			rec := rig.postJSON(reallocProposePath, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
			if strings.Contains(rec.Body.String(), "token") {
				t.Fatalf("a refused proposal leaked a token: %s", rec.Body)
			}
		})
	}
}

func TestReallocationProposeThenApproveDelegatesOneMove(t *testing.T) {
	rig := newReallocRig(t)
	p := proposeOK(t, rig, reallocPropose).Proposals[0]
	rec := rig.postJSON(approvePath(p.ProposalId), `{"token":"`+p.Token+`","approvedBy":"supervisor-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	want := map[string]string{"proposalId": p.ProposalId, "associateId": "a1", "pathId": "pack", "assignmentId": "a1@pack", "approvedBy": "supervisor-1", "executedAt": "2026-10-29T12:00:00Z"}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("outcome[%s] = %q, want %q (all: %v)", k, out[k], v, out)
		}
	}
	if rig.assigner.calls.Load() != 1 || rig.assigner.last.AgentId != "warehouse-ops-agent" || rig.assigner.last.ProposalId != p.ProposalId || rig.assigner.last.ApprovedBy != "supervisor-1" {
		t.Fatalf("peer saw %+v (%d calls)", rig.assigner.last, rig.assigner.calls.Load())
	}
}

func TestReallocationApproveStatusMapping(t *testing.T) {
	approve := func(rig *reallocRig, p proposalBody, token, approvedBy string) *httptest.ResponseRecorder {
		return rig.postJSON(approvePath(p.Proposals[0].ProposalId), `{"token":"`+token+`","approvedBy":"`+approvedBy+`"}`)
	}
	t.Run("forged token is 400 and never reaches the peer", func(t *testing.T) {
		rig := newReallocRig(t)
		p := proposeOK(t, rig, reallocPropose)
		if rec := approve(rig, p, p.Proposals[0].Token+"x", "sup"); rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if rig.assigner.calls.Load() != 0 {
			t.Fatal("a forged token reached the peer")
		}
	})
	t.Run("expired token is 410", func(t *testing.T) {
		rig := newReallocRig(t)
		p := proposeOK(t, rig, reallocPropose)
		*rig.now = rig.now.Add(5 * time.Minute)
		if rec := approve(rig, p, p.Proposals[0].Token, "sup"); rec.Code != http.StatusGone {
			t.Fatalf("status = %d, want 410", rec.Code)
		}
		if rig.assigner.calls.Load() != 0 {
			t.Fatal("an expired token reached the peer")
		}
	})
	t.Run("token of another proposal is 400", func(t *testing.T) {
		rig := newReallocRig(t)
		p := proposeOK(t, rig, reallocPropose)
		other := proposeOK(t, rig, reallocPropose)
		if rec := rig.postJSON(approvePath(p.Proposals[0].ProposalId), `{"token":"`+other.Proposals[0].Token+`","approvedBy":"sup"}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("missing approvedBy is 400", func(t *testing.T) {
		rig := newReallocRig(t)
		p := proposeOK(t, rig, reallocPropose)
		if rec := approve(rig, p, p.Proposals[0].Token, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("replay is 409", func(t *testing.T) {
		rig := newReallocRig(t)
		p := proposeOK(t, rig, reallocPropose)
		_ = approve(rig, p, p.Proposals[0].Token, "sup")
		if rec := approve(rig, p, p.Proposals[0].Token, "sup"); rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
		if rig.assigner.calls.Load() != 1 {
			t.Fatalf("peer calls = %d, want 1", rig.assigner.calls.Load())
		}
	})
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"peer business rule is 409":   {ports.ErrAssignmentRejected, http.StatusConflict},
		"peer unknown id is 404":      {ports.ErrAssignmentNotFound, http.StatusNotFound},
		"peer refused arguments 400":  {ports.ErrAssignmentInvalid, http.StatusBadRequest},
		"peer unreachable is 502":     {ports.ErrWorkforceAgentUnreachable, http.StatusBadGateway},
		"unclassified failure is 502": {errors.New("boom"), http.StatusBadGateway},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newReallocRig(t)
			rig.assigner.err = tc.err
			p := proposeOK(t, rig, reallocPropose)
			if rec := approve(rig, p, p.Proposals[0].Token, "sup"); rec.Code != tc.want {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
		})
	}
}

func TestReallocationRoutesRejectNonJSONContentTypes(t *testing.T) {
	rig := newReallocRig(t)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		for _, p := range []string{reallocProposePath, approvePath("lrp-x")} {
			if rec := rig.post(p, ct, reallocPropose); rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s with Content-Type %q = %d, want 415", p, ct, rec.Code)
			}
		}
	}
	if rig.assigner.calls.Load() != 0 {
		t.Fatal("a rejected request reached the peer")
	}
}

func TestReallocationRoutesRejectMalformedBodies(t *testing.T) {
	rig := newReallocRig(t)
	for name, body := range map[string]string{
		"not json":      `{`,
		"unknown field": `{"siteCode":"WH1","sudo":true}`,
		"trailing data": reallocPropose + ` {}`,
		"wrong type":    `{"siteCode":12}`,
	} {
		if rec := rig.postJSON(reallocProposePath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
	if rec := rig.postJSON(reallocProposePath, `{"siteCode":"`+strings.Repeat("x", 9<<10)+`"}`); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d, want 413", rec.Code)
	}
}

func TestReallocationRoutesStayBehindTheGETOnlyCORSPolicy(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	rig := newReallocRig(t)
	for _, p := range []string{reallocProposePath, approvePath("lrp-x")} {
		req := httptest.NewRequest(http.MethodOptions, p, nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "content-type")
		rec := httptest.NewRecorder()
		rig.router.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
			t.Errorf("%s preflight allowed %q — a browser on another origin could now call the write", p, got)
		}
	}
}

func TestReallocationRoutesAreNotServedForOtherMethods(t *testing.T) {
	rig := newReallocRig(t)
	for _, p := range []string{reallocProposePath, approvePath("lrp-x")} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		rig.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", p, rec.Code)
		}
	}
}
