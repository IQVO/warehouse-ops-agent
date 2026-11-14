# Derived from: docs/docs/adr/0027-release-next-work-act-slice.md (propose runs the flow-balance policy and mints a confirmation token ONLY when the policy itself
#   recommends release_next_work; approve verifies the token, consumes it before delegating, and delegates "admit the next unit" to wes-work-planning over A2A),
#   docs/docs/adr/0028 (the advisory exception context never changes whether a token is minted), docs/docs/api/http-routes.md (POST /act/release-next-work/
#   proposals and /approvals: 400/404/409/410/415/502/503), internal/domain/approval (a token is dead AT its expiry; a token is bound to one action).
# NOTE: warehouse-docs (use-cases.md, troubleshooting.md) still says every REST route is a GET and that nothing here writes; the repo's own ADRs 0027 and 0029 are
# the authority for this slice. The slice had no scenario before this file.
@bdd
Feature: Release-next-work act slice
  The agent may admit the next work unit of a path only after a human confirms a
  recommendation the agent itself made on complete evidence. Proposing writes nothing;
  the confirmation token is single-use, short-lived and bound to the release action.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And the release-next-work act slice is wired to wes-work-planning

  Scenario: A healthy path gets a confirmation token and nothing is released yet
    Given wes-work-planning recommends "NoActionNeeded" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management reports 4 planned and 4 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I ask to release the next work on path "pick-zone-a"
    Then the response status is 200
    And the response carries a confirmation token
    And the JSON field "action" equals "release_next_work"
    And the JSON field "pathId" equals "pick-zone-a"
    And the JSON field "recommendation.recommendedAction" equals "release_next_work"
    And the JSON field "exceptionContext.status" equals "disabled"
    And wes-work-planning's release agent received 0 requests

  Scenario Outline: No token is minted unless the policy itself recommends releasing
    Given wes-work-planning recommends "<wes>" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management reports <planned> planned and <active> active heads for path "pick-zone-a"
    And fulfillment-execution reports <stuck> stuck tasks
    And these upstreams are unreachable: "<down>"
    When I ask to release the next work on path "pick-zone-a"
    Then the response status is 409
    And the response error mentions "release_next_work is not the recommended action"
    And the response carries no confirmation token

    Examples:
      | wes            | planned | active | stuck | down                 |
      | NoActionNeeded | 4       | 4      | 2     |                      |
      | NoActionNeeded | 4       | 2      | 0     |                      |
      | ReassignLabor  | 5       | 2      | 0     |                      |
      | NoActionNeeded | 4       | 4      | 0     | workforce-management |

  Scenario: Confirming releases the next work of the proposed path exactly once
    Given wes-work-planning recommends "NoActionNeeded" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management reports 4 planned and 4 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I ask to release the next work on path "pick-zone-a"
    And I confirm the release with the proposed token
    Then the response status is 200
    And the JSON field "pathId" equals "pick-zone-a"
    And the JSON field "workUnitId" equals "ORD-1042-line-1"
    And wes-work-planning's release agent was asked to release the next work for path "pick-zone-a"
    When I confirm the release with the proposed token
    Then the response status is 409
    And wes-work-planning's release agent received 1 request

  Scenario Outline: How planning answers decides the status, and the token stays spent
    Given wes-work-planning recommends "NoActionNeeded" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management reports 4 planned and 4 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And wes-work-planning's release agent <outcome>
    When I ask to release the next work on path "pick-zone-a"
    And I confirm the release with the proposed token
    Then the response status is <status>
    When I confirm the release with the proposed token
    Then the response status is 409
    And wes-work-planning's release agent received 1 request

    Examples:
      | outcome                    | status |
      | has no pending work        | 409    |
      | does not know the path     | 404    |
      | is unreachable             | 502    |

  Scenario Outline: A confirmation token is good until its expiry instant and dead at it
    Given wes-work-planning recommends "NoActionNeeded" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management reports 4 planned and 4 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I ask to release the next work on path "pick-zone-a"
    And <seconds> seconds pass
    And I confirm the release with the proposed token
    Then the response status is <status>
    And wes-work-planning's release agent received <releases> requests

    Examples: the token lives five minutes
      | seconds | status | releases |
      | 299     | 200    | 1        |
      | 300     | 410    | 0        |

  Scenario: A token the agent did not mint never reaches planning
    When I confirm the release with the token "forged.token"
    Then the response status is 400
    And wes-work-planning's release agent received 0 requests

  Scenario: A genuine labor-reallocation token cannot release work
    When I confirm the release with a labor-reallocation token
    Then the response status is 400
    And wes-work-planning's release agent received 0 requests

  Scenario: Unknown fields in the body are refused
    When I POST "/act/release-next-work/proposals" with JSON:
      """
      {"pathId":"pick-zone-a","buildingId":"B1","shiftId":"S1","force":true}
      """
    Then the response status is 400

  Scenario: The write routes accept only JSON
    When I POST "/act/release-next-work/approvals" with content type "text/plain" and body "x"
    Then the response status is 415
