# Derived from: docs/docs/api-surface.md (GET /explain-travel-factor: both location codes REQUIRED; 400 for missing or validation-slug rejection, 502 for any other upstream failure, 503 if not wired),
# docs/docs/adr/0009-explain-travel-factor.md (the agent never infers location codes; 60 m threshold),
# docs/docs/adr/0016-rest-error-mapping-cors-and-strict-path-target-config.md (upstream failure is 502, not 400),
# docs/docs/adr/0018-mcp-tool-error-slug-classification.md (validation slug => caller's input => 400),
# internal/domain/policy/travel_factor.go (travel_significant above 60 m, travel_negligible at or below).
@bdd
Feature: Explain travel factor
  Given two facility-layout location codes the caller already knows, the agent asks
  facility-layout for the route length and classifies whether travel plausibly explains
  a slow path. The agent never guesses a location code on the caller's behalf.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario Outline: The route length is classified against the 60 metre threshold
    Given facility-layout estimates <metres> metres <basis> between "01-A-01-01-01-01-01" and "01-B-09-04-03-02-01"
    When I ask to explain the travel factor for path "pick-zone-a" from "01-A-01-01-01-01-01" to "01-B-09-04-03-02-01"
    Then the response status is 200
    And the JSON field "metresM" equals "<metres>"
    And the JSON field "estimated" is <estimated>
    And the JSON field "kind" equals "<kind>"

    Examples:
      | metres | basis     | estimated | kind               |
      | 12.5   | measured  | false     | travel_negligible  |
      | 60     | measured  | false     | travel_negligible  |
      | 60.5   | measured  | false     | travel_significant |
      | 140    | estimated | true      | travel_significant |

  Scenario: A graph-estimated distance is called out as an estimate
    Given facility-layout estimates 80 metres estimated between "01-A-01-01-01-01-01" and "01-B-09-04-03-02-01"
    When I ask to explain the travel factor for path "pick-zone-a" from "01-A-01-01-01-01-01" to "01-B-09-04-03-02-01"
    Then the JSON field "rationale" contains "estimated from the travel graph"

  Scenario: The codes the caller supplied are passed to facility-layout verbatim
    Given facility-layout estimates 30 metres measured between "01-A-01-01-01-01-01" and "01-B-09-04-03-02-01"
    When I ask to explain the travel factor for path "pick-zone-a" from "01-A-01-01-01-01-01" to "01-B-09-04-03-02-01"
    Then facility-layout was asked for the route from "01-A-01-01-01-01-01" to "01-B-09-04-03-02-01"

  Scenario Outline: A missing location code is the caller's mistake and facility-layout is not called
    When I ask to explain the travel factor for path "pick-zone-a" from "<from>" to "<to>"
    Then the response status is 400
    And the response error mentions "fromLocationCode and toLocationCode are both required"
    And facility-layout was not called

    Examples:
      | from                | to                  |
      |                     | 01-B-09-04-03-02-01 |
      | 01-A-01-01-01-01-01 |                     |

  Scenario: A code facility-layout rejects with a validation slug is the caller's input, so 400
    Given facility-layout rejects the location codes with "malformed-location-code: XX is not a seven-segment code"
    When I ask to explain the travel factor for path "pick-zone-a" from "XX" to "01-B-09-04-03-02-01"
    Then the response status is 400
    And the response error mentions "malformed-location-code"

  Scenario: An unreachable facility-layout is an upstream degradation, so 502
    Given facility-layout's MCP tools are unreachable
    When I ask to explain the travel factor for path "pick-zone-a" from "01-A-01-01-01-01-01" to "01-B-09-04-03-02-01"
    Then the response status is 502

  Scenario: A non-validation failure inside facility-layout is also 502, never blamed on the caller
    Given facility-layout fails with "internal-error: routing graph not loaded"
    When I ask to explain the travel factor for path "pick-zone-a" from "01-A-01-01-01-01-01" to "01-B-09-04-03-02-01"
    Then the response status is 502
    And the response error mentions "internal-error"

  Scenario: The endpoint answers 503 when it is not configured
    Given the "explain-travel-factor" capability is not configured
    When I ask to explain the travel factor for path "pick-zone-a" from "01-A-01-01-01-01-01" to "01-B-09-04-03-02-01"
    Then the response status is 503
