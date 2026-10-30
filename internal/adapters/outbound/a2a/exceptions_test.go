package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// exceptionPeer is a stub exception-management agent: the REAL SDK server
// side (self-pointing card, SendMessage over JSON-RPC) answering with
// whatever the test scripts, and recording every request it receives.
type exceptionPeer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
}

func (p *exceptionPeer) seen() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.requests...)
}

// newExceptionPeer serves the exception-management card. answer returns the
// envelope for a request (it may block to simulate a slow peer).
func newExceptionPeer(t *testing.T, answer func(ctx context.Context) any) *exceptionPeer {
	t.Helper()
	listener, err := netListen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	peer := &exceptionPeer{}
	executor := a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			if data, ok := ec.Message.Parts[0].Content.(a2a.Data); ok {
				if m, ok := data.Value.(map[string]any); ok {
					peer.mu.Lock()
					peer.requests = append(peer.requests, m)
					peer.mu.Unlock()
				}
			}
			yield(a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(answer(ctx))), nil)
		}
	})
	card := &a2a.AgentCard{
		Name: "exception-management-stub", Description: "test double", Version: "test",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://"+listener.Addr().String()+"/", a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"application/json"}, DefaultOutputModes: []string{"application/json"},
		Skills: []a2a.AgentSkill{{ID: "find_exceptions", Name: "Find exception cases", Description: "test double skill"}},
	}
	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	peer.Server = &httptest.Server{Listener: listener, Config: &http.Server{Handler: mux}}
	peer.Start()
	t.Cleanup(peer.Close)
	return peer
}

func fixed(env any) func(context.Context) any { return func(context.Context) any { return env } }

// caseWire builds one case the way exception-management's find_exceptions
// answers it, including the large fields this client must ignore.
func caseWire(id, sourceType, queue, severity string, escalated bool) map[string]any {
	return map[string]any{
		"caseId": id, "sourceType": sourceType, "sourceSubject": "rcpt-1/sscc/DuplicateSscc",
		"status": "OPEN", "severity": severity, "queue": queue, "occurrenceCount": 1, "escalated": escalated,
		"openedAt": "2026-10-26T02:32:12Z", "lastSeenAt": "2026-10-26T02:32:12Z", "slaMinutes": 60,
		"slaDueAt": "2026-10-26T03:32:12Z", "sourceEventId": "evt-1",
		"sourcePayload": map[string]any{"kind": "DuplicateSscc", "sscc": "0340"},
		"audit":         []any{map[string]any{"action": "OPENED", "actor": "system", "at": "2026-10-26T02:32:12Z"}},
		"version":       1,
	}
}

func exceptionsEnvelope(cases ...map[string]any) map[string]any {
	return map[string]any{
		"skill": "find_exceptions",
		"data":  map[string]any{"exceptions": map[string]any{"count": len(cases), "cases": cases}},
	}
}

const (
	typeCarton = "com.warehouse.wms.inbound-receiving.receipt.CartonDiscrepancyRaised"
	typeStuck  = "com.warehouse.wes.fulfillment-execution.task.TaskStuck"
)

func TestOpenExceptions_OkAsksOnlyTheReadSkillAndRanksEscalatedFirst(t *testing.T) {
	peer := newExceptionPeer(t, fixed(exceptionsEnvelope(
		caseWire("case-newest", typeStuck, "ps-main", "LOW", false),
		caseWire("case-escalated", typeCarton, "problem-solve", "HIGH", true),
		caseWire("case-older", typeStuck, "ps-main", "LOW", false),
	)))
	client := NewExceptionClient(peer.URL+"/", nil)

	got, err := client.OpenExceptions(context.Background(), ports.ExceptionContextQuery{})
	if err != nil {
		t.Fatalf("OpenExceptions: %v", err)
	}
	want := ports.ExceptionSummary{OpenCount: 3, Cases: []ports.ExceptionBrief{
		{CaseID: "case-escalated", Status: "OPEN", Queue: "problem-solve", Severity: "HIGH", Escalated: true, Reason: "CartonDiscrepancyRaised"},
		{CaseID: "case-newest", Status: "OPEN", Queue: "ps-main", Severity: "LOW", Reason: "TaskStuck"},
		{CaseID: "case-older", Status: "OPEN", Queue: "ps-main", Severity: "LOW", Reason: "TaskStuck"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
	// The wire request is exactly the read skill with the OPEN filter and
	// nothing else: no other skill, no other argument.
	reqs := peer.seen()
	if len(reqs) != 1 || !reflect.DeepEqual(reqs[0], map[string]any{"skill": "find_exceptions", "arguments": map[string]any{"status": "OPEN"}}) {
		t.Fatalf("requests = %v, want exactly one find_exceptions{status:OPEN}", reqs)
	}
}

func TestOpenExceptions_BoundsTheListButReportsTheTrueCount(t *testing.T) {
	cases := make([]map[string]any, 0, 14)
	for i := range 14 {
		cases = append(cases, caseWire(fmt.Sprintf("case-%02d", i), typeStuck, "q", "LOW", false))
	}
	client := NewExceptionClient(newExceptionPeer(t, fixed(exceptionsEnvelope(cases...))).URL, nil)
	for limit, want := range map[int]int{0: ports.DefaultExceptionCases, 3: 3, 99: ports.MaxExceptionCases} {
		got, err := client.OpenExceptions(context.Background(), ports.ExceptionContextQuery{Limit: limit})
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if got.OpenCount != 14 || len(got.Cases) != want {
			t.Errorf("limit %d: openCount=%d shown=%d, want 14 and %d", limit, got.OpenCount, len(got.Cases), want)
		}
	}
}

func TestOpenExceptions_NoOpenCasesIsOkWithAnEmptyList(t *testing.T) {
	client := NewExceptionClient(newExceptionPeer(t, fixed(exceptionsEnvelope())).URL, nil)
	got, err := client.OpenExceptions(context.Background(), ports.ExceptionContextQuery{})
	if err != nil || got.OpenCount != 0 || len(got.Cases) != 0 {
		t.Fatalf("got %+v err %v, want an empty ok summary", got, err)
	}
}

func TestShortReason(t *testing.T) {
	long := "x." + strings.Repeat("A", 200)
	for in, want := range map[string]string{
		typeCarton: "CartonDiscrepancyRaised", "plain": "plain", "": "unknown", "trailing.": "unknown",
		long: strings.Repeat("A", maxReasonRunes),
	} {
		if got := shortReason(in); got != want {
			t.Errorf("shortReason(%.30q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenExceptions_EveryFailureIsTheTypedUnavailableError(t *testing.T) {
	problem := map[string]any{"skill": "find_exceptions", "problem": map[string]any{
		"type": "https://peer/problems/internal-error", "title": "Internal server error", "detail": "an unexpected error occurred", "status": 500}}
	notFound := map[string]any{"skill": "find_exceptions", "problem": map[string]any{
		"type": "https://peer/problems/case-not-found", "title": "Not found", "detail": "gone", "status": 404}}
	cases := map[string]map[string]any{
		"problem answer (500)":            problem,
		"problem answer (404)":            notFound,
		"neither data nor problem":        {"skill": "find_exceptions"},
		"data without exceptions":         {"skill": "find_exceptions", "data": map[string]any{"slaStatus": map[string]any{}}},
		"count does not match cases":      {"skill": "find_exceptions", "data": map[string]any{"exceptions": map[string]any{"count": 2, "cases": []any{}}}},
		"count missing":                   {"skill": "find_exceptions", "data": map[string]any{"exceptions": map[string]any{"cases": []any{}}}},
		"case without an id":              exceptionsEnvelope(map[string]any{"sourceType": typeStuck}),
		"exceptions of the wrong shape":   {"skill": "find_exceptions", "data": map[string]any{"exceptions": "nope"}},
		"data of the wrong shape (array)": {"skill": "find_exceptions", "data": []any{1}},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			client := NewExceptionClient(newExceptionPeer(t, fixed(env)).URL, nil)
			_, err := client.OpenExceptions(context.Background(), ports.ExceptionContextQuery{})
			if !errors.Is(err, ports.ErrExceptionContextUnavailable) {
				t.Fatalf("error = %v, want ErrExceptionContextUnavailable", err)
			}
		})
	}
}

func TestOpenExceptions_ProblemKeepsThePeerDetailForTheLog(t *testing.T) {
	env := map[string]any{"skill": "find_exceptions", "problem": map[string]any{"title": "Internal server error", "detail": "db is down", "status": 500}}
	client := NewExceptionClient(newExceptionPeer(t, fixed(env)).URL, nil)
	_, err := client.OpenExceptions(context.Background(), ports.ExceptionContextQuery{})
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "db is down") {
		t.Fatalf("error = %v, want the peer's status and detail", err)
	}
}

func TestOpenExceptions_ConnectionRefusedIsUnavailable(t *testing.T) {
	_, err := NewExceptionClient("http://127.0.0.1:1", nil).OpenExceptions(context.Background(), ports.ExceptionContextQuery{})
	if !errors.Is(err, ports.ErrExceptionContextUnavailable) {
		t.Fatalf("error = %v, want ErrExceptionContextUnavailable", err)
	}
}

func TestOpenExceptions_CardServerErrorIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(srv.Close)
	_, err := NewExceptionClient(srv.URL, nil).OpenExceptions(context.Background(), ports.ExceptionContextQuery{})
	if !errors.Is(err, ports.ErrExceptionContextUnavailable) {
		t.Fatalf("error = %v, want ErrExceptionContextUnavailable", err)
	}
}

func TestOpenExceptions_SlowPeerHonoursTheCallerDeadline(t *testing.T) {
	release := make(chan struct{})
	peer := newExceptionPeer(t, func(ctx context.Context) any {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return exceptionsEnvelope()
	})
	t.Cleanup(func() { close(release) })
	client := NewExceptionClient(peer.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client.OpenExceptions(ctx, ports.ExceptionContextQuery{})
	if !errors.Is(err, ports.ErrExceptionContextUnavailable) {
		t.Fatalf("error = %v, want ErrExceptionContextUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("lookup took %s, the deadline was not honoured", elapsed)
	}
}

func TestOpenExceptions_ClientSurvivesTheCancelledContextOfAnEarlierCall(t *testing.T) {
	// The use case gives every lookup its own short-lived context and
	// cancels it afterwards; the cached protocol client must outlive it.
	client := NewExceptionClient(newExceptionPeer(t, fixed(exceptionsEnvelope(caseWire("case-1", typeStuck, "q", "LOW", false)))).URL, nil)
	for i := range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		got, err := client.OpenExceptions(ctx, ports.ExceptionContextQuery{})
		cancel()
		if err != nil || got.OpenCount != 1 {
			t.Fatalf("call %d: %+v %v", i, got, err)
		}
	}
}

func TestExceptionClientAnswersAreNeverMarshalledWithThePeerPayload(t *testing.T) {
	client := NewExceptionClient(newExceptionPeer(t, fixed(exceptionsEnvelope(caseWire("case-1", typeStuck, "q", "LOW", false)))).URL, nil)
	got, err := client.OpenExceptions(context.Background(), ports.ExceptionContextQuery{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	for _, leaked := range []string{"DuplicateSscc", "sscc", "rcpt-1", "audit"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("summary %s leaks peer payload %q", raw, leaked)
		}
	}
}
