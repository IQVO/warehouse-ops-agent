package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type fakeInvoker struct {
	calls []string
	out   map[string]string
	err   error
}

func (f *fakeInvoker) Invoke(_ context.Context, upstream, tool string, args map[string]any) (string, error) {
	f.calls = append(f.calls, upstream+"/"+tool)
	if f.err != nil {
		return "", f.err
	}
	return f.out[tool], nil
}

// scripted serves one canned Messages API response per call, in order,
// and records every request body so tests can assert what the model saw.
type scripted struct {
	t         *testing.T
	responses []string
	requests  []apiRequest
	status    int
}

func (s *scripted) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" || r.URL.Path != "/v1/messages" {
			s.t.Errorf("bad request shape: %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("x-api-key"))
		}
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.t.Fatalf("decode request: %v", err)
		}
		s.requests = append(s.requests, req)
		i := len(s.requests) - 1
		if s.status != 0 {
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`))
			return
		}
		if i >= len(s.responses) {
			s.t.Fatalf("unexpected call #%d", i+1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.responses[i]))
	}
}

func brief() ports.Brief {
	return ports.Brief{
		UseCase:        "flow_balance_advisory",
		Question:       "What should the operator do on path pick?",
		Facts:          map[string]any{"wes-work-planning.get_rebalance_recommendation": map[string]any{"action": "ReassignLabor", "backlogDepth": 40}},
		AllowedActions: []string{"assign_labor", "release_next_work", "hold"},
		Tools: []ports.ToolSpec{{
			Upstream: "workforce-management", Name: "get_staffing_gap", Description: "Planned vs active heads on a path.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pathId": map[string]any{"type": "string"}}},
		}},
	}
}

func newReasoner(t *testing.T, srv *httptest.Server, inv ports.ToolInvoker) *Reasoner {
	t.Helper()
	r, err := New(Config{APIKey: "k", BaseURL: srv.URL, Timeout: 5 * time.Second, MaxTurns: 3}, inv)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNew_Validation(t *testing.T) {
	if _, err := New(Config{}, &fakeInvoker{}); err == nil {
		t.Fatal("missing API key must fail")
	}
	if _, err := New(Config{APIKey: "k"}, nil); err == nil {
		t.Fatal("missing invoker must fail")
	}
	r, _ := New(Config{APIKey: "k"}, &fakeInvoker{})
	if r.cfg.Model != defaultModel || r.cfg.BaseURL != defaultBaseURL || r.cfg.MaxTurns != defaultMaxTurns {
		t.Fatalf("defaults not applied: %+v", r.cfg)
	}
}

func TestReason_ToolUseThenSubmit(t *testing.T) {
	s := &scripted{t: t, responses: []string{
		`{"model":"claude-sonnet-4-5-20250929","stop_reason":"tool_use","content":[{"type":"text","text":"Let me check staffing."},{"type":"tool_use","id":"tu_1","name":"workforce-management__get_staffing_gap","input":{"pathId":"pick"}}]}`,
		`{"model":"claude-sonnet-4-5-20250929","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_2","name":"submit_plan","input":{"recommendedAction":"assign_labor","proposedHeads":2,"rationale":"WES asks to reassign and WFM confirms a 2-head gap."}}]}`,
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	inv := &fakeInvoker{out: map[string]string{"get_staffing_gap": `{"plannedHeads":5,"activeHeads":3,"understaffed":true}`}}

	plan, err := newReasoner(t, srv, inv).Reason(context.Background(), brief())
	if err != nil {
		t.Fatal(err)
	}
	assertSubmittedPlan(t, plan)
	assertInvokerAuditTrail(t, inv, plan)
	// The second request must carry the tool_result back to the model.
	second := s.requests[1]
	assertToolResultEchoed(t, second)
	// Tool catalogue: the MCP tool plus submit_plan, nothing else.
	assertToolCatalogue(t, second)
	assertPromptsCarryVocabularyAndFacts(t, second)
}

// assertSubmittedPlan checks the plan the model submitted through
// submit_plan, including the captured model id.
func assertSubmittedPlan(t *testing.T, plan ports.Plan) {
	t.Helper()
	if plan.RecommendedAction != "assign_labor" || plan.ProposedHeads != 2 || !strings.Contains(plan.Rationale, "2-head") {
		t.Fatalf("unexpected plan %+v", plan)
	}
	if plan.Model != "claude-sonnet-4-5-20250929" {
		t.Fatalf("model not captured: %q", plan.Model)
	}
}

// assertInvokerAuditTrail checks the plan's tool-call audit: exactly one
// invocation, of the offered workforce-management tool, with its args and
// ok outcome recorded.
func assertInvokerAuditTrail(t *testing.T, inv *fakeInvoker, plan ports.Plan) {
	t.Helper()
	if len(inv.calls) != 1 || inv.calls[0] != "workforce-management/get_staffing_gap" {
		t.Fatalf("invoker calls %v", inv.calls)
	}
	if len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Outcome != "ok" || plan.ToolCalls[0].Args["pathId"] != "pick" {
		t.Fatalf("audit trail %+v", plan.ToolCalls)
	}
}

// assertToolResultEchoed checks that the follow-up request carries the
// invoker's output back to the model as a tool_result.
func assertToolResultEchoed(t *testing.T, second apiRequest) {
	t.Helper()
	last := second.Messages[len(second.Messages)-1]
	if last.Role != "user" || len(last.Content) != 1 || last.Content[0].Type != "tool_result" || last.Content[0].ToolUseID != "tu_1" || !strings.Contains(last.Content[0].Content, "understaffed") {
		t.Fatalf("tool_result not echoed: %+v", last)
	}
}

// assertToolCatalogue checks the tools offered to the model: the MCP tool
// plus submit_plan, nothing else.
func assertToolCatalogue(t *testing.T, second apiRequest) {
	t.Helper()
	names := []string{}
	for _, tl := range second.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "workforce-management__get_staffing_gap,submit_plan" {
		t.Fatalf("tools offered: %v", names)
	}
}

// assertPromptsCarryVocabularyAndFacts checks the system and user prompts
// carry the action vocabulary and the gathered facts.
func assertPromptsCarryVocabularyAndFacts(t *testing.T, second apiRequest) {
	t.Helper()
	if !strings.Contains(second.System, "hold") || !strings.Contains(second.Messages[0].Content[0].Text, "ReassignLabor") {
		t.Fatal("system/user prompts must carry the vocabulary and the facts")
	}
}

func TestReason_ToolErrorIsReportedNotFatal(t *testing.T) {
	s := &scripted{t: t, responses: []string{
		`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_1","name":"workforce-management__get_staffing_gap","input":{"pathId":"pick"}}]}`,
		`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_2","name":"submit_plan","input":{"recommendedAction":"hold","proposedHeads":0,"rationale":"staffing unknown"}}]}`,
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	inv := &fakeInvoker{err: errors.New("not found")}
	plan, err := newReasoner(t, srv, inv).Reason(context.Background(), brief())
	if err != nil || plan.RecommendedAction != "hold" {
		t.Fatalf("plan %+v err %v", plan, err)
	}
	if plan.ToolCalls[0].Outcome != "not found" {
		t.Fatalf("audit must record the failure: %+v", plan.ToolCalls)
	}
	if res := s.requests[1].Messages[len(s.requests[1].Messages)-1].Content[0]; !res.IsError || res.Content != "not found" {
		t.Fatalf("tool_result must be flagged is_error: %+v", res)
	}
}

func TestReason_UnknownToolIsRefused(t *testing.T) {
	s := &scripted{t: t, responses: []string{
		`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_1","name":"shell__exec","input":{"cmd":"rm -rf /"}}]}`,
		`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_2","name":"submit_plan","input":{"recommendedAction":"hold","proposedHeads":0,"rationale":"x"}}]}`,
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	inv := &fakeInvoker{}
	plan, err := newReasoner(t, srv, inv).Reason(context.Background(), brief())
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.calls) != 0 {
		t.Fatal("an un-offered tool must never reach the invoker")
	}
	if plan.ToolCalls[0].Outcome != "refused: unknown tool" {
		t.Fatalf("audit %+v", plan.ToolCalls)
	}
}

func TestReason_ProseOnlyIsNudgedThenBounded(t *testing.T) {
	prose := `{"stop_reason":"end_turn","content":[{"type":"text","text":"I think you should hold."}]}`
	s := &scripted{t: t, responses: []string{prose, prose, prose}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	_, err := newReasoner(t, srv, &fakeInvoker{}).Reason(context.Background(), brief())
	if err == nil || !strings.Contains(err.Error(), "no plan submitted within 3 turns") {
		t.Fatalf("want bounded failure, got %v", err)
	}
	if len(s.requests) != 3 {
		t.Fatalf("want exactly MaxTurns calls, got %d", len(s.requests))
	}
}

func TestReason_APIErrorSurfaces(t *testing.T) {
	s := &scripted{t: t, status: 529}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	_, err := newReasoner(t, srv, &fakeInvoker{}).Reason(context.Background(), brief())
	if err == nil || !strings.Contains(err.Error(), "529") || !strings.Contains(err.Error(), "overloaded_error") {
		t.Fatalf("got %v", err)
	}
}

func TestReason_TimeoutIsHonoured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	r, _ := New(Config{APIKey: "k", BaseURL: srv.URL, Timeout: 100 * time.Millisecond}, &fakeInvoker{})
	start := time.Now()
	_, err := r.Reason(context.Background(), brief())
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("timeout not honoured: err=%v took=%s", err, time.Since(start))
	}
}

func TestReason_BadSubmitInput(t *testing.T) {
	s := &scripted{t: t, responses: []string{
		`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_2","name":"submit_plan","input":"not an object"}]}`,
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	if _, err := newReasoner(t, srv, &fakeInvoker{}).Reason(context.Background(), brief()); err == nil || !strings.Contains(err.Error(), "submit_plan input") {
		t.Fatalf("got %v", err)
	}
}
