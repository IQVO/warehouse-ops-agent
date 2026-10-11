# Derived from: docs/docs/adr/0004-llm-reasoner-behind-the-policy-layer.md (LLM_MODE off|shadow|on; deterministic decision always computed; model output enters the domain only through policy validation),
# docs/docs/api-surface.md (GET /flow-balance/{pathId}: "Optionally arbitrated by the ADR-0004 LLM reasoner"),
# internal/domain/policy/arbitrate.go (Arbitrate, ValidatePlan, MaxProposedHeads = 50, DecisionSource deterministic|llm|fallback),
# internal/ports/reasoner.go (Brief: closed AllowedActions, facts keyed by source).
@bdd
Feature: LLM reasoner behind the policy layer
  The deterministic decision is always computed first and is always the fallback.
  A model-backed reasoner can replace it only in "on" mode and only with a plan the
  policy layer validates against the closed vocabulary and the head-count bound;
  anything else is rejected and the deterministic decision stands. The reasoner here
  is a fake: no model or network is ever contacted.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks

  Scenario: In off mode the reasoner is never consulted
    Given the LLM mode is "off"
    And a reasoner is wired that proposes "hold" with 0 heads and rationale "model prefers to wait"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "deterministic"
    And the reasoner was consulted 0 times

  Scenario: In shadow mode the reasoner is consulted but the deterministic decision is returned
    Given the LLM mode is "shadow"
    And a reasoner is wired that proposes "hold" with 0 heads and rationale "model prefers to wait"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "deterministic"
    And the reasoner was consulted 1 time
    And the arbitration metrics recorded source "deterministic" in mode "shadow" with agreement "false"

  Scenario: Shadow mode records agreement when the plan matches the deterministic action
    Given the LLM mode is "shadow"
    And a reasoner is wired that proposes "assign_labor" with 3 heads and rationale "understaffed"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the decision source is "deterministic"
    And the arbitration metrics recorded source "deterministic" in mode "shadow" with agreement "true"

  Scenario: In on mode a valid plan replaces the lever but not the evidence
    Given the LLM mode is "on"
    And a reasoner is wired that proposes "release_next_work" with 0 heads and rationale "queue is shallow, release the next unit"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "release_next_work" with 0 proposed heads
    And the decision source is "llm"
    And the JSON field "rationale" equals "queue is shallow, release the next unit"
    And the JSON array "evidence" has 3 items
    And the arbitration metrics recorded source "llm" in mode "on" with agreement "false"

  Scenario Outline: In on mode a plan outside the policy's bounds is rejected and the deterministic decision stands
    Given the LLM mode is "on"
    And a reasoner is wired that proposes "<action>" with <heads> heads and rationale "<rationale>"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "fallback"
    And the arbitration metrics recorded source "fallback" in mode "on" with agreement "unknown"

    Examples:
      | action        | heads | rationale            |
      | launch_rocket | 0     | outside the vocabulary |
      | assign_labor  | 51    | above the head bound |
      | assign_labor  | -1    | below zero           |
      | hold          | 2     | heads without assign |
      | assign_labor  | 3     |                      |

  Scenario Outline: A plan on the edge of the bounds is accepted
    Given the LLM mode is "on"
    And a reasoner is wired that proposes "assign_labor" with <heads> heads and rationale "peak shift"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with <heads> proposed heads
    And the decision source is "llm"

    Examples:
      | heads |
      | 1     |
      | 50    |

  Scenario: In on mode a failing reasoner falls back to the deterministic decision
    Given the LLM mode is "on"
    And a reasoner is wired that fails with "upstream model unavailable"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "fallback"
    And the reasoner was consulted 1 time

  Scenario: In shadow mode a failing reasoner is invisible to the caller
    Given the LLM mode is "shadow"
    And a reasoner is wired that fails with "upstream model unavailable"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "deterministic"

  Scenario: On mode without a wired reasoner behaves as the deterministic path
    Given the LLM mode is "on"
    And no reasoner is wired
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the decision source is "deterministic"

  Scenario: The reasoner is offered only the closed vocabulary and told what is unavailable
    Given workforce-management's MCP tools are unreachable
    And the LLM mode is "on"
    And a reasoner is wired that proposes "hold" with 0 heads and rationale "staffing signal is missing"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the reasoner was offered the actions "assign_labor, release_next_work, hold"
    And the reasoner was told "workforce-management.get_staffing_gap" is unavailable
    And the reasoner was shown the fact "wes-work-planning.get_rebalance_recommendation"
