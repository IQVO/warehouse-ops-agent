package http_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/memory"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The act slice's Idempotency-Key contract (ADR 0030, fleet REST guideline
// section 2) and the deprecated verb route (ADR 0031), through the real chi
// router with the real in-memory key store cmd/agent wires.

var keySeq atomic.Int64

// freshKey mints a new Idempotency-Key: one per logical submit.
func freshKey() string { return fmt.Sprintf("test-key-%d", keySeq.Add(1)) }

func newIdempotencyStore(t *testing.T) *memory.IdempotencyStore {
	t.Helper()
	s, err := memory.NewIdempotencyStore(24*time.Hour, 64, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem body: %s", rec.Body)
	}
	return p.Type
}

const problemBase = "https://problems.warehouse-ops-agent.io/"

// keyedRoutes are every POST route the guideline covers: all four keyed.
func keyedRoutes() []string {
	return []string{proposalsPath, approvalsPath, reallocProposePath, approvePath("lrp-x")}
}

func bothSlicesRouter(t *testing.T) http.Handler {
	t.Helper()
	act := newActRig(t, recommendRelease)
	realloc := newReallocRig(t)
	mux := http.NewServeMux()
	mux.Handle("/act/", act.router)
	mux.Handle("/labor-reallocation/", realloc.router)
	return mux
}

func TestEveryActPOSTRequiresAnIdempotencyKey(t *testing.T) {
	router := bothSlicesRouter(t)
	for _, p := range keyedRoutes() {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || problemType(t, rec) != problemBase+"idempotency-key-required" {
			t.Errorf("%s without a key = %d %s, want 400 idempotency-key-required", p, rec.Code, rec.Body)
		}
	}
}

func TestAMalformedIdempotencyKeyIs400Invalid(t *testing.T) {
	router := bothSlicesRouter(t)
	for _, p := range keyedRoutes() {
		for name, values := range map[string][]string{
			"empty":         {""},
			"129 chars":     {strings.Repeat("a", 129)},
			"edge space":    {" abc"},
			"control char":  {"a\x01b"},
			"two values":    {"k1", "k2"},
			"non-ASCII key": {"chave-ç"},
		} {
			req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header["Idempotency-Key"] = values
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || problemType(t, rec) != problemBase+"idempotency-key-invalid" {
				t.Errorf("%s %s = %d %s, want 400 idempotency-key-invalid", p, name, rec.Code, rec.Body)
			}
		}
	}
}

func TestKeyedRoutesAnswer503WithoutAKeyStore(t *testing.T) {
	router := inboundhttp.NewRouter(&inboundhttp.Handlers{DailyBrief: newTestDailyBrief()}, "t")
	for _, p := range keyedRoutes() {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", freshKey())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "idempotency") {
			t.Errorf("%s with no key store = %d %s, want 503 naming the missing store", p, rec.Code, rec.Body)
		}
	}
}

func TestAnApprovalRetriedWithItsKeyReplaysTheFirstAnswer(t *testing.T) {
	rig := newReallocRig(t)
	p := proposeOK(t, rig, reallocPropose).Proposals[0]
	body := `{"token":"` + p.Token + `","approvedBy":"supervisor-1"}`
	key := freshKey()
	first := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
	if first.Code != http.StatusOK || first.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("first = %d %s replayed=%q, want 200 and no replay header", first.Code, first.Body, first.Header().Get("Idempotency-Replayed"))
	}
	again := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
	if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %s, want the byte-identical first answer %s", again.Code, again.Body, first.Body)
	}
	if again.Header().Get("Idempotency-Replayed") != "true" || again.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("replay headers = %v, want Idempotency-Replayed: true and the stored Content-Type", again.Header())
	}
	if rig.assigner.calls.Load() != 1 {
		t.Fatalf("peer calls = %d, want exactly 1 (the retry must not delegate again)", rig.assigner.calls.Load())
	}
}

func TestAReleaseApprovalRetriedWithItsKeyDelegatesOnce(t *testing.T) {
	rig := newActRig(t, recommendRelease)
	body := `{"token":"` + mintToken(t, rig) + `"}`
	key := freshKey()
	first := rig.postKeyed(approvalsPath, "application/json", key, body)
	again := rig.postKeyed(approvalsPath, "application/json", key, body)
	if first.Code != http.StatusOK || again.Code != http.StatusOK || again.Body.String() != first.Body.String() || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("first = %d %s, again = %d %s %v", first.Code, first.Body, again.Code, again.Body, again.Header())
	}
	if rig.releaser.calls.Load() != 1 {
		t.Fatalf("releaser calls = %d, want 1", rig.releaser.calls.Load())
	}
}

func TestAProposalRetriedWithItsKeyReturnsTheSameTokens(t *testing.T) {
	rig := newReallocRig(t)
	key := freshKey()
	first := rig.postKeyed(reallocProposePath, "application/json", key, reallocPropose)
	again := rig.postKeyed(reallocProposePath, "application/json", key, reallocPropose)
	if first.Code != http.StatusOK || again.Body.String() != first.Body.String() || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("first = %d %s, again = %d %s", first.Code, first.Body, again.Code, again.Body)
	}
	fresh := rig.postJSON(reallocProposePath, reallocPropose)
	if fresh.Body.String() == first.Body.String() {
		t.Fatal("a NEW key must run the proposal again and mint new tokens")
	}
}

func TestAKeyReusedWithAnotherRequestIs409(t *testing.T) {
	rig := newReallocRig(t)
	key := freshKey()
	if rec := rig.postKeyed(reallocProposePath, "application/json", key, reallocPropose); rec.Code != http.StatusOK {
		t.Fatalf("first = %d %s", rec.Code, rec.Body)
	}
	other := strings.Replace(reallocPropose, `"S1"`, `"S2"`, 1)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"another body": rig.postKeyed(reallocProposePath, "application/json", key, other),
		"another path": rig.postKeyed(approvePath("lrp-x"), "application/json", key, reallocPropose),
	} {
		if rec.Code != http.StatusConflict || problemType(t, rec) != problemBase+"idempotency-key-reused" {
			t.Errorf("%s = %d %s, want 409 idempotency-key-reused", name, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "Idempotency-Key was already used with a different request") {
			t.Errorf("%s title = %s", name, rec.Body)
		}
	}
}

func TestABusinessRejectionIsStoredAndReplayed(t *testing.T) {
	rig := newReallocRig(t)
	p := proposeOK(t, rig, reallocPropose).Proposals[0]
	body := `{"token":"` + p.Token + `x","approvedBy":"sup"}`
	key := freshKey()
	first := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
	again := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
	if first.Code != http.StatusBadRequest || again.Code != http.StatusBadRequest || again.Body.String() != first.Body.String() || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("first = %d %s, again = %d %s %v, want the 400 replayed", first.Code, first.Body, again.Code, again.Body, again.Header())
	}
}

func TestAnUpstreamFailureIsNotStoredSoTheRetryReExecutes(t *testing.T) {
	rig := newReallocRig(t)
	rig.assigner.err = ports.ErrWorkforceAgentUnreachable
	p := proposeOK(t, rig, reallocPropose).Proposals[0]
	body := `{"token":"` + p.Token + `","approvedBy":"sup"}`
	key := freshKey()
	if first := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body); first.Code != http.StatusBadGateway {
		t.Fatalf("first = %d %s, want 502", first.Code, first.Body)
	}
	again := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
	if again.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("a 5xx must never be replayed")
	}
	// The token was consumed before delegating (ADR 0027 at-most-once), so the
	// re-execution answers token-already-used: never a second delegation.
	if again.Code != http.StatusConflict || rig.assigner.calls.Load() != 1 {
		t.Fatalf("retry = %d %s with %d peer calls, want a fresh 409 and 1 call", again.Code, again.Body, rig.assigner.calls.Load())
	}
}

func TestConcurrentApprovalsWithOneKeyDelegateOnce(t *testing.T) {
	rig := newReallocRig(t)
	p := proposeOK(t, rig, reallocPropose).Proposals[0]
	body := `{"token":"` + p.Token + `","approvedBy":"sup"}`
	key := freshKey()
	rig.assigner.gate = make(chan struct{})
	const n = 12
	var wg sync.WaitGroup
	codes := make([]int, n)
	bodies := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := rig.postKeyed(approvePath(p.ProposalId), "application/json", key, body)
			codes[i], bodies[i] = rec.Code, rec.Body.String()
		}()
	}
	time.Sleep(50 * time.Millisecond) // every request is in flight on the one key
	close(rig.assigner.gate)
	wg.Wait()
	for i := range n {
		if codes[i] != http.StatusOK || bodies[i] != bodies[0] {
			t.Fatalf("request %d = %d %s, want every caller to get the one 200", i, codes[i], bodies[i])
		}
	}
	if rig.assigner.calls.Load() != 1 {
		t.Fatalf("peer calls = %d, want exactly 1", rig.assigner.calls.Load())
	}
}

func TestAnOversizedKeyedBodyIs413(t *testing.T) {
	rig := newReallocRig(t)
	big := `{"siteCode":"` + strings.Repeat("x", 9<<10) + `"}`
	if rec := rig.postKeyed(reallocProposePath, "application/json", freshKey(), big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized keyed body = %d, want 413", rec.Code)
	}
}

// ADR 0031: the verb route keeps working, keyless as before, and says it is
// deprecated on every answer, errors included.
func TestTheDeprecatedApproveRouteStillWorksAndAnnouncesItsSunset(t *testing.T) {
	rig := newReallocRig(t)
	p := proposeOK(t, rig, reallocPropose).Proposals[0]
	rec := rig.postKeyed(deprecatedApprovePath(p.ProposalId), "application/json", "", `{"token":"`+p.Token+`","approvedBy":"sup"}`)
	if rec.Code != http.StatusOK || rig.assigner.calls.Load() != 1 {
		t.Fatalf("deprecated approve = %d %s (%d peer calls), want the old 200", rec.Code, rec.Body, rig.assigner.calls.Load())
	}
	assertDeprecated(t, rec)
	if bad := rig.postKeyed(deprecatedApprovePath(p.ProposalId), "text/plain", "", "x"); bad.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("deprecated error answer = %d, want 415", bad.Code)
	} else {
		assertDeprecated(t, bad)
	}
	fresh := rig.postJSON(approvePath(p.ProposalId), `{}`)
	if fresh.Header().Get("Deprecation") != "" || fresh.Header().Get("Sunset") != "" {
		t.Fatalf("the guideline route must not be marked deprecated: %v", fresh.Header())
	}
}

func assertDeprecated(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()
	if h.Get("Deprecation") != "@1794268800" || h.Get("Sunset") != "Wed, 10 Feb 2027 00:00:00 GMT" || h.Get("Link") != `<approvals>; rel="successor-version"` {
		t.Fatalf("deprecation headers = Deprecation %q, Sunset %q, Link %q", h.Get("Deprecation"), h.Get("Sunset"), h.Get("Link"))
	}
}
