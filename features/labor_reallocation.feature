# Derived from: docs/docs/adr/0029-labor-reallocation-act-slice.md (act slice #2: propose then approve, HMAC token bound to one move, delegated over A2A assign_labor, REST-only),
# docs/docs/api/http-routes.md (POST /labor-reallocation/proposals and POST /labor-reallocation/proposals/{proposalId}/approve: 200/400/404/409/410/415/502/503),
# internal/domain/policy/labor_reallocation.go (at most three ranked moves; no move off a path that is not above its plannedHeads),
# internal/application/usecases/labor_reallocation.go (token consumed before delegating; a bad token never reaches the peer).
@bdd
Feature: Labor reallocation (act slice #2)
  The agent proposes at most three moves that would cover an understaffed path, each
  from readings workforce-management returned. Nothing is written until a human
  approves ONE move with its own short-lived token and says who approved it; only then
  is the move delegated to workforce-management, which keeps every invariant.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And workforce-management reports 10 planned and 7 active heads on path "pack"
    And workforce-management reports 5 planned and 8 active heads on path "pick"

  Scenario: A proposal ranks the moves and carries a token per move, writing nothing
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1","fromPathId":"pick"}]}
      """
    Then the response status is 200
    And the JSON field "target.plannedHeads" equals "10"
    And the JSON field "target.activeHeads" equals "7"
    And the JSON array "proposals" has 1 item
    And the JSON field "proposals.0.associateId" equals "a1"
    And the JSON field "proposals.0.fromPathId" equals "pick"
    And the JSON field "proposals.0.toPathId" equals "pack"
    And the JSON array "proposals.0.reasonCodes" has the item "origin_path_above_plan"
    And the JSON field "proposals.0.nextCheck" contains "Read-only"
    And the JSON array "proposals.0.evidence" has 2 items
    And the response carries a token and a proposal id for the first proposal
    And the workforce-management agent received 0 assignments

  Scenario: Never more than three moves, and never more than the path is short
    Given workforce-management reports 10 planned and 2 active heads on path "pack"
    And workforce-management reports 2 planned and 12 active heads on path "stow"
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[
        {"associateId":"a1","fromPathId":"stow"},{"associateId":"a2","fromPathId":"stow"},
        {"associateId":"a3","fromPathId":"stow"},{"associateId":"a4","fromPathId":"stow"}]}
      """
    Then the response status is 200
    And the JSON array "proposals" has 3 items
    And the JSON array "skipped" has 1 item
    And the JSON field "skipped.0.reason" equals "move_limit_reached"

  Scenario: An associate whose path is not above plan is not moved
    Given workforce-management reports 5 planned and 5 active heads on path "pick"
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1","fromPathId":"pick"}]}
      """
    Then the response status is 409
    And the response error mentions "no offered associate"
    And the response carries no token

  Scenario: A path that is not understaffed gets no proposal
    Given workforce-management reports 4 planned and 4 active heads on path "pack"
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1","fromPathId":"pick"}]}
      """
    Then the response status is 409
    And the response error mentions "not understaffed"
    And the response carries no token

  Scenario: A proposal without candidates is refused
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[]}
      """
    Then the response status is 400

  Scenario: Approving a move delegates exactly that move, stating who approved it
    Given I have proposed moving "a1" from "pick" to "pack"
    When I approve the first proposal as "supervisor-1"
    Then the response status is 200
    And the JSON field "associateId" equals "a1"
    And the JSON field "pathId" equals "pack"
    And the JSON field "approvedBy" equals "supervisor-1"
    And the workforce-management agent received 1 assignments
    And the workforce-management agent was asked to assign "a1" to "pack" approved by "supervisor-1" as agent "warehouse-ops-agent" under the first proposal

  Scenario: An expired token is refused and never reaches the peer
    Given I have proposed moving "a1" from "pick" to "pack"
    When 6 minutes pass
    And I approve the first proposal as "supervisor-1"
    Then the response status is 410
    And the workforce-management agent received 0 assignments

  Scenario: A forged token is refused and never reaches the peer
    Given I have proposed moving "a1" from "pick" to "pack"
    When I approve the first proposal with a forged token as "supervisor-1"
    Then the response status is 400
    And the workforce-management agent received 0 assignments

  Scenario: A token cannot approve another proposal
    Given I have proposed moving "a1" from "pick" to "pack"
    When I approve the proposal "lrp-00000000000000000000000000000000" with the first token as "supervisor-1"
    Then the response status is 400
    And the workforce-management agent received 0 assignments

  Scenario: Approving needs a named approver
    Given I have proposed moving "a1" from "pick" to "pack"
    When I approve the first proposal as ""
    Then the response status is 400
    And the workforce-management agent received 0 assignments

  Scenario: A token can be used once
    Given I have proposed moving "a1" from "pick" to "pack"
    When I approve the first proposal as "supervisor-1"
    And I approve the first proposal as "supervisor-1"
    Then the response status is 409
    And the workforce-management agent received 1 assignments

  Scenario: A domain rule at workforce-management is reported as a conflict
    Given the workforce-management agent rejects the assignment with a domain rule
    And I have proposed moving "a1" from "pick" to "pack"
    When I approve the first proposal as "supervisor-1"
    Then the response status is 409
    And the response error mentions "rejected the move on a domain rule"

  Scenario: An unknown associate at workforce-management is reported as not found
    Given the workforce-management agent does not know the associate
    And I have proposed moving "a1" from "pick" to "pack"
    When I approve the first proposal as "supervisor-1"
    Then the response status is 404

  Scenario: An unreachable workforce-management agent is a bad gateway and the token stays spent
    Given the workforce-management agent is unreachable
    And I have proposed moving "a1" from "pick" to "pack"
    When I approve the first proposal as "supervisor-1"
    Then the response status is 502
    When I approve the first proposal as "supervisor-1"
    Then the response status is 409

  Scenario Outline: With the A2A agent not configured the feature is off
    Given the "labor-reallocation" capability is not configured
    When I POST "<path>" with JSON:
      """
      {}
      """
    Then the response status is 503
    And the response error mentions "WORKFORCE_MANAGEMENT_A2A_URL"

    Examples:
      | path                                                 |
      | /labor-reallocation/proposals                        |
      | /labor-reallocation/proposals/lrp-abc/approve        |

  Scenario: A browser on another origin is not granted the write
    When I send a CORS preflight from origin "http://localhost:5173" for method "POST" on "/labor-reallocation/proposals"
    Then the response header "Access-Control-Allow-Methods" is absent

  Scenario: The write routes accept only JSON
    When I POST "/labor-reallocation/proposals" with content type "text/plain" and body "x"
    Then the response status is 415
