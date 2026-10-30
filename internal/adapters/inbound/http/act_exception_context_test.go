package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type stubExceptions struct {
	calls   int
	summary ports.ExceptionSummary
	err     error
}

func (s *stubExceptions) OpenExceptions(context.Context, ports.ExceptionContextQuery) (ports.ExceptionSummary, error) {
	s.calls++
	return s.summary, s.err
}

// newExceptionRig builds the REST router over a ReleaseWork wired with the
// ADR-0028 advisory port (nil port = lookup disabled).
func newExceptionRig(t *testing.T, d policy.Decision, port ports.ExceptionContext) *actRig {
	t.Helper()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
	if err != nil {
		t.Fatal(err)
	}
	rel := &actReleaser{}
	uc, err := usecases.NewReleaseWork(&actDecider{decision: d}, signer, rel, nil, clock, usecases.WithExceptionContext(port, 0))
	if err != nil {
		t.Fatal(err)
	}
	h := &inboundhttp.Handlers{DailyBrief: newTestDailyBrief(), ReleaseWork: uc}
	return &actRig{router: inboundhttp.NewRouter(h, "warehouse-ops-agent-test"), releaser: rel, now: &now}
}

// proposeBodyMap returns the top-level members as raw JSON, so the field
// ORDER of the documented shape is asserted too (a map would sort keys).
func proposeBodyMap(t *testing.T, rig *actRig) map[string]json.RawMessage {
	t.Helper()
	rec := rig.postJSON(proposalsPath, proposeBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("propose = %d %s", rec.Code, rec.Body)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The literal response shape documented in docs/docs/api/http-routes.md.
func TestProposeCarriesTheExceptionContextShape(t *testing.T) {
	port := &stubExceptions{summary: ports.ExceptionSummary{OpenCount: 17, Cases: []ports.ExceptionBrief{
		{CaseID: "case-90a1a2e0", Status: "OPEN", Queue: "problem-solve", Severity: "HIGH", Escalated: true, Reason: "CartonDiscrepancyRaised"},
	}}}
	out := proposeBodyMap(t, newExceptionRig(t, recommendRelease, port))

	got := out["exceptionContext"]
	const want = `{"status":"ok","summary":{"openCount":17,"cases":[` +
		`{"caseId":"case-90a1a2e0","status":"OPEN","queue":"problem-solve","severity":"HIGH","escalated":true,"reason":"CartonDiscrepancyRaised"}]}}`
	if string(got) != want {
		t.Fatalf("exceptionContext =\n%s\nwant\n%s", got, want)
	}
}

func TestProposeExceptionContextUnavailableAndDisabledCarryNoSummary(t *testing.T) {
	for name, tc := range map[string]struct {
		port ports.ExceptionContext
		want string
	}{
		"unavailable": {&stubExceptions{err: ports.ErrExceptionContextUnavailable}, `{"status":"unavailable"}`},
		"disabled":    {nil, `{"status":"disabled"}`},
	} {
		out := proposeBodyMap(t, newExceptionRig(t, recommendRelease, tc.port))
		if got := out["exceptionContext"]; string(got) != tc.want {
			t.Errorf("%s: exceptionContext = %s, want %s", name, got, tc.want)
		}
		if len(out["token"]) <= len(`""`) {
			t.Errorf("%s: no token minted", name)
		}
	}
}

func TestProposeEmptyHubIsOkWithAnEmptyCaseList(t *testing.T) {
	out := proposeBodyMap(t, newExceptionRig(t, recommendRelease, &stubExceptions{summary: ports.ExceptionSummary{}}))
	if got := out["exceptionContext"]; string(got) != `{"status":"ok","summary":{"openCount":0,"cases":[]}}` {
		t.Fatalf("exceptionContext = %s", got)
	}
}

// Everything except the random token and the advisory block is identical
// byte for byte whatever the hub answered.
func TestProposeBodyIsByteIdenticalAroundTheExceptionContext(t *testing.T) {
	withoutAdvisory := func(m map[string]json.RawMessage) []byte {
		delete(m, "token")
		delete(m, "exceptionContext")
		b, _ := json.Marshal(m)
		return b
	}
	base := withoutAdvisory(proposeBodyMap(t, newExceptionRig(t, recommendRelease, nil)))
	for name, port := range map[string]ports.ExceptionContext{
		"present":     &stubExceptions{summary: ports.ExceptionSummary{OpenCount: 1, Cases: []ports.ExceptionBrief{{CaseID: "case-1"}}}},
		"absent":      &stubExceptions{},
		"unavailable": &stubExceptions{err: errors.New("down")},
	} {
		if got := withoutAdvisory(proposeBodyMap(t, newExceptionRig(t, recommendRelease, port))); !bytes.Equal(got, base) {
			t.Errorf("%s: body changed\n got %s\nwant %s", name, got, base)
		}
	}
}

func TestRefusedProposalBodyDoesNotCarryTheExceptionContext(t *testing.T) {
	port := &stubExceptions{summary: ports.ExceptionSummary{OpenCount: 5}}
	rig := newExceptionRig(t, policy.Decision{RecommendedAction: policy.ActionAssignLabor, Source: policy.SourceDeterministic}, port)
	rec := rig.postJSON(proposalsPath, proposeBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d %s, want 409 exactly as before ADR 0028", rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if _, has := out["exceptionContext"]; has || port.calls != 0 {
		t.Fatalf("body %v, hub calls %d: a refusal must neither carry nor trigger the lookup", out, port.calls)
	}
}
