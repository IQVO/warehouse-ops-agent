package main_test

import (
	"errors"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Steps for the reasoner path: the policy layer's arbitration (fake
// ports.Reasoner) and ADR 0011's resilience (the real anthropic adapter against
// a loopback stand-in for the Messages API).

// ---------------------------------------------------------------- Given ----

func (w *world) theLLMModeIs(mode string) error {
	parsed, err := policy.ParseLLMMode(mode)
	if err != nil {
		return err
	}
	w.llmMode = parsed
	return nil
}

func (w *world) aReasonerProposes(action string, heads int, rationale string) error {
	w.reasoner = &fakeReasoner{plan: ports.Plan{
		RecommendedAction: action, ProposedHeads: heads, Rationale: rationale, Model: "fake-reasoner",
	}}
	return nil
}

func (w *world) aReasonerFailsWith(message string) error {
	w.reasoner = &fakeReasoner{err: errors.New(message)}
	return nil
}

func (w *world) noReasonerIsWired() error {
	w.reasoner = nil
	return nil
}

func (w *world) anAnthropicStandInSubmits(action string, heads int, rationale string) error {
	plan := standPlan{action: action, heads: heads, rationale: rationale}
	if w.stand == nil {
		w.stand = newFakeAnthropic(plan)
		return nil
	}
	w.stand.mu.Lock()
	defer w.stand.mu.Unlock()
	w.stand.plan = plan
	return nil
}

func (w *world) theStandInFailsForTheFirst(status, n int) error {
	w.stand.mu.Lock()
	defer w.stand.mu.Unlock()
	w.stand.failStatus, w.stand.failFirst = status, n
	return nil
}

func (w *world) theStandInAlwaysFails(status int) error {
	w.stand.mu.Lock()
	defer w.stand.mu.Unlock()
	w.stand.failStatus, w.stand.failAlways = status, true
	return nil
}

func (w *world) theStandInTakes(ms int) error {
	w.stand.mu.Lock()
	defer w.stand.mu.Unlock()
	w.stand.delay = time.Duration(ms) * time.Millisecond
	return nil
}

func (w *world) theStandInRecovers() error {
	w.stand.mu.Lock()
	defer w.stand.mu.Unlock()
	w.stand.failAlways, w.stand.failFirst = false, 0
	return nil
}

func (w *world) theReasonerTimeoutIs(ms int) error {
	w.llmTimeout = time.Duration(ms) * time.Millisecond
	return nil
}

func (w *world) theBreakerCooldownIs(ms int) error {
	w.breakerCooldown = time.Duration(ms) * time.Millisecond
	return nil
}

// theBreakerCooldownElapses is the one real wait in the suite: the breaker's
// OPEN -> half-open transition is driven by its own clock, which the adapter
// exposes only through BreakerCooldown (set short for this scenario).
func (w *world) theBreakerCooldownElapses() error {
	time.Sleep(w.breakerCooldown + 150*time.Millisecond)
	return nil
}

// ----------------------------------------------------------------- Then ----

func (w *world) theReasonerWasConsulted(n int) error {
	if w.reasoner == nil {
		return fmt.Errorf("no reasoner was wired in this scenario")
	}
	w.reasoner.mu.Lock()
	defer w.reasoner.mu.Unlock()
	if w.reasoner.calls != n {
		return fmt.Errorf("expected the reasoner to be consulted %d time(s), got %d", n, w.reasoner.calls)
	}
	return nil
}

func (w *world) theArbitrationMetricsRecorded(source, mode, agreement string) error {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()
	if len(w.metrics.records) == 0 {
		return fmt.Errorf("no arbitration was recorded")
	}
	got := w.metrics.records[len(w.metrics.records)-1]
	if got.useCase != "flow_balance_advisory" || got.source != source || got.mode != mode || agreementWord(got.agree) != agreement {
		return fmt.Errorf("expected source=%q mode=%q agreement=%q for flow_balance_advisory, got source=%q mode=%q agreement=%q for %q",
			source, mode, agreement, got.source, got.mode, agreementWord(got.agree), got.useCase)
	}
	return nil
}

func (w *world) theReasonerBrief() (ports.Brief, error) {
	if w.reasoner == nil {
		return ports.Brief{}, fmt.Errorf("no reasoner was wired in this scenario")
	}
	w.reasoner.mu.Lock()
	defer w.reasoner.mu.Unlock()
	if w.reasoner.calls == 0 {
		return ports.Brief{}, fmt.Errorf("the reasoner was never consulted")
	}
	return w.reasoner.brief, nil
}

func (w *world) theReasonerWasOfferedTheActions(actions string) error {
	brief, err := w.theReasonerBrief()
	if err != nil {
		return err
	}
	if got := quoteList(brief.AllowedActions); got != actions {
		return fmt.Errorf("expected the reasoner to be offered %q, got %q", actions, got)
	}
	return nil
}

func (w *world) theReasonerWasToldUnavailable(source string) error {
	brief, err := w.theReasonerBrief()
	if err != nil {
		return err
	}
	if got := brief.Facts[source]; got != "unavailable" {
		return fmt.Errorf("expected the brief to state %q as unavailable, got %v", source, got)
	}
	return nil
}

func (w *world) theReasonerWasShownTheFact(source string) error {
	brief, err := w.theReasonerBrief()
	if err != nil {
		return err
	}
	got, ok := brief.Facts[source]
	if !ok || got == "unavailable" {
		return fmt.Errorf("expected the brief to carry the fact %q, got %v", source, got)
	}
	return nil
}

func (w *world) theStandInReceived(n int) error {
	if w.stand == nil {
		return fmt.Errorf("no Anthropic API stand-in in this scenario")
	}
	if got := w.stand.received(); got != n {
		return fmt.Errorf("expected the Anthropic API stand-in to receive %d request(s), got %d", n, got)
	}
	return nil
}

func (w *world) theAdvisoryAnsweredWithin(ms int) error {
	if limit := time.Duration(ms) * time.Millisecond; w.elapsed > limit {
		return fmt.Errorf("expected an answer within %s, took %s", limit, w.elapsed)
	}
	return nil
}

func registerReasonerSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the LLM mode is "([^"]*)"$`, w.theLLMModeIs)
	sc.Step(`^a reasoner is wired that proposes "([^"]*)" with (-?\d+) heads and rationale "([^"]*)"$`, w.aReasonerProposes)
	sc.Step(`^a reasoner is wired that fails with "([^"]*)"$`, w.aReasonerFailsWith)
	sc.Step(`^no reasoner is wired$`, w.noReasonerIsWired)
	sc.Step(`^an Anthropic API stand-in that submits the plan "([^"]*)" with (-?\d+) heads and rationale "([^"]*)"$`, w.anAnthropicStandInSubmits)
	sc.Step(`^the Anthropic API stand-in fails with status (\d+) for the first (\d+) requests$`, w.theStandInFailsForTheFirst)
	sc.Step(`^the Anthropic API stand-in always fails with status (\d+)$`, w.theStandInAlwaysFails)
	sc.Step(`^the Anthropic API stand-in takes (\d+) ms to answer$`, w.theStandInTakes)
	sc.Step(`^the Anthropic API stand-in recovers$`, w.theStandInRecovers)
	sc.Step(`^the reasoner timeout is (\d+) ms$`, w.theReasonerTimeoutIs)
	sc.Step(`^the circuit breaker cooldown is (\d+) ms$`, w.theBreakerCooldownIs)
	sc.Step(`^the circuit breaker cooldown elapses$`, w.theBreakerCooldownElapses)

	sc.Step(`^the reasoner was consulted (\d+) times?$`, w.theReasonerWasConsulted)
	sc.Step(`^the arbitration metrics recorded source "([^"]*)" in mode "([^"]*)" with agreement "(true|false|unknown)"$`, w.theArbitrationMetricsRecorded)
	sc.Step(`^the reasoner was offered the actions "([^"]*)"$`, w.theReasonerWasOfferedTheActions)
	sc.Step(`^the reasoner was told "([^"]*)" is unavailable$`, w.theReasonerWasToldUnavailable)
	sc.Step(`^the reasoner was shown the fact "([^"]*)"$`, w.theReasonerWasShownTheFact)
	sc.Step(`^the Anthropic API stand-in received (\d+) requests?$`, w.theStandInReceived)
	sc.Step(`^the advisory answered within (\d+) ms$`, w.theAdvisoryAnsweredWithin)
}
