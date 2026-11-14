# Derived from: warehouse-docs docs/contexts/warehouse-ops-agent/use-cases.md (Console report dashboards: default window is the trailing 24 hours, `to` must be after `from`;
#   Order lifecycle: each stage independent, tasks joined through work-unit ids, one lookup per work unit, no work units means no task lookup),
#   troubleshooting.md ("fulfillment stage empty although tasks exist", 400 on /console/reports/*) and sequence-diagrams.md (packageSealed / labelApplied
#   are derived from a completed SLAM task).
# Complements console_reports.feature and order_lifecycle.feature.
@bdd
Feature: Console read-model rules
  The console asks for windows and journeys the way a human does: with half a window, or
  for an order that has not reached the floor yet. The defaults and the joins must hold.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario Outline: A window that does not run forward is refused before any reader is asked
    When I open the <dashboard> dashboard from "2026-01-01T06:00:00Z" to "2026-01-01T06:00:00Z"
    Then the response status is 400
    And the response error mentions "must be after"

    Examples:
      | dashboard |
      | WMS       |
      | WES       |

  Scenario: A window with only a start ends now
    When I GET "/console/reports/wms?from=2026-01-01T00:00:00Z"
    Then the response status is 200
    And the JSON field "from" equals "2026-01-01T00:00:00Z"
    And the JSON field "to" equals "2026-01-01T08:00:00Z"

  Scenario: A window with only an end starts twenty-four hours before it
    When I GET "/console/reports/wes?to=2026-01-01T06:00:00Z"
    Then the response status is 200
    And the JSON field "from" equals "2025-12-31T06:00:00Z"
    And the JSON field "to" equals "2026-01-01T06:00:00Z"

  Scenario: Every work unit of the order is asked for its own tasks
    Given order-management has order "ORD-1" with status "Released" and a line of 2 units of SKU "SKU-1"
    And wes-work-planning has work unit "ORD-1-line-1" on path "pick-zone-a" in state "Released" for order "ORD-1"
    And wes-work-planning has work unit "ORD-1-line-2" on path "pack-zone-b" in state "Released" for order "ORD-1"
    And fulfillment-execution has a "OPEN" "PICK" task "T-1" for work unit "ORD-1-line-1"
    And fulfillment-execution has a "OPEN" "PACK" task "T-2" for work unit "ORD-1-line-2"
    When I open the lifecycle of order "ORD-1"
    Then the JSON array "fulfillment.tasks" has 2 items
    And fulfillment-execution was asked for the tasks of "ORD-1-line-1"
    And fulfillment-execution was asked for the tasks of "ORD-1-line-2"

  Scenario: An order with no work unit yet triggers no task lookup at all
    Given order-management has order "ORD-2" with status "Received" and a line of 1 units of SKU "SKU-9"
    When I open the lifecycle of order "ORD-2"
    Then the response status is 200
    And fulfillment-execution was not asked for any tasks

  Scenario Outline: The package counts as sealed only once a SLAM task has completed
    Given order-management has order "ORD-1" with status "Released" and a line of 1 units of SKU "SKU-1"
    And wes-work-planning has work unit "ORD-1-line-1" on path "pick-zone-a" in state "Released" for order "ORD-1"
    And fulfillment-execution has a "<state>" "<type>" task "T-1" for work unit "ORD-1-line-1"
    When I open the lifecycle of order "ORD-1"
    Then the JSON field "fulfillment.packageSealed" is <sealed>

    Examples:
      | type | state     | sealed |
      | PICK | COMPLETED | false  |
      | PACK | COMPLETED | false  |
      | SLAM | OPEN      | false  |
