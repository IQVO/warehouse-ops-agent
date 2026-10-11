package main_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/llm/anthropic"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// standPlan is the plan the stand-in model "submits".
type standPlan struct {
	action    string
	heads     int
	rationale string
}

// fakeAnthropic is an in-process stand-in for the Anthropic Messages API: a
// loopback httptest server that answers the real anthropic.Reasoner adapter's
// requests. It lets the acceptance suite drive ADR 0011's retry, timeout and
// circuit breaker through the REAL adapter without any model or external
// network. It never sees a real API key.
type fakeAnthropic struct {
	srv *httptest.Server

	mu         sync.Mutex
	hits       int
	plan       standPlan
	failStatus int
	failFirst  int
	failAlways bool
	delay      time.Duration
}

func newFakeAnthropic(plan standPlan) *fakeAnthropic {
	f := &fakeAnthropic{plan: plan}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeAnthropic) close() { f.srv.Close() }

func (f *fakeAnthropic) received() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func (f *fakeAnthropic) handle(rw http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)

	f.mu.Lock()
	f.hits++
	fail := f.failAlways || f.hits <= f.failFirst
	status, delay, plan := f.failStatus, f.delay, f.plan
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}

	rw.Header().Set("Content-Type", "application/json")
	if fail {
		rw.WriteHeader(status)
		_, _ = rw.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"stand-in failure"}}`))
		return
	}
	_ = json.NewEncoder(rw).Encode(map[string]any{
		"model":       "stand-in-model",
		"stop_reason": "tool_use",
		"content": []map[string]any{{
			"type": "tool_use",
			"id":   "toolu_stand_in",
			"name": "submit_plan",
			"input": map[string]any{
				"recommendedAction": plan.action,
				"proposedHeads":     plan.heads,
				"rationale":         plan.rationale,
			},
		}},
	})
}

// newReasoner builds the production adapter pointed at the stand-in. timeout
// and cooldown of zero keep the adapter's own defaults.
func (f *fakeAnthropic) newReasoner(timeout, cooldown time.Duration) (ports.Reasoner, error) {
	return anthropic.New(anthropic.Config{
		APIKey:          "bdd-stand-in-key",
		BaseURL:         f.srv.URL,
		Timeout:         timeout,
		BreakerCooldown: cooldown,
		HTTPClient:      f.srv.Client(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, noopToolInvoker{})
}

// noopToolInvoker is the ToolInvoker the reasoner is handed; the stand-in
// model never asks for a tool, and this fake would refuse if it did.
type noopToolInvoker struct{}

func (noopToolInvoker) Invoke(context.Context, string, string, map[string]any) (string, error) {
	return "", errNotUsed
}
