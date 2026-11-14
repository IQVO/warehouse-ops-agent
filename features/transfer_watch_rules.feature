# Derived from: warehouse-docs docs/contexts/warehouse-ops-agent/use-cases.md (Transfer watch: olderThanMinutes positive, non-terminal state, limit 0-200;
#   cause from state alone, tallied highest first; imbalance lists short sites before covered, never proposes a quantity or route),
#   troubleshooting.md (400 from /transfer-watch/stuck), and the in-repo docs/docs/adr/0019-network-inventory-planning-transfer-watch.md
#   (DRAFT, PROPOSED, APPROVED and any state outside the saga stages are "unclassified": no cause is invented; short means headroom below zero).
# Complements transfer_watch.feature.
@bdd
Feature: Transfer watch rules
  The agent explains why a transfer is stuck from its saga state alone and which sites are
  short from network-inventory-planning's own headroom figure. It never invents a cause,
  a threshold, a quantity or a route.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario Outline: A stuck transfer's cause comes from its state alone
    Given network-inventory-planning holds transfer "T-77" in state "<state>" for 12 units of SKU "SKU-4711" from "SITE-A" to "SITE-B"
    When I list the transfers stuck for more than 30 minutes
    Then the response status is 200
    And the JSON field "transfers.0.triage.cause" equals "<cause>"
    And the JSON field "causeCount.0.cause" equals "<cause>"
    And the JSON field "causeCount.0.count" equals "1"

    Examples:
      | state     | cause                       |
      | ALLOCATED | floor-work-not-progressing  |
      | PICKED    | floor-work-not-progressing  |
      | ARRIVED   | destination-receipt-missing |
      | DRAFT     | unclassified                |
      | PROPOSED  | unclassified                |
      | APPROVED  | unclassified                |

  Scenario Outline: The documented bounds of the stuck-transfer query are accepted at the edge
    Given network-inventory-planning holds transfer "T-1" in state "ALLOCATING" for 5 units of SKU "SKU-1" from "SITE-A" to "SITE-B"
    When I GET "<path>"
    Then the response status is 200
    And the JSON field "total" equals "1"

    Examples:
      | path                                                |
      | /transfer-watch/stuck?olderThanMinutes=1            |
      | /transfer-watch/stuck?olderThanMinutes=30&limit=0   |
      | /transfer-watch/stuck?olderThanMinutes=30&limit=200 |

  Scenario Outline: A negative bound, an unknown state or a finished state is refused before network-inventory-planning is asked
    When I GET "<path>"
    Then the response status is 400
    And network-inventory-planning was not called

    Examples:
      | path                                                       |
      | /transfer-watch/stuck?olderThanMinutes=-5                  |
      | /transfer-watch/stuck?olderThanMinutes=30&limit=-1         |
      | /transfer-watch/stuck?olderThanMinutes=30&state=SHIPPED    |
      | /transfer-watch/stuck?olderThanMinutes=30&state=CANCELLED  |
      | /transfer-watch/stuck?olderThanMinutes=30&state=UNFULFILLABLE |

  Scenario: Short sites come first, the most short first, then covered sites with the most headroom first
    Given network-inventory-planning simulates site "SITE-C" with demand 20 against capacity 60 and headroom 40
    And network-inventory-planning simulates site "SITE-A" with demand 70 against capacity 60 and headroom -10
    And network-inventory-planning simulates site "SITE-D" with demand 45 against capacity 60 and headroom 15
    And network-inventory-planning simulates site "SITE-B" with demand 100 against capacity 40 and headroom -60
    When I ask for the network imbalance
    Then the JSON field "sites.0.site" equals "SITE-B"
    And the JSON field "sites.1.site" equals "SITE-A"
    And the JSON field "sites.2.site" equals "SITE-C"
    And the JSON field "sites.3.site" equals "SITE-D"
    And the JSON field "summary" contains "SITE-B (-60), SITE-A (-10)"

  Scenario Outline: A site is short only when its headroom is below zero
    Given network-inventory-planning simulates site "SITE-A" with demand 60 against capacity 60 and headroom <headroom>
    When I ask for the network imbalance
    Then the JSON field "sites.0.balance" equals "<balance>"
    And the JSON field "imbalanced" is <imbalanced>

    Examples:
      | headroom | balance | imbalanced |
      | 0        | covered | false      |
      | -1       | short   | true       |

  Scenario: A site with exactly zero headroom is covered but has nothing to give, so it is not named as a donor
    Given network-inventory-planning simulates site "SITE-A" with demand 60 against capacity 60 and headroom 0
    And network-inventory-planning simulates site "SITE-B" with demand 100 against capacity 40 and headroom -60
    When I ask for the network imbalance
    Then the JSON field "imbalanced" is true
    And the JSON field "summary" contains "no site with headroom is enabled to originate transfers"

  Scenario: The imbalance names the sites and proposes no quantity or route
    Given network-inventory-planning simulates site "SITE-A" with demand 20 against capacity 60 and headroom 40
    And network-inventory-planning simulates site "SITE-B" with demand 100 against capacity 40 and headroom -60
    When I ask for the network imbalance
    Then the JSON field "nextCheck" contains "proposes no quantity"
    And the JSON field "quantity" is absent
    And the JSON field "route" is absent
    And the JSON field "summary" contains "SITE-A (+40)"
