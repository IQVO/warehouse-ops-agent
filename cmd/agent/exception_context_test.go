package main

import (
	"context"
	"io"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type releaseDecider struct{}

func (releaseDecider) Execute(_ context.Context, _, _, pathId string) (policy.Decision, error) {
	return policy.Decision{PathId: pathId, RecommendedAction: policy.ActionReleaseNextWork, Rationale: "backlog over wip", Source: policy.SourceDeterministic}, nil
}

type neverReleaser struct{ calls int }

func (r *neverReleaser) ReleaseNextWork(context.Context, string) (ports.ReleasedWork, error) {
	r.calls++
	return ports.ReleasedWork{}, nil
}

// exceptionStub is a real-SDK exception-management stub answering the given
// envelope to find_exceptions.
func exceptionStub(t *testing.T, envelope any) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	executor := a2asrv.AgentExecutorFunc(func(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(envelope)), nil)
		}
	})
	card := &a2a.AgentCard{
		Name: "exception-management-stub", Description: "test double", Version: "test",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://"+l.Addr().String()+"/", a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"application/json"}, DefaultOutputModes: []string{"application/json"},
		Skills: []a2a.AgentSkill{{ID: "find_exceptions", Name: "Find exception cases", Description: "test double"}},
	}
	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: mux}}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// proposeVia builds the use case the way newReleaseWork does (through
// exceptionContextOption) for the given config and proposes once.
func proposeVia(t *testing.T, url string) (usecases.ReleaseProposal, *neverReleaser) {
	t.Helper()
	signer, err := approval.NewEphemeralSigner(time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	rel := &neverReleaser{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	uc, err := usecases.NewReleaseWork(releaseDecider{}, signer, rel, logger, nil,
		exceptionContextOption(config.Config{ExceptionManagementA2AURL: url}, logger))
	if err != nil {
		t.Fatal(err)
	}
	p, err := uc.Propose(context.Background(), "b1", "s1", "pick-zone-a")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	return p, rel
}

func TestExceptionContextOption_UnsetIsDisabled(t *testing.T) {
	p, _ := proposeVia(t, "")
	if p.ExceptionContext.Status != usecases.ExceptionContextDisabled || p.ExceptionContext.Summary != nil || p.Token == "" {
		t.Fatalf("proposal = %+v, want disabled, no summary and a token", p)
	}
}

func TestExceptionContextOption_AgainstAStubPeerOverTheRealWire(t *testing.T) {
	ok := map[string]any{"skill": "find_exceptions", "data": map[string]any{"exceptions": map[string]any{"count": 1, "cases": []any{
		map[string]any{"caseId": "case-1", "sourceType": "com.x.TaskStuck", "status": "OPEN", "severity": "HIGH", "queue": "q", "escalated": true},
	}}}}
	problem := map[string]any{"skill": "find_exceptions", "problem": map[string]any{"title": "Internal server error", "detail": "boom", "status": 500}}

	for name, tc := range map[string]struct {
		url    func() string
		status usecases.ExceptionContextStatus
	}{
		"ok":                 {func() string { return exceptionStub(t, ok).URL + "/" }, usecases.ExceptionContextOK},
		"problem answer":     {func() string { return exceptionStub(t, problem).URL }, usecases.ExceptionContextUnavailable},
		"connection refused": {func() string { return "http://127.0.0.1:1/" }, usecases.ExceptionContextUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			p, rel := proposeVia(t, tc.url())
			if p.ExceptionContext.Status != tc.status || p.Token == "" || rel.calls != 0 {
				t.Fatalf("proposal = %+v releaser calls %d, want status %q, a token and no write", p, rel.calls, tc.status)
			}
			if tc.status == usecases.ExceptionContextOK {
				s := p.ExceptionContext.Summary
				if s == nil || s.OpenCount != 1 || len(s.Cases) != 1 || s.Cases[0].CaseID != "case-1" || s.Cases[0].Reason != "TaskStuck" {
					t.Fatalf("summary = %+v", s)
				}
			}
		})
	}
}
