# Derived from: docs/docs/api-surface.md (GET /console/orders/{id}/lifecycle: each stage degrades independently, 404 only when order-management says the order does not exist, no 502 path),
# docs/docs/adr/0002-micro-frontend-console-architecture.md (console-bff partial-tolerant read model),
# docs/docs/adr/0016-rest-error-mapping-cors-and-strict-path-target-config.md (unreachable order-management still yields 200 with orderManagement null),
# internal/application/usecases/order_lifecycle.go (tasks are joined through each work unit's id, not the plain order id).
@bdd
Feature: Console order lifecycle
  The console shell asks the agent for one order's journey across order-management,
  inventory-storage, wes-work-planning and fulfillment-execution. A stage that has not
  happened yet, or whose context does not answer, is null: it never fails the screen.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario: An order's journey is stitched across all four contexts
    Given order-management has order "ORD-1" with status "Released" and a line of 2 units of SKU "SKU-1"
    And inventory-storage has an "Active" reservation of 2 units of SKU "SKU-1" for demand "ORD-1"
    And wes-work-planning has work unit "ORD-1-line-1" on path "pick-zone-a" in state "Released" for order "ORD-1"
    And fulfillment-execution has a "COMPLETED" "SLAM" task "T-3" for work unit "ORD-1-line-1"
    When I open the lifecycle of order "ORD-1"
    Then the response status is 200
    And the JSON field "orderId" equals "ORD-1"
    And the JSON field "orderManagement.status" equals "Released"
    And the JSON field "orderManagement.lines.0.sku" equals "SKU-1"
    And the JSON field "inventory.reservations.0.status" equals "Active"
    And the JSON field "planning.workUnits.0.workUnitId" equals "ORD-1-line-1"
    And the JSON field "fulfillment.tasks.0.taskType" equals "SLAM"
    And the JSON field "fulfillment.packageSealed" is true

  Scenario: Tasks are found through the work unit id, not the plain order id
    Given order-management has order "ORD-1" with status "Released" and a line of 1 units of SKU "SKU-1"
    And wes-work-planning has work unit "ORD-1-line-1" on path "pick-zone-a" in state "Released" for order "ORD-1"
    And fulfillment-execution has a "OPEN" "PICK" task "T-1" for work unit "ORD-1-line-1"
    When I open the lifecycle of order "ORD-1"
    Then the JSON field "fulfillment.tasks.0.taskId" equals "T-1"
    And the JSON field "fulfillment.packageSealed" is false
    And fulfillment-execution was asked for the tasks of "ORD-1-line-1"

  Scenario: An order that has not been released yet shows only the stages that happened
    Given order-management has order "ORD-2" with status "Received" and a line of 1 units of SKU "SKU-9"
    When I open the lifecycle of order "ORD-2"
    Then the response status is 200
    And the JSON field "orderManagement.status" equals "Received"
    And the JSON field "fulfillment" is null

  Scenario: An order unknown to order-management is a 404
    When I open the lifecycle of order "ORD-404"
    Then the response status is 404
    And the response error mentions "order not found"

  Scenario Outline: One downstream context being unreachable nulls only its own stage
    Given order-management has order "ORD-1" with status "Released" and a line of 2 units of SKU "SKU-1"
    And inventory-storage has an "Active" reservation of 2 units of SKU "SKU-1" for demand "ORD-1"
    And wes-work-planning has work unit "ORD-1-line-1" on path "pick-zone-a" in state "Released" for order "ORD-1"
    And fulfillment-execution has a "OPEN" "PICK" task "T-1" for work unit "ORD-1-line-1"
    And the <context> REST API is unreachable
    When I open the lifecycle of order "ORD-1"
    Then the response status is 200
    And the JSON field "<stage>" is null
    And the JSON field "orderId" equals "ORD-1"

    Examples:
      | context               | stage           |
      | order-management      | orderManagement |
      | inventory-storage     | inventory       |
      | wes-work-planning     | planning        |

  Scenario: An unreachable fulfillment-execution leaves the other stages intact
    Given order-management has order "ORD-1" with status "Released" and a line of 2 units of SKU "SKU-1"
    And wes-work-planning has work unit "ORD-1-line-1" on path "pick-zone-a" in state "Released" for order "ORD-1"
    And the fulfillment-execution REST API is unreachable
    When I open the lifecycle of order "ORD-1"
    Then the response status is 200
    And the JSON field "orderManagement.status" equals "Released"
    And the JSON field "planning.workUnits.0.workUnitId" equals "ORD-1-line-1"
    And the JSON field "fulfillment" is null

  Scenario: The endpoint answers 503 when it is not configured
    Given the "order-lifecycle" capability is not configured
    When I open the lifecycle of order "ORD-1"
    Then the response status is 503
