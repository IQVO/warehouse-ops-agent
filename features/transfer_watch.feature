# Derived from: docs/docs/api-surface.md (GET /transfer-watch/stuck, /transfer-watch/transfers/{id}, /transfer-watch/imbalance: 400 caller input, 404 unknown transfer, 502 other upstream failure, 503 when unconfigured),
# docs/docs/adr/0019-network-inventory-planning-transfer-watch.md (read-only; olderThanMinutes has no default; triage is advisory and proposes no quantity or route),
# internal/domain/policy/transfer_triage.go (state -> cause), internal/domain/policy/transfer_imbalance.go (short = negative headroom).
@bdd
Feature: Transfer watch
  The agent explains why inter-warehouse transfers are stuck and which sites are short.
  It reads network-inventory-planning and advises; moving, approving or cancelling a
  transfer stays an operator action in that context.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario: Stuck transfers are triaged by saga state and tallied by cause
    Given network-inventory-planning holds transfer "T-1" in state "ALLOCATING" for 5 units of SKU "SKU-1" from "SITE-A" to "SITE-B"
    And network-inventory-planning holds transfer "T-2" in state "IN_TRANSIT" for 8 units of SKU "SKU-2" from "SITE-A" to "SITE-C"
    And network-inventory-planning holds transfer "T-3" in state "ALLOCATING" for 2 units of SKU "SKU-3" from "SITE-B" to "SITE-C"
    When I list the transfers stuck for more than 30 minutes
    Then the response status is 200
    And the JSON field "total" equals "3"
    And the JSON field "transfers.0.triage.cause" equals "inventory-reply-missing"
    And the JSON field "transfers.1.triage.cause" equals "destination-receipt-missing"
    And the JSON field "causeCount.0.cause" equals "inventory-reply-missing"
    And the JSON field "causeCount.0.count" equals "2"
    And network-inventory-planning was asked for transfers older than 30 minutes

  Scenario: The state filter narrows the stuck list
    Given network-inventory-planning holds transfer "T-1" in state "ALLOCATING" for 5 units of SKU "SKU-1" from "SITE-A" to "SITE-B"
    And network-inventory-planning holds transfer "T-2" in state "IN_TRANSIT" for 8 units of SKU "SKU-2" from "SITE-A" to "SITE-C"
    When I GET "/transfer-watch/stuck?olderThanMinutes=30&state=IN_TRANSIT"
    Then the response status is 200
    And the JSON field "total" equals "1"
    And the JSON field "transfers.0.id" equals "T-2"

  Scenario Outline: The caller's own input is validated and the agent picks no default
    When I GET "<path>"
    Then the response status is 400
    And network-inventory-planning was not called

    Examples:
      | path                                                |
      | /transfer-watch/stuck                               |
      | /transfer-watch/stuck?olderThanMinutes=abc          |
      | /transfer-watch/stuck?olderThanMinutes=0            |
      | /transfer-watch/stuck?olderThanMinutes=30&state=RECEIVED |
      | /transfer-watch/stuck?olderThanMinutes=30&limit=201 |

  Scenario: One transfer's status carries its audit trail and triage
    Given network-inventory-planning holds transfer "T-1" in state "ALLOCATING" for 5 units of SKU "SKU-1" from "SITE-A" to "SITE-B"
    And transfer "T-1" has an audit entry moving it from "APPROVED" to "ALLOCATING" because "allocation requested"
    When I look up the status of transfer "T-1"
    Then the response status is 200
    And the JSON field "state" equals "ALLOCATING"
    And the JSON field "triage.cause" equals "inventory-reply-missing"
    And the JSON array "audit" has 1 item
    And the JSON field "audit.0.to" equals "ALLOCATING"

  Scenario: A transfer NIP does not know is a 404
    When I look up the status of transfer "T-404"
    Then the response status is 404

  Scenario: An unreachable network-inventory-planning is a 502
    Given network-inventory-planning is unreachable
    When I look up the status of transfer "T-1"
    Then the response status is 502

  Scenario: The network imbalance names short sites first and the donors that could help
    Given network-inventory-planning simulates site "SITE-A" with demand 20 against capacity 60 and headroom 40
    And network-inventory-planning simulates site "SITE-B" with demand 100 against capacity 40 and headroom -60
    When I ask for the network imbalance
    Then the response status is 200
    And the JSON field "advisory" is true
    And the JSON field "imbalanced" is true
    And the JSON field "sites.0.site" equals "SITE-B"
    And the JSON field "sites.0.balance" equals "short"
    And the JSON field "sites.1.site" equals "SITE-A"
    And the JSON field "sites.1.balance" equals "covered"
    And the JSON field "summary" contains "SITE-B (-60)"

  Scenario: A network with every site covered reports no imbalance
    Given network-inventory-planning simulates site "SITE-A" with demand 20 against capacity 60 and headroom 40
    When I ask for the network imbalance
    Then the JSON field "imbalanced" is false

  Scenario: A fail-closed simulation is a 502, never an empty reading
    Given network-inventory-planning is unreachable
    When I ask for the network imbalance
    Then the response status is 502

  Scenario Outline: The routes answer 503 when network-inventory-planning is not configured
    Given the "transfer-watch" capability is not configured
    When I GET "<path>"
    Then the response status is 503

    Examples:
      | path                                       |
      | /transfer-watch/stuck?olderThanMinutes=30 |
      | /transfer-watch/transfers/T-1              |
      | /transfer-watch/imbalance                  |
