# Derived from: warehouse-docs docs/contexts/warehouse-ops-agent/use-cases.md (Daily brief: list_open_exceptions minimum severity;
#   Detect stranded reservation E2: the seven ordered rules), aggregate-design-canvas.md (boundary validation: severity filter, TaskType),
#   troubleshooting.md (MCP server rows: a tool missing from tools/list, invalid severity), testing.md (every registered tool is ReadOnlyHint),
#   and the in-repo docs/docs/mcp/tools.md.
# The E2 check has NO REST route: the MCP tool is its only trigger, so these scenarios drive the agent's real MCP server over Streamable HTTP.
@bdd
Feature: The agent's MCP surface
  The same use cases are offered to agentic hosts as read-only MCP tools. An unknown
  filter value is rejected, never defaulted; a tool whose upstream is not configured is
  not advertised; and the stranded-reservation check walks seven rules in a fixed order
  and ends in "hold" with the reason at the first one that fails.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario Outline: The severity filter returns exceptions at or above the minimum, worst first
    Given the daily brief monitors path "pick-zone-a" with process path "PICK" at site "SITE-A" in building "B1" for shift "S1"
    And the daily brief also monitors path "pack-zone-b" with process path "PACK" at site "SITE-A" in building "B1" for shift "S1"
    And wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" over its alarm threshold
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And wes-work-planning reports backlog depth 90 and WIP 20 for path "pack-zone-b" over its alarm threshold
    And workforce-management reports 4 planned and 1 active heads for path "pack-zone-b"
    And fulfillment-execution reports 3 stuck tasks of type "PACK"
    When I call the MCP tool "list_open_exceptions" with the arguments <arguments>
    Then the MCP tool call succeeds
    And the JSON field "count" equals "<count>"
    And the JSON field "exceptions.0.pathId" equals "pack-zone-b"
    And the JSON field "exceptions.0.severity" equals "critical"

    Examples:
      | arguments                | count |
      | {}                       | 2     |
      | {"severity":"info"}      | 2     |
      | {"severity":"warning"}   | 2     |
      | {"severity":"critical"}  | 1     |

  Scenario Outline: A severity outside info, warning and critical is rejected, never read as no filter
    Given the daily brief monitors path "pick-zone-a" with process path "PICK" at site "SITE-A" in building "B1" for shift "S1"
    When I call the MCP tool "list_open_exceptions" with the arguments {"severity":"<severity>"}
    Then the MCP tool call fails mentioning "invalid severity"
    And the MCP tool call fails mentioning "<severity>"

    Examples:
      | severity |
      | meltdown |
      | CRITICAL |
      | urgent   |

  Scenario: Every tool the server advertises is read-only, and the optional ones appear when wired
    When I list the MCP tools
    Then the MCP tools include "get_daily_brief"
    And the MCP tools include "list_open_exceptions"
    And the MCP tools include "get_flow_balance_exception"
    And the MCP tools include "explain_travel_factor"
    And the MCP tools include "detect_stranded_reservation"
    And every MCP tool is read-only

  Scenario: A tool whose use case is not configured is not advertised
    Given the "flow-balance" capability is not configured
    And the "explain-travel-factor" capability is not configured
    When I list the MCP tools
    Then the MCP tools include "get_daily_brief"
    And the MCP tools include "list_open_exceptions"
    And the MCP tools do not include "get_flow_balance_exception"
    And the MCP tools do not include "explain_travel_factor"

  # ------------------------------------------- detect_stranded_reservation (E2) --

  Scenario: Expired leases, a shortfall and a named reservation give a revoke recommendation with its blast radius
    Given fulfillment-execution reports 2 stuck tasks of type "PICK"
    And inventory-storage reports 5 usable units of SKU "SKU-4711"
    And inventory-storage bin "BIN-A07" holds 4 reserved units of SKU "SKU-4711" and 9 reserved units of SKU "SKU-0815"
    When I ask for a stranded-reservation check on task type "PICK" for SKU "SKU-4711" with shortfall threshold 5, reservation "RSV-9001" in bin "BIN-A07"
    Then the MCP tool call succeeds
    And the JSON field "action" equals "revoke_reservation"
    And the JSON field "detected" is true
    And the JSON field "reservationId" equals "RSV-9001"
    And the JSON field "blastRadius.binId" equals "BIN-A07"
    And the JSON field "blastRadius.sku" equals "SKU-4711"
    And the JSON field "blastRadius.quantityFreed" equals "4"
    And the JSON array "blastRadius.binLines" has 2 items
    And the JSON array "evidence" has 3 items

  Scenario Outline: The check holds at the first rule that fails and says why
    Given fulfillment-execution reports 2 stuck tasks of type "<stuck type>"
    And inventory-storage reports <usable> usable units of SKU "SKU-4711"
    And inventory-storage bin "BIN-A07" holds 4 reserved units of SKU "SKU-4711" and 9 reserved units of SKU "SKU-0815"
    And these upstreams are unreachable: "<down>"
    When I ask for a stranded-reservation check on task type "PICK" for SKU "SKU-4711" with shortfall threshold 5, reservation "<reservation>" in bin "<bin>"
    Then the MCP tool call succeeds
    And the JSON field "action" equals "hold"
    And the JSON field "detected" is <detected>
    And the JSON field "rationale" contains "<reason>"

    Examples: rule 2 expired-lease signal, 3 expired leases of the type, 4 availability signal, 5 usable at or below the threshold, 6 a reservation, 7 the blast radius
      | stuck type | usable | down                  | reservation | bin     | detected | reason                                          |
      | PICK       | 2      | fulfillment-execution | RSV-9001    | BIN-A07 | false    | expired-lease signal is unavailable             |
      | PACK       | 2      |                       | RSV-9001    | BIN-A07 | false    | no expired/expiring PICK leases                 |
      | PICK       | 2      | inventory-storage     | RSV-9001    | BIN-A07 | false    | availability signal is unavailable              |
      | PICK       | 6      |                       | RSV-9001    | BIN-A07 | false    | above the shortfall threshold                   |
      | PICK       | 2      |                       |             | BIN-A07 | true     | no candidate reservationId was supplied         |
      | PICK       | 2      |                       | RSV-9001    |         | true     | blast radius (bin occupancy) is unavailable     |

  Scenario Outline: Usable stock at the threshold is a shortfall, one above it is not
    Given fulfillment-execution reports 1 stuck task of type "PICK"
    And inventory-storage reports <usable> usable units of SKU "SKU-4711"
    And inventory-storage bin "BIN-A07" holds 4 reserved units of SKU "SKU-4711" and 9 reserved units of SKU "SKU-0815"
    When I ask for a stranded-reservation check on task type "PICK" for SKU "SKU-4711" with shortfall threshold 5, reservation "RSV-9001" in bin "BIN-A07"
    Then the JSON field "action" equals "<action>"
    And the JSON field "detected" is <detected>

    Examples:
      | usable | action             | detected |
      | 5      | revoke_reservation | true     |
      | 6      | hold               | false    |

  Scenario: A task type outside PICK, PACK and SLAM is rejected, never defaulted
    Given fulfillment-execution reports 2 stuck tasks of type "PICK"
    When I ask for a stranded-reservation check on task type "BOX" for SKU "SKU-4711" with shortfall threshold 5, reservation "RSV-9001" in bin "BIN-A07"
    Then the MCP tool call fails mentioning "unknown task type"
    And the MCP tool call fails mentioning "BOX"
