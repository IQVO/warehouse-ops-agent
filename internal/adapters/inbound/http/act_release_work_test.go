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
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type actDecider struct {
	decision policy.Decision
	err      error
}

func (a *actDecider) Execute(_ context.Context, _, _, pathId string) (policy.Decision, error) {
	d := a.decision
	d.PathId = pathId
	return d, a.err
}

type actReleaser struct {
	calls atomic.Int32
	path  string
	err   error
}

func (a *actReleaser) ReleaseNextWork(_ context.Context, pathId string) (ports.ReleasedWork, error) {
	a.calls.Add(1)
	a.path = pathId
	return ports.ReleasedWork{WorkUnitId: "wu-9", CPT: "2026-12-24T09:00:00Z", Ref: "order-9"}, a.err
}

type actRig struct {
	router   http.Handler
	releaser *actReleaser
	now      *time.Time
}

func newActRig(t *testing.T, d policy.Decision) *actRig {
	t.Helper()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
	if err != nil {
		t.Fatal(err)
	}
	rel := &actReleaser{}
	uc, err := usecases.NewReleaseWork(&actDecider{decision: d}, signer, rel, nil, clock)
	if err != nil {
		t.Fatal(err)
	}
	h := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief(), ReleaseWork: uc}
	return &actRig{router: inboundhttp.NewRouter(h, "warehouse-ops-agent-test"), releaser: rel, now: &now}
}

func (r *actRig) post(path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	r.router.ServeHTTP(rec, req)
	return rec
}

func (r *actRig) postJSON(path, body string) *httptest.ResponseRecorder {
	return r.post(path, "application/json", body)
}

const (
	proposalsPath = "/act/release-next-work/proposals"
	approvalsPath = "/act/release-next-work/approvals"
	proposeBody   = `{"pathId":"pick-zone-a","buildingId":"b1","shiftId":"s1"}`
)

var recommendRelease = policy.Decision{
	RecommendedAction: policy.ActionReleaseNextWork,
	Rationale:         "backlog 12 over wip 3",
	Source:            policy.SourceDeterministic,
}

func mintToken(t *testing.T, r *actRig) string {
	t.Helper()
	rec := r.postJSON(proposalsPath, proposeBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("propose = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("propose body %s: %v", rec.Body, err)
	}
	return out.Token
}

func TestActRoutes503WhenNotConfigured(t *testing.T) {
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}, "t")
	for _, p := range []string{proposalsPath, approvalsPath} {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503", p, rec.Code)
		}
	}
}

func TestProposeReturnsTokenAndTheFullRecommendation(t *testing.T) {
	rig := newActRig(t, recommendRelease)
	rec := rig.postJSON(proposalsPath, proposeBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["token"] == "" || out["action"] != "release_next_work" || out["pathId"] != "pick-zone-a" || out["source"] != "deterministic" || out["expiresAt"] != "2026-10-29T12:05:00Z" {
		t.Fatalf("body = %v", out)
	}
	rc, _ := out["recommendation"].(map[string]any)
	if rc["recommendedAction"] != "release_next_work" || rc["rationale"] != "backlog 12 over wip 3" {
		t.Fatalf("recommendation = %v", rc)
	}
	if rig.releaser.calls.Load() != 0 {
		t.Fatal("propose must not write")
	}
}

func TestProposeConflictsWhenPolicyDoesNotRecommendRelease(t *testing.T) {
	rig := newActRig(t, policy.Decision{RecommendedAction: policy.FlowBalanceActionHold, Source: policy.SourceDeterministic})
	rec := rig.postJSON(proposalsPath, proposeBody)
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("status = %d body = %s, want 409 and no token", rec.Code, rec.Body)
	}
}

func TestProposeConflictsOnPartialEvidence(t *testing.T) {
	d := recommendRelease
	d.Partial = true
	d.MissingSignals = []string{"workforce-management"}
	rec := newActRig(t, d).postJSON(proposalsPath, proposeBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestProposeInvalidInputIs400(t *testing.T) {
	rig := newActRig(t, recommendRelease)
	h := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}
	signer, _ := approval.NewEphemeralSigner(time.Minute, nil)
	uc, _ := usecases.NewReleaseWork(&actDecider{err: usecases.ErrInvalidInput}, signer, rig.releaser, nil, nil)
	h.ReleaseWork = uc
	req := httptest.NewRequest(http.MethodPost, proposalsPath, strings.NewReader(`{"pathId":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	inboundhttp.NewRouter(h, "t").ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestProposeThenApproveReleasesTheProposedPath(t *testing.T) {
	rig := newActRig(t, recommendRelease)
	token := mintToken(t, rig)
	rec := rig.postJSON(approvalsPath, `{"token":"`+token+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["workUnitId"] != "wu-9" || out["pathId"] != "pick-zone-a" || out["reference"] != "order-9" || out["cpt"] != "2026-12-24T09:00:00Z" || out["executedAt"] != "2026-10-29T12:00:00Z" {
		t.Fatalf("outcome = %v", out)
	}
	if rig.releaser.path != "pick-zone-a" || rig.releaser.calls.Load() != 1 {
		t.Fatalf("releaser path=%q calls=%d", rig.releaser.path, rig.releaser.calls.Load())
	}
}

func TestApproveStatusMapping(t *testing.T) {
	t.Run("replay is 409", func(t *testing.T) {
		rig := newActRig(t, recommendRelease)
		token := mintToken(t, rig)
		_ = rig.postJSON(approvalsPath, `{"token":"`+token+`"}`)
		if rec := rig.postJSON(approvalsPath, `{"token":"`+token+`"}`); rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
		if rig.releaser.calls.Load() != 1 {
			t.Fatalf("releaser calls = %d, want 1", rig.releaser.calls.Load())
		}
	})
	t.Run("garbage token is 400", func(t *testing.T) {
		rig := newActRig(t, recommendRelease)
		if rec := rig.postJSON(approvalsPath, `{"token":"nope"}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if rig.releaser.calls.Load() != 0 {
			t.Fatal("a bad token reached the releaser")
		}
	})
	t.Run("empty token is 400", func(t *testing.T) {
		if rec := newActRig(t, recommendRelease).postJSON(approvalsPath, `{}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("expired token is 410", func(t *testing.T) {
		rig := newActRig(t, recommendRelease)
		token := mintToken(t, rig)
		*rig.now = rig.now.Add(6 * time.Minute)
		if rec := rig.postJSON(approvalsPath, `{"token":"`+token+`"}`); rec.Code != http.StatusGone {
			t.Fatalf("status = %d, want 410", rec.Code)
		}
	})
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"nothing pending is 409":   {ports.ErrNoPendingWork, http.StatusConflict},
		"unknown path is 404":      {ports.ErrUnknownWorkPlanningPath, http.StatusNotFound},
		"agent unreachable is 502": {ports.ErrWorkPlanningUnreachable, http.StatusBadGateway},
		"unclassified failure 502": {errors.New("boom"), http.StatusBadGateway},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newActRig(t, recommendRelease)
			rig.releaser.err = tc.err
			token := mintToken(t, rig)
			if rec := rig.postJSON(approvalsPath, `{"token":"`+token+`"}`); rec.Code != tc.want {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
		})
	}
}

func TestActRoutesRejectNonJSONContentTypes(t *testing.T) {
	// A cross-site HTML form can only send these "simple" types without a
	// CORS preflight; refusing them is the no-auth CSRF defence.
	rig := newActRig(t, recommendRelease)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonp"} {
		for _, p := range []string{proposalsPath, approvalsPath} {
			if rec := rig.post(p, ct, proposeBody); rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s with Content-Type %q = %d, want 415", p, ct, rec.Code)
			}
		}
	}
	if rig.releaser.calls.Load() != 0 {
		t.Fatal("a rejected request reached the releaser")
	}
	if rec := rig.post(proposalsPath, "application/json; charset=utf-8", proposeBody); rec.Code != http.StatusOK {
		t.Fatalf("application/json with a charset parameter = %d, want 200", rec.Code)
	}
}

func TestActRoutesRejectMalformedBodies(t *testing.T) {
	rig := newActRig(t, recommendRelease)
	for name, body := range map[string]string{
		"not json":       `{`,
		"unknown field":  `{"pathId":"p","buildingId":"b","shiftId":"s","sudo":true}`,
		"trailing data":  proposeBody + ` {}`,
		"wrong type":     `{"pathId":12}`,
		"empty document": ``,
	} {
		if rec := rig.postJSON(proposalsPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
	big := `{"pathId":"` + strings.Repeat("x", 9<<10) + `"}`
	if rec := rig.postJSON(proposalsPath, big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d, want 413", rec.Code)
	}
}

func TestActRoutesStayBehindTheGETOnlyCORSPolicy(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	rig := newActRig(t, recommendRelease)
	for _, p := range []string{proposalsPath, approvalsPath} {
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
