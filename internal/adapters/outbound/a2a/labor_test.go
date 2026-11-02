package a2a

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/claudioed/warehouse-ops-agent/internal/observability"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// laborPeer is an in-memory stand-in for the workforce-management A2A
// agent: the REAL SDK server side (self-pointing card, SendMessage over
// JSON-RPC). Like the real skill it REJECTS any argument name it does not
// know, so a client that sends an unknown name fails here the way it would
// there. It records every request's arguments and the A2A-Version header of
// every HTTP request.
type laborPeer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
	versions []string
}

func (p *laborPeer) seen() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.requests...)
}

func (p *laborPeer) versionHeaders() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.versions...)
}

// knownAssignLaborArgs is the argument set the peer accepts once the
// attribution arguments (fleet contract G4) are live.
var knownAssignLaborArgs = map[string]bool{"associateId": true, "pathId": true, "proposalId": true, "approvedBy": true, "agentId": true}

func laborProblem(detail string, status int) any {
	return map[string]any{"skill": "assign_labor", "problem": map[string]any{"title": "Request failed validation", "detail": detail, "status": status}}
}

func newLaborPeer(t *testing.T, answer func(args map[string]any) any) *laborPeer {
	t.Helper()
	listener, err := netListen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	baseURL := "http://" + listener.Addr().String()
	peer := &laborPeer{}
	executor := a2asrv.AgentExecutorFunc(func(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			args := peer.record(ec)
			reply := answer(args)
			for name := range args {
				if !knownAssignLaborArgs[name] {
					reply = laborProblem("unknown argument "+name, 400)
				}
			}
			yield(a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(reply)), nil)
		}
	})
	card := &a2a.AgentCard{
		Name: "workforce-management-stub", Description: "test double", Version: "test",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(baseURL+"/", a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"application/json"}, DefaultOutputModes: []string{"application/json"},
		Skills: []a2a.AgentSkill{{ID: "assign_labor", Name: "Assign labor", Description: "test double skill"}},
	}
	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	record := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			peer.mu.Lock()
			peer.versions = append(peer.versions, r.Header.Get(a2a.SvcParamVersion))
			peer.mu.Unlock()
		}
		mux.ServeHTTP(w, r)
	})
	peer.Server = &httptest.Server{Listener: listener, Config: &http.Server{Handler: record}}
	peer.Start()
	t.Cleanup(peer.Close)
	return peer
}

// record stores and returns the skill arguments of one request.
func (p *laborPeer) record(ec *a2asrv.ExecutorContext) map[string]any {
	args := map[string]any{}
	if data, ok := ec.Message.Parts[0].Content.(a2a.Data); ok {
		if m, ok := data.Value.(map[string]any); ok {
			if a, ok := m["arguments"].(map[string]any); ok {
				args = a
			}
		}
	}
	p.mu.Lock()
	p.requests = append(p.requests, args)
	p.mu.Unlock()
	return args
}

func assigned(args map[string]any) any {
	return map[string]any{"skill": "assign_labor", "data": map[string]any{"laborAssigned": map[string]any{
		"associateId": args["associateId"], "pathId": args["pathId"], "assignmentId": "assoc-7@pack",
	}}}
}

var laborReq = ports.AssignLaborRequest{AssociateId: "assoc-7", PathId: "pack", ProposalId: "lrp-abc", ApprovedBy: "supervisor-1", AgentId: "warehouse-ops-agent"}

func TestAssignLabor_SendsExactlyTheContractArgumentsAndMapsTheAnswer(t *testing.T) {
	peer := newLaborPeer(t, assigned)
	got, err := NewLaborClient(peer.URL+"/", nil).AssignLabor(context.Background(), laborReq)
	if err != nil {
		t.Fatalf("AssignLabor: %v", err)
	}
	if got != (ports.LaborAssignment{AssociateId: "assoc-7", PathId: "pack", AssignmentId: "assoc-7@pack"}) {
		t.Fatalf("assignment = %+v", got)
	}
	want := map[string]any{"associateId": "assoc-7", "pathId": "pack", "proposalId": "lrp-abc", "approvedBy": "supervisor-1", "agentId": "warehouse-ops-agent"}
	if seen := peer.seen(); len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("peer saw %v, want exactly %v", seen, want)
	}
}

func TestAssignLabor_SendsTheA2AVersionHeader(t *testing.T) {
	peer := newLaborPeer(t, assigned)
	if _, err := NewLaborClient(peer.URL, nil).AssignLabor(context.Background(), laborReq); err != nil {
		t.Fatal(err)
	}
	versions := peer.versionHeaders()
	if len(versions) == 0 {
		t.Fatal("the peer saw no JSON-RPC request")
	}
	for _, v := range versions {
		if v == "" {
			t.Fatalf("a request carried no %s header: %v", a2a.SvcParamVersion, versions)
		}
	}
}

func TestAssignLabor_ProblemMapping(t *testing.T) {
	cases := map[string]struct {
		status int
		want   error
	}{
		"domain rule (certification, break, shift ended)": {409, ports.ErrAssignmentRejected},
		"unknown associate or path":                       {404, ports.ErrAssignmentNotFound},
		"refused arguments":                               {400, ports.ErrAssignmentInvalid},
		"peer internal problem":                           {500, ports.ErrWorkforceAgentUnreachable},
		"dependency unavailable":                          {503, ports.ErrWorkforceAgentUnreachable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			peer := newLaborPeer(t, func(map[string]any) any { return laborProblem("the peer's reason", tc.status) })
			_, err := NewLaborClient(peer.URL, nil).AssignLabor(context.Background(), laborReq)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), "the peer's reason") {
				t.Fatalf("error %q lost the peer's detail", err)
			}
		})
	}
}

func TestAssignLabor_UnreachableAndOffContractAreTyped(t *testing.T) {
	if _, err := NewLaborClient("http://127.0.0.1:1", nil).AssignLabor(context.Background(), laborReq); !errors.Is(err, ports.ErrWorkforceAgentUnreachable) {
		t.Fatalf("unreachable error = %v", err)
	}
	peer := newLaborPeer(t, func(map[string]any) any { return map[string]any{"skill": "assign_labor"} })
	if _, err := NewLaborClient(peer.URL, nil).AssignLabor(context.Background(), laborReq); !errors.Is(err, ports.ErrWorkforceAgentUnreachable) {
		t.Fatalf("off-contract error = %v", err)
	}
}

// The skill span keeps its name and carries the three G9 attributes.
func TestAssignLabor_SpanCarriesTheAgenticAttributes(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	peer := newLaborPeer(t, assigned)
	if _, err := NewLaborClient(peer.URL, nil).AssignLabor(context.Background(), laborReq); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range rec.Ended() {
		if s.Name() != "a2a.skill assign_labor" {
			continue
		}
		found = true
		attrs := map[string]string{}
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.String()
		}
		want := map[string]string{
			observability.AttrOperationName: observability.OpInvokeAgent,
			observability.AttrAgentName:     "warehouse-ops-agent",
			observability.AttrAuthority:     "L3",
			"a2a.skill":                     "assign_labor",
		}
		for k, v := range want {
			if attrs[k] != v {
				t.Errorf("span attribute %s = %q, want %q (all: %v)", k, attrs[k], v, attrs)
			}
		}
	}
	if !found {
		t.Fatal("no span named \"a2a.skill assign_labor\" was recorded")
	}
}

func TestEverySkillHasADeclaredAuthority(t *testing.T) {
	for _, skill := range []string{releaseSkill, assignLaborSkill, findExceptionsSkill} {
		if !observability.ValidAuthority(skillAuthority[skill]) {
			t.Errorf("skill %q has authority %q, want one of L1..L4", skill, skillAuthority[skill])
		}
	}
	if skillAuthority[assignLaborSkill] != "L3" || skillAuthority[releaseSkill] != "L3" || skillAuthority[findExceptionsSkill] != "L1" {
		t.Errorf("authority table = %v", skillAuthority)
	}
}
