# Derived from: docs/docs/adr/0029-labor-reallocation-act-slice.md (propose then approve; at most three ranked moves; an origin may give an associate up only
#   while its active heads are strictly above plan and each move uses one head of that surplus; the target never receives more moves than it is short;
#   a candidate with no stated origin ranks last and says so; token lifetime), internal/domain/policy/labor_reallocation.go (the published reason and
#   skip codes), internal/domain/approval (a token is dead AT its expiry instant).
# Complements labor_reallocation.feature (which pins the three-move cap, the not-above-plan 409 and the approval gates).
@bdd
Feature: Labor reallocation rules
  Every screened-out candidate says why, the list is ranked by what the origin can spare,
  and a confirmation token is good for exactly its lifetime. The agent never moves an
  associate whose origin would be left short, and never invents the origin of one it was
  not told about.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And workforce-management reports 10 planned and 7 active heads on path "pack"
    And workforce-management reports 5 planned and 8 active heads on path "pick"

  Scenario Outline: A candidate that must not move is skipped with the published reason
    Given workforce-management reports <planned> planned and <active> active heads on path "stow"
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":<candidates>}
      """
    Then the response status is 200
    And the JSON array "proposals" has 1 item
    And the JSON field "proposals.0.associateId" equals "a1"
    And the JSON array "skipped" has 1 item
    And the JSON field "skipped.0.associateId" equals "a2"
    And the JSON field "skipped.0.reason" equals "<reason>"

    Examples:
      | planned | active | candidates                                                                        | reason                         |
      | 5       | 5      | [{"associateId":"a1","fromPathId":"pick"},{"associateId":"a2","fromPathId":"stow"}] | origin_path_not_above_plan     |
      | 6       | 3      | [{"associateId":"a1","fromPathId":"pick"},{"associateId":"a2","fromPathId":"stow"}] | origin_path_understaffed       |
      | 5       | 5      | [{"associateId":"a1","fromPathId":"pick"},{"associateId":"a2","fromPathId":"ghost"}] | origin_status_unavailable      |
      | 5       | 5      | [{"associateId":"a1","fromPathId":"pick"},{"associateId":"a2","fromPathId":"pack"}] | already_on_target_path         |
      | 4       | 5      | [{"associateId":"a1","fromPathId":"stow"},{"associateId":"a2","fromPathId":"stow"}] | origin_surplus_already_used    |

  Scenario: The same associate offered twice is moved once
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[{"associateId":"a1","fromPathId":"pick"},{"associateId":"a1","fromPathId":"pick"}]}
      """
    Then the response status is 200
    And the JSON array "proposals" has 1 item
    And the JSON field "skipped.0.reason" equals "duplicate_associate"

  Scenario: The target never receives more moves than it is short of plan
    Given workforce-management reports 10 planned and 8 active heads on path "pack"
    And workforce-management reports 2 planned and 12 active heads on path "stow"
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[
        {"associateId":"a1","fromPathId":"stow"},{"associateId":"a2","fromPathId":"stow"},{"associateId":"a3","fromPathId":"stow"}]}
      """
    Then the response status is 200
    And the JSON array "proposals" has 2 items
    And the JSON array "skipped" has 1 item
    And the JSON field "skipped.0.reason" equals "move_limit_reached"

  Scenario: Moves are ranked by the surplus of the origin, and an associate with no stated origin comes last and says so
    Given workforce-management reports 4 planned and 5 active heads on path "stow"
    When I POST "/labor-reallocation/proposals" with JSON:
      """
      {"siteCode":"WH1","shiftId":"S1","toPathId":"pack","candidates":[
        {"associateId":"a1","fromPathId":"stow"},{"associateId":"a2"},{"associateId":"a3","fromPathId":"pick"}]}
      """
    Then the response status is 200
    And the JSON array "proposals" has 3 items
    And the JSON field "proposals.0.associateId" equals "a3"
    And the JSON field "proposals.1.associateId" equals "a1"
    And the JSON field "proposals.2.associateId" equals "a2"
    And the JSON field "proposals.2.fromPathId" is absent
    And the JSON array "proposals.2.reasonCodes" has the item "no_current_path_stated"
    And the JSON array "proposals.0.reasonCodes" has the item "target_path_understaffed"

  Scenario Outline: A confirmation token is good until its expiry instant and dead at it
    Given I have proposed moving "a1" from "pick" to "pack"
    When <seconds> seconds pass
    And I approve the first proposal as "supervisor-1"
    Then the response status is <status>
    And the workforce-management agent received <assignments> assignments

    Examples: the token lives five minutes
      | seconds | status | assignments |
      | 299     | 200    | 1           |
      | 300     | 410    | 0           |
