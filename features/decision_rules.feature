# Derived from: warehouse-docs docs/contexts/warehouse-ops-agent/use-cases.md (Daily brief E3 rules, Flow-balance advisory E1 decision table,
#   utilization overlay thresholds, LLM arbitration), business-context.md (two-signal correlation threshold),
#   troubleshooting.md (Daily brief and "No utilization in the flow balance" rows), and the in-repo
# docs/docs/ddd/use-cases.md + docs/docs/adr/0008-labor-utilization-advisory-correlation.md (50-deep queue, below 60 % and idle share above 40 %).
# Complements daily_brief.feature, flow_balance.feature and llm_arbitration.feature: only rules those do not already pin.
@bdd
Feature: Decision rules of the daily brief, the flow-balance advisory and its arbitration
  The agent turns upstream readings into one ranked judgement. These scenarios pin the
  documented correlation thresholds at their edges, the table rows the first suites do
  not reach, and the rules that stop a missing fact from being guessed.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And facility-layout knows site "SITE-A" as "Main Warehouse"
    And the daily brief monitors path "pick-zone-a" with process path "PICK" at site "SITE-A" in building "B1" for shift "S1"

  # ------------------------------------------------------------ daily brief --

  Scenario Outline: Any two of the three signals open a warning exception, not only backlog plus staffing
    Given wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" <threshold> its alarm threshold
    And workforce-management reports <planned> planned and <active> active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 9 for process path "PICK"
    And fulfillment-execution reports <stuck> stuck tasks of type "PICK"
    When I request the daily brief
    Then the JSON array "openExceptions" has 1 item
    And the JSON field "openExceptions.0.kind" equals "flow_balance_risk"
    And the JSON field "openExceptions.0.severity" equals "warning"
    And the JSON field "openExceptions.0.siteCode" equals "SITE-A"
    And the JSON array "openExceptions.0.evidence" has 2 items
    And the JSON array "openExceptions.0.evidence" has an item containing "<first>"
    And the JSON array "openExceptions.0.evidence" has an item containing "<second>"

    Examples:
      | threshold | planned | active | stuck | first                 | second               |
      | over      | 5       | 5      | 2     | get_backlog_telemetry | diagnose_stuck_tasks |
      | within    | 5       | 2      | 2     | get_staffing_gap      | diagnose_stuck_tasks |

  Scenario: A deep queue is reported but is never a signal of its own
    Given wes-work-planning reports backlog depth 12 and WIP 4 for path "pick-zone-a" within its alarm threshold
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 500 for process path "PICK"
    And fulfillment-execution reports 0 stuck tasks
    When I request the daily brief
    Then the response status is 200
    And the JSON field "sites.0.paths.0.queue.depth" equals "500"
    And the JSON array "openExceptions" is empty

  Scenario: Stuck tasks of another process path are not this path's problem
    Given wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" over its alarm threshold
    And workforce-management reports 5 planned and 5 active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 9 for process path "PICK"
    And fulfillment-execution reports 4 stuck tasks of type "PACK"
    When I request the daily brief
    Then the JSON field "sites.0.paths.0.stuck.count" equals "0"
    And the JSON array "openExceptions" is empty

  Scenario: A fact that failed is left out of the correlation and is named, never guessed as a signal
    Given wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" over its alarm threshold
    And workforce-management reports 5 planned and 5 active heads for path "pick-zone-a"
    And fulfillment-execution's MCP tools are unreachable
    When I request the daily brief
    Then the response status is 200
    And the JSON field "sites.0.paths.0.backlog.backlogDepth" equals "120"
    And the JSON field "sites.0.paths.0.queue" is absent
    And the JSON field "sites.0.paths.0.stuck" is absent
    And the JSON array "sites.0.paths.0.unavailable" has 2 items
    And the JSON array "sites.0.paths.0.unavailable" has an item containing "get_queue_status"
    And the JSON array "sites.0.paths.0.unavailable" has an item containing "diagnose_stuck_tasks"
    And the JSON array "openExceptions" is empty

  Scenario: The signals that did arrive still correlate while another upstream is down
    Given wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" over its alarm threshold
    And workforce-management's MCP tools are unreachable
    And fulfillment-execution reports a queue depth of 9 for process path "PICK"
    And fulfillment-execution reports 2 stuck tasks of type "PICK"
    When I request the daily brief
    Then the JSON array "sites.0.paths.0.unavailable" has an item containing "workforce-management"
    And the JSON array "openExceptions" has 1 item
    And the JSON field "openExceptions.0.severity" equals "warning"
    And the JSON array "openExceptions.0.evidence" has 2 items

  Scenario: Paths are grouped under the facility-layout site they belong to
    Given facility-layout knows site "SITE-B" as "Cross-dock"
    And the daily brief also monitors path "dock-b" with process path "PACK" at site "SITE-B" in building "B2" for shift "S1"
    And wes-work-planning reports backlog depth 12 and WIP 4 for path "pick-zone-a" within its alarm threshold
    And wes-work-planning reports backlog depth 7 and WIP 2 for path "dock-b" within its alarm threshold
    When I request the daily brief
    Then the JSON array "sites" has 2 items
    And the JSON field "sites.0.siteCode" equals "SITE-A"
    And the JSON field "sites.0.siteName" equals "Main Warehouse"
    And the JSON field "sites.0.paths.0.pathId" equals "pick-zone-a"
    And the JSON field "sites.1.siteCode" equals "SITE-B"
    And the JSON field "sites.1.siteName" equals "Cross-dock"
    And the JSON field "sites.1.paths.0.pathId" equals "dock-b"

  Scenario: The brief asks fulfillment-execution only for leases that have already expired
    Given wes-work-planning reports backlog depth 12 and WIP 4 for path "pick-zone-a" within its alarm threshold
    When I request the daily brief
    Then fulfillment-execution was asked for stuck tasks within 0 seconds

  # ----------------------------------------------------- flow-balance table --

  Scenario Outline: An understaffed path gets labor whatever the stuck-task reading says
    Given wes-work-planning recommends "<wes>" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports <planned> planned and <active> active heads for path "pick-zone-a"
    And fulfillment-execution reports <stuck> stuck tasks
    And these upstreams are unreachable: "<down>"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "assign_labor" with <heads> proposed heads
    And the JSON array "evidence" has <evidence> items

    Examples:
      | wes              | planned | active | stuck | down                 | heads | evidence |
      | ThrottleUpstream | 5       | 2      | 3     |                      | 3     | 3        |
      | ReassignLabor    | 9       | 6      | 0     | fulfillment-execution | 3     | 2        |

  Scenario Outline: A decision that lacks the signal its branch needs holds, says it is partial and names the gap
    Given wes-work-planning recommends "<wes>" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports <planned> planned and <active> active heads for path "pick-zone-a"
    And fulfillment-execution reports <stuck> stuck tasks
    And these upstreams are unreachable: "<down>"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "hold" with 0 proposed heads
    And the decision is partial
    And the JSON array "missingSignals" has the item "<missing>"
    And the JSON array "evidence" has 2 items

    Examples:
      | wes            | planned | active | stuck | down                  | missing                                    |
      | ReassignLabor  | 4       | 4      | 0     | fulfillment-execution | fulfillment-execution.diagnose_stuck_tasks |
      | NoActionNeeded | 4       | 4      | 0     | workforce-management  | workforce-management.get_staffing_gap      |

  Scenario: Stuck tasks alone settle a healthy-backlog path even when staffing is unknown
    Given wes-work-planning recommends "NoActionNeeded" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management's MCP tools are unreachable
    And fulfillment-execution reports 2 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "hold" with 0 proposed heads
    And the decision is not partial
    And the rationale mentions "2 stuck task(s)"

  Scenario: An understaffed flag always proposes at least one head
    Given wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management flags path "pick-zone-a" understaffed although 4 planned and 4 active heads
    And fulfillment-execution reports 0 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 1 proposed heads

  Scenario: The stuck-task diagnostic looks back fifteen minutes
    Given wes-work-planning recommends "NoActionNeeded" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management reports 4 planned and 4 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then fulfillment-execution was asked for stuck tasks within 900 seconds

  # ------------------------------------------------------ utilization overlay --

  Scenario Outline: The overlay fires only when queue depth and utilization clearly agree, and never moves the lever
    Given path "pick-zone-a" is bound to process path "PICK"
    And wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth <backlog> and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And labor-performance reports <pct> percent utilization for task type "PICK" with <task> task seconds and <idle> idle seconds
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the utilization overlay is "<kind>"

    Examples: queue is deep above 50, utilization is low below 60 percent, idle share must be above 40 percent
      | backlog | pct  | task | idle | kind                   |
      | 51      | 59.9 | 590  | 410  | claim_flow_problem     |
      | 50      | 59.9 | 590  | 410  | starvation             |
      | 51      | 60   | 600  | 400  | staffing_gap_confirmed |
      | 50      | 60   | 600  | 400  | none                   |
      | 51      | 59   | 600  | 400  | none                   |

  Scenario: A path with no process-path binding gets no overlay and labor-performance is not asked
    Given wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And labor-performance reports 30 percent utilization for task type "PICK" with 300 task seconds and 700 idle seconds
    When I request the flow-balance advisory for path "pick-zone-a-unbound"
    Then the utilization overlay is "none"
    And labor-performance was not asked

  Scenario: Labor-performance is asked about the task type the path is bound to
    Given path "pack-zone-b" is bound to process path "PACK"
    And wes-work-planning recommends "ReassignLabor" for path "pack-zone-b" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pack-zone-b"
    And fulfillment-execution reports 0 stuck tasks
    And labor-performance reports 90 percent utilization for task type "PACK" with 900 task seconds and 100 idle seconds
    When I request the flow-balance advisory for path "pack-zone-b"
    Then labor-performance was asked for task type "PACK"
    And the utilization overlay is "staffing_gap_confirmed"

  Scenario: Without the wes anchor there is no queue-depth reading and so no overlay
    Given path "pick-zone-a" is bound to process path "PICK"
    And wes-work-planning's MCP tools are unreachable
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And labor-performance reports 30 percent utilization for task type "PICK" with 300 task seconds and 700 idle seconds
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "hold" with 0 proposed heads
    And the utilization overlay is "none"

  # ------------------------------------------------------------ arbitration --

  Scenario: A model plan replaces the lever but the decision stays as partial as the deterministic pass saw it
    Given wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management's MCP tools are unreachable
    And fulfillment-execution reports 0 stuck tasks
    And the LLM mode is "on"
    And a reasoner is wired that proposes "assign_labor" with 2 heads and rationale "cover the gap until the roster is confirmed"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 2 proposed heads
    And the decision source is "llm"
    And the decision is partial
    And the JSON array "missingSignals" has the item "workforce-management.get_staffing_gap"
    And the JSON array "evidence" has 2 items
