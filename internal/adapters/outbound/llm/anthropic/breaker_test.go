package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gobreaker "github.com/sony/gobreaker/v2"
)

// recordingRecorder implements resilience.StateRecorder, capturing every
// state transition in order (mirrors order-management's
// productclassification/breaker_test.go recordingRecorder).
type recordingRecorder struct {
	states []int64
}

func (r *recordingRecorder) SetState(_ string, state int64) {
	r.states = append(r.states, state)
}

func (r *recordingRecorder) last() int64 {
	if len(r.states) == 0 {
		return -1
	}
	return r.states[len(r.states)-1]
}

// gobreaker.State's own numbering (0=closed,1=half-open,2=open),
// duplicated as untyped constants for the same reason
// resilience.RecordStateChange documents needing no translation table.
const (
	gobreakerClosed = 0
	gobreakerOpen   = 2
)

const holdPlan = `{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_1","name":"submit_plan","input":{"recommendedAction":"hold","proposedHeads":0,"rationale":"x"}}]}`

// scriptedStatus serves a fixed HTTP status/body for every request and
// counts how many times it was hit — a fake HTTP transport that fails
// (or succeeds) deterministically, per the testing brief's "fake HTTP
// transport that fails N times then succeeds" requirement.
type scriptedStatus struct {
	calls  int32
	status func(n int32) (int, string)
}

func (s *scriptedStatus) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&s.calls, 1)
		status, body := s.status(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func alwaysServerError(_ int32) (int, string) {
	return http.StatusInternalServerError, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`
}

func alwaysBadRequest(_ int32) (int, string) {
	return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"bad key"}}`
}

// TestCall_RetriesTransientErrorsUpToMaxAttempts is the ADR-0011 retry
// acceptance test: a server that always 500s is retried exactly
// maxRetryAttempts (3) times per Reason() call.
func TestCall_RetriesTransientErrorsUpToMaxAttempts(t *testing.T) {
	s := &scriptedStatus{status: alwaysServerError}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	r, err := New(Config{APIKey: "k", BaseURL: srv.URL, Timeout: 5 * time.Second, MaxTurns: 1}, &fakeInvoker{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reason(context.Background(), brief()); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if got := atomic.LoadInt32(&s.calls); got != maxRetryAttempts {
		t.Fatalf("server calls = %d, want exactly %d (1 original + %d retries)", got, maxRetryAttempts, maxRetryAttempts-1)
	}
}

// TestCall_SucceedsAfterTransientFailures proves N failures then success
// is transparently retried into a successful result.
func TestCall_SucceedsAfterTransientFailures(t *testing.T) {
	s := &scriptedStatus{status: func(n int32) (int, string) {
		if n < 3 {
			return http.StatusInternalServerError, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`
		}
		return http.StatusOK, holdPlan
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	r, err := New(Config{APIKey: "k", BaseURL: srv.URL, Timeout: 5 * time.Second, MaxTurns: 1}, &fakeInvoker{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := r.Reason(context.Background(), brief())
	if err != nil {
		t.Fatalf("Reason: %v", err)
	}
	if plan.RecommendedAction != "hold" {
		t.Fatalf("unexpected plan %+v", plan)
	}
	if got := atomic.LoadInt32(&s.calls); got != 3 {
		t.Fatalf("server calls = %d, want exactly 3 (2 failures + 1 success)", got)
	}
}

// TestCall_4xxIsNotRetried proves a permanent client error (bad API key,
// malformed request) returns on the FIRST attempt — retrying a request
// that can never succeed wastes both money and the call's timeout
// budget.
func TestCall_4xxIsNotRetried(t *testing.T) {
	s := &scriptedStatus{status: alwaysBadRequest}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	r, err := New(Config{APIKey: "k", BaseURL: srv.URL, Timeout: 5 * time.Second, MaxTurns: 1}, &fakeInvoker{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reason(context.Background(), brief()); err == nil {
		t.Fatal("expected an error")
	}
	if got := atomic.LoadInt32(&s.calls); got != 1 {
		t.Fatalf("server calls = %d, want exactly 1 -- a 4xx must not be retried", got)
	}
}

// TestCall_OpensAfterConsecutiveFailures proves the breaker trips after
// 5 consecutive failing Reason() calls (each internally already retried
// 3x, so the breaker sees ONE failure per call, not three), and once
// OPEN the server is never reached again — calls short-circuit
// immediately with a breaker-rejection error.
func TestCall_OpensAfterConsecutiveFailures(t *testing.T) {
	s := &scriptedStatus{status: alwaysServerError}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	recorder := &recordingRecorder{}
	r, err := New(Config{APIKey: "k", BaseURL: srv.URL, Timeout: 5 * time.Second, MaxTurns: 1, BreakerRecorder: recorder}, &fakeInvoker{})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		if _, err := r.Reason(context.Background(), brief()); err == nil {
			t.Fatalf("call %d: expected an error (server always fails)", i)
		}
	}
	if recorder.last() != gobreakerOpen {
		t.Fatalf("breaker state after 5 failing calls = %d, want open (%d)", recorder.last(), gobreakerOpen)
	}
	callsBeforeOpen := atomic.LoadInt32(&s.calls)
	if callsBeforeOpen != 5*maxRetryAttempts {
		t.Fatalf("server calls before open = %d, want exactly %d (5 calls x %d attempts each)", callsBeforeOpen, 5*maxRetryAttempts, maxRetryAttempts)
	}

	// Once open, the server must never be reached again, and the error
	// must be recognizable as a breaker rejection.
	_, err = r.Reason(context.Background(), brief())
	if err == nil {
		t.Fatal("expected a breaker-open error")
	}
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected errors.Is(err, gobreaker.ErrOpenState), got %v", err)
	}
	if atomic.LoadInt32(&s.calls) != callsBeforeOpen {
		t.Fatal("server was called again while the breaker is open -- it must short-circuit instead")
	}
}

// TestCall_HalfOpenRecoversToClosed proves the full state-machine round
// trip: 5 tripping failures open the breaker, then after the cooldown a
// half-open probe reaches the now-healthy server and closes the
// breaker.
func TestCall_HalfOpenRecoversToClosed(t *testing.T) {
	var failUntil int32 = 5 * maxRetryAttempts
	s := &scriptedStatus{status: func(n int32) (int, string) {
		if n <= atomic.LoadInt32(&failUntil) {
			return http.StatusInternalServerError, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`
		}
		return http.StatusOK, holdPlan
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	recorder := &recordingRecorder{}
	r, err := New(Config{
		APIKey: "k", BaseURL: srv.URL, Timeout: 5 * time.Second, MaxTurns: 1,
		BreakerRecorder: recorder, BreakerCooldown: 100 * time.Millisecond,
	}, &fakeInvoker{})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		if _, err := r.Reason(context.Background(), brief()); err == nil {
			t.Fatalf("call %d: expected an error (server still failing)", i)
		}
	}
	if recorder.last() != gobreakerOpen {
		t.Fatalf("breaker state after 5 failing calls = %d, want open (%d)", recorder.last(), gobreakerOpen)
	}

	time.Sleep(150 * time.Millisecond)

	plan, err := r.Reason(context.Background(), brief())
	if err != nil {
		t.Fatalf("half-open probe: %v", err)
	}
	if plan.RecommendedAction != "hold" {
		t.Fatalf("half-open probe must reach the REAL (now healthy) server, got %+v", plan)
	}
	if recorder.last() != gobreakerClosed {
		t.Fatalf("breaker state after a successful half-open probe = %d, want closed (%d)", recorder.last(), gobreakerClosed)
	}
}
