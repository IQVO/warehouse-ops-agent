# Derived from: docs/docs/api-surface.md (REST table: every route is a GET; neither surface is authenticated),
# docs/docs/adr/0006-fleet-wide-auth-removal.md (no auth anywhere in the fleet),
# docs/docs/adr/0016-rest-error-mapping-cors-and-strict-path-target-config.md (CORS admits GET and OPTIONS only),
# internal/adapters/inbound/http/router.go (chi router and middleware chain).
@bdd
Feature: HTTP conventions shared by every route
  warehouse-ops-agent is a read-only, unauthenticated agent. Every route is a GET,
  CORS admits only the console origin and only GET/OPTIONS, and an unknown route or
  method is refused by the router before any use case runs.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario: The liveness probe answers with a JSON status
    When I GET "/healthz"
    Then the response status is 200
    And the response is JSON
    And the JSON field "status" equals "ok"

  Scenario Outline: No route asks for credentials
    When I GET "<path>"
    Then the response status is neither 401 nor 403
    And the request carried no Authorization header

    Examples:
      | path                                               |
      | /healthz                                           |
      | /daily-brief                                       |
      | /flow-balance/pick-zone-a?buildingId=B1&shiftId=S1 |
      | /console/reports/wms                               |
      | /console/reports/wes                               |
      | /runtime-signals                                   |
      | /master-data-gaps                                  |
      | /inbound-outlook                                   |
      | /transfer-watch/imbalance                          |

  Scenario: An unknown route is not found
    When I GET "/no-such-route"
    Then the response status is 404

  Scenario Outline: Only GET is served
    When I send a <method> request to "<path>"
    Then the response status is 405

    Examples:
      | method | path                      |
      | POST   | /daily-brief              |
      | PUT    | /flow-balance/pick-zone-a |
      | DELETE | /console/reports/wms      |

  Scenario: The console origin may read the API cross-origin
    When I send a CORS preflight from origin "http://localhost:5173" for method "GET" on "/daily-brief"
    Then the response header "Access-Control-Allow-Origin" equals "http://localhost:5173"

  Scenario: A write method is not granted cross-origin
    When I send a CORS preflight from origin "http://localhost:5173" for method "POST" on "/daily-brief"
    Then the response header "Access-Control-Allow-Methods" is absent

  Scenario: An unknown origin is not granted cross-origin access
    When I send a CORS preflight from origin "http://evil.example" for method "GET" on "/daily-brief"
    Then the response header "Access-Control-Allow-Origin" is absent
