package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cenkalti/backoff/v4"
	gobreaker "github.com/sony/gobreaker/v2"

	"github.com/claudioed/warehouse-ops-agent/internal/resilience"
)

// call executes one Anthropic Messages API request behind a per-
// dependency circuit breaker, a context-deadline-derived timeout, and a
// bounded jittered retry (ADR-0011) — the resilience wrapper sits around
// the ACTUAL outbound HTTP call, not the whole Reason tool-use loop: each
// turn of that loop invokes this once, so a several-turn conversation
// still only ever contributes one success/failure per turn toward the
// breaker's trip condition, never N (a retry storm inside one turn is
// bounded and counts as a single logical attempt).
//
// A breaker-OPEN (or half-open-and-saturated) rejection is returned as an
// ordinary error, exactly like a transport failure or an exhausted retry
// budget — Reason propagates it unchanged, and the EXISTING fallback
// mechanism (usecases.FlowBalanceAdvisory.arbitrate -> policy.Arbitrate
// treating any Reasoner error as planErr) is what routes LLMOn mode back
// to the deterministic Decision. No second fallback path is added here.
func (r *Reasoner) call(ctx context.Context, req apiRequest) (*apiResponse, error) {
	callCtx, cancel := resilience.CallTimeout(ctx, maxCallTimeout)
	defer cancel()

	resp, err := r.breaker.Execute(func() (*apiResponse, error) {
		return r.retryingRequest(callCtx, req)
	})
	if err != nil {
		if isBreakerRejection(err) {
			return nil, fmt.Errorf("anthropic: circuit breaker open for %s: %w", circuitBreakerDependencyName, err)
		}
		return nil, err
	}
	return resp, nil
}

// retryingRequest retries doRequest with jittered exponential backoff,
// bounded to maxRetryAttempts total attempts and to callCtx's own
// deadline (whichever is tighter). Only a transient error (a transport-
// level failure, or a 5xx apiStatusError) is retried; a 4xx
// apiStatusError (bad API key, malformed request) or a response-decode
// error is permanent and returns on the first attempt — retrying a
// paid LLM call that can never succeed wastes both money and the call's
// timeout budget.
func (r *Reasoner) retryingRequest(callCtx context.Context, req apiRequest) (*apiResponse, error) {
	policy := backoff.NewExponentialBackOff(
		backoff.WithInitialInterval(retryInitialInterval),
		backoff.WithMaxInterval(retryMaxInterval),
	)
	bounded := backoff.WithContext(backoff.WithMaxRetries(policy, maxRetryAttempts-1), callCtx)

	return backoff.RetryNotifyWithData(func() (*apiResponse, error) {
		resp, err := r.doRequest(callCtx, req)
		if err != nil && !isTransientError(err) {
			return nil, backoff.Permanent(err)
		}
		return resp, err
	}, bounded, nil)
}

// transientError marks a failure worth retrying: a transport-level
// problem (dial failure, timeout, connection reset) or a response-body
// read failure, as opposed to a well-formed response the server itself
// rejected.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }

func (e *transientError) Unwrap() error { return e.err }

// apiStatusError carries the Anthropic API's own HTTP status so the
// retry policy can tell a transient server problem (5xx — retry) from a
// permanent client problem (4xx: bad API key, malformed request —
// retrying can never fix it).
type apiStatusError struct {
	Status int
	Err    error
}

func (e *apiStatusError) Error() string { return e.Err.Error() }

func (e *apiStatusError) Unwrap() error { return e.Err }

// isTransientError reports whether err is worth retrying: any
// transientError, or a 5xx apiStatusError. A 4xx apiStatusError and a
// response-decode error (malformed JSON body) are permanent.
func isTransientError(err error) bool {
	var status *apiStatusError
	if errors.As(err, &status) {
		return status.Status >= 500
	}
	var transient *transientError
	return errors.As(err, &transient)
}

// isBreakerRejection reports whether err is gobreaker refusing to even
// attempt the call (open, or half-open and already at its probe limit)
// — the only case where the caller should treat this exactly like any
// other Reasoner failure and let the existing deterministic fallback
// take over.
func isBreakerRejection(err error) bool {
	return errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests)
}

func (r *Reasoner) doRequest(ctx context.Context, req apiRequest) (*apiResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", r.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", apiVersion)
	resp, err := r.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, &transientError{fmt.Errorf("anthropic: messages: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &transientError{fmt.Errorf("anthropic: read: %w", err)}
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("anthropic: decode (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode/100 != 2 {
		msg := "unexpected status"
		if out.Error != nil {
			msg = out.Error.Type + ": " + out.Error.Message
		}
		return nil, &apiStatusError{Status: resp.StatusCode, Err: fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, msg)}
	}
	return &out, nil
}
