# Derived from: docs/docs/adr/0011-reasoner-path-circuit-breaker-timeout-retry.md
#   (breaker opens on 5 consecutive failures; bounded retry of 3 attempts, transient errors only, 4xx permanent;
#    deadline-derived timeout; breaker OPEN falls back through the existing deterministic path),
# docs/docs/adr/0004-llm-reasoner-behind-the-policy-layer.md (deterministic fallback on any reasoner error),
# internal/resilience/breaker.go (ReadyToTrip), internal/adapters/outbound/llm/anthropic/reasoner.go.
# The Anthropic Messages API is replaced by a local in-process stand-in: no model or external network is ever contacted.
@bdd
Feature: Reasoner-path resilience
  The model call sits behind a retry, a timeout and a circuit breaker. However the
  model dependency misbehaves (slow, failing, rejected or tripped open), the caller
  keeps getting the deterministic decision, marked as a fallback, and a dependency
  known to be down is not hammered.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And the LLM mode is "on"
    And an Anthropic API stand-in that submits the plan "release_next_work" with 0 heads and rationale "queue is shallow"

  Scenario: A healthy model is used
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "release_next_work" with 0 proposed heads
    And the decision source is "llm"
    And the Anthropic API stand-in received 1 request

  Scenario: A transient server error is retried and the model still answers
    Given the Anthropic API stand-in fails with status 503 for the first 1 requests
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the decision source is "llm"
    And the Anthropic API stand-in received 2 requests

  Scenario: A persistent server error exhausts a bounded retry and falls back
    Given the Anthropic API stand-in always fails with status 500
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "fallback"
    And the Anthropic API stand-in received 3 requests

  Scenario: A client error is permanent and is not retried
    Given the Anthropic API stand-in always fails with status 401
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "fallback"
    And the Anthropic API stand-in received 1 request

  Scenario: A model slower than the reasoner timeout does not hold the caller hostage
    Given the reasoner timeout is 300 ms
    And the Anthropic API stand-in takes 5000 ms to answer
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "fallback"
    And the advisory answered within 3000 ms

  Scenario: A model that answers outside the vocabulary is refused and the deterministic decision stands
    Given an Anthropic API stand-in that submits the plan "launch_rocket" with 0 heads and rationale "not a lever"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "fallback"

  Scenario: Five consecutive failures open the breaker and the dependency is no longer called
    Given the Anthropic API stand-in always fails with status 500
    When I request the flow-balance advisory for path "pick-zone-a" 6 times
    Then the response status is 200
    And the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "fallback"
    And the rationale mentions "confirms the path is understaffed"
    And the Anthropic API stand-in received 15 requests

  Scenario: The breaker closes again once the dependency recovers
    Given the circuit breaker cooldown is 300 ms
    And the Anthropic API stand-in always fails with status 500
    And I request the flow-balance advisory for path "pick-zone-a" 6 times
    And the Anthropic API stand-in recovers
    And the circuit breaker cooldown elapses
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "release_next_work" with 0 proposed heads
    And the decision source is "llm"
    And the Anthropic API stand-in received 16 requests
