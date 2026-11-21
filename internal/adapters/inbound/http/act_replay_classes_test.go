package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The replay-class proofs on the REAL routes (ADR 0033): the fleet's first
// OUTCOME refusals — an approval that spends the confirmation token's nonce
// (its commit, in-process) and is then refused by the peer — and the propose
// routes' RETRYABLE refusals, which read live peer state and store nothing.

// TestReleaseApprovalRefusalAfterTheNonceIsSpentIsAnOutcome proves the
// no-pending-work refusal is an OUTCOME: the nonce was consumed before the
// delegation (usecases/release_work.go Approve: consume, then
// releaser.ReleaseNextWork), so the state exists after the refusal (a second
// execution under a new key can only answer token-already-used), the refusal
// is stored under its key, and a replay adds nothing.
func TestReleaseApprovalRefusalAfterTheNonceIsSpentIsAnOutcome(t *testing.T) {
	rig := newActRig(t, recommendRelease)
	rig.releaser.err = ports.ErrNoPendingWork
	body := `{"token":"` + mintToken(t, rig) + `"}`
	key := freshKey()

	first := rig.postKeyed(approvalsPath, "application/json", key, body)
	if first.Code != http.StatusConflict {
		t.Fatalf("first answer = %d %s, want 409 no-pending-work", first.Code, first.Body)
	}
	if first.Header().Get("Idempotency-Replayed") != "" {
		t.Error("the first answer carries Idempotency-Replayed")
	}

	// The state the command committed exists after the refusal: re-executing
	// under a NEW key does not reach the peer again — the token is spent.
	second := rig.postKeyed(approvalsPath, "application/json", freshKey(), body)
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "confirmation token already used") {
		t.Errorf("re-execution under a new key = %d %s, want 409 token already used (the nonce was consumed)", second.Code, second.Body)
	}
	if rig.releaser.calls.Load() != 1 {
		t.Errorf("releaser called %d times, want 1: a spent token never delegates again", rig.releaser.calls.Load())
	}

	// A replay under the original key answers the stored refusal, byte for
	// byte, and records nothing new.
	replay := rig.postKeyed(approvalsPath, "application/json", key, body)
	if replay.Code != http.StatusConflict || replay.Body.String() != first.Body.String() {
		t.Errorf("replay = %d %s, want the stored %d answer %s", replay.Code, replay.Body, first.Code, first.Body)
	}
	if replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Error("the replay does not carry Idempotency-Replayed: true")
	}
	if rig.releaser.calls.Load() != 1 {
		t.Errorf("releaser called %d times after the replay, want 1: a replay adds nothing", rig.releaser.calls.Load())
	}
}

// TestLaborApprovalRefusalAfterTheNonceIsSpentIsAnOutcome is the second
// OUTCOME: assign_labor's peer refusal (assignment-rejected) arrives after
// the labor token's nonce was consumed (usecases/labor_reallocation.go
// Approve: consume, then assigner.AssignLabor).
func TestLaborApprovalRefusalAfterTheNonceIsSpentIsAnOutcome(t *testing.T) {
	rig := newReallocRig(t)
	rig.assigner.err = ports.ErrAssignmentRejected
	p := proposeOK(t, rig, reallocPropose).Proposals[0]
	body := `{"token":"` + p.Token + `","approvedBy":"supervisor-1"}`
	key := freshKey()

	first := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
	if first.Code != http.StatusConflict || !strings.Contains(first.Body.String(), "workforce-management rejected the move") {
		t.Fatalf("first answer = %d %s, want 409 assignment-rejected", first.Code, first.Body)
	}

	second := rig.postKeyed(approvePath(p.ProposalId), "application/json", freshKey(), body)
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "confirmation token already used") {
		t.Errorf("re-execution under a new key = %d %s, want 409 token already used (the nonce was consumed)", second.Code, second.Body)
	}
	if rig.assigner.calls.Load() != 1 {
		t.Errorf("assigner called %d times, want 1", rig.assigner.calls.Load())
	}

	replay := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
	if replay.Code != http.StatusConflict || replay.Body.String() != first.Body.String() || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Errorf("replay = %d %s replayed=%q, want the stored 409 answer with Idempotency-Replayed: true",
			replay.Code, replay.Body, replay.Header().Get("Idempotency-Replayed"))
	}
	if rig.assigner.calls.Load() != 1 {
		t.Errorf("assigner called %d times after the replay, want 1: a replay adds nothing", rig.assigner.calls.Load())
	}
}

// TestProposalRefusalOnLivePeerStateIsRetryableAndReleasesTheKey proves the
// propose routes' refusals are RETRYABLE: the proposal writes nothing and the
// refusal reads the peer's live decision, so nothing is stored, the key is
// released, and the same key retries once the state changes.
func TestProposalRefusalOnLivePeerStateIsRetryableAndReleasesTheKey(t *testing.T) {
	decider := &actDecider{decision: policy.Decision{
		RecommendedAction: policy.ActionAssignLabor, // anything but release_next_work
		Rationale:         "hold the line",
	}}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := usecases.NewReleaseWork(decider, signer, &actReleaser{}, nil, clock)
	if err != nil {
		t.Fatal(err)
	}
	store := newIdempotencyStore(t)
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{DailyBrief: newTestDailyBrief(), ReleaseWork: uc, Idempotency: store}, "warehouse-ops-agent-test")

	key := freshKey()
	send := func(k, body string) (code int, replayed, responseBody string) {
		req := httptest.NewRequest(http.MethodPost, proposalsPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", k)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code, rec.Header().Get("Idempotency-Replayed"), rec.Body.String()
	}

	code, replayed, _ := send(key, proposeBody)
	if code != http.StatusConflict {
		t.Fatalf("first answer = %d, want 409 not-recommended", code)
	}
	if replayed != "" {
		t.Error("the first answer carries Idempotency-Replayed")
	}
	if store.Held(key) {
		t.Fatal("the key is still held after a RETRYABLE refusal: it must be released, nothing stored")
	}

	// The state changes (the peer now recommends the release) and the SAME
	// key with the SAME body executes again and succeeds — then it is stored.
	decider.decision = recommendRelease
	code, replayed, body := send(key, proposeBody)
	if code != http.StatusOK || replayed != "" {
		t.Fatalf("retry under the released key = %d replayed=%q, want a fresh 200 (the handler ran again)", code, replayed)
	}
	if !store.Held(key) {
		t.Fatal("the success is not stored under the key")
	}
	code, replayed, again := send(key, proposeBody)
	if code != http.StatusOK || replayed != "true" || again != body {
		t.Errorf("third request = %d replayed=%q, want the stored 200 replayed byte for byte", code, replayed)
	}
}
