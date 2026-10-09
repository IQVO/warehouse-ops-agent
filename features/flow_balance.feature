# Derived from: docs/docs/api-surface.md (GET /flow-balance/{pathId}: 400 on a use-case error, 503 if not wired),
# docs/docs/adr/0015-core-flow-balance-and-daily-brief-adoption.md (E1 correlation, partial-tolerant),
# docs/docs/adr/0008-labor-utilization-advisory-correlation.md (additive utilization overlay),
# internal/domain/policy/flow_balance.go (closed lever vocabulary: assign_labor, release_next_work, hold; unrecognised wes action is rejected, never defaulted),
# internal/domain/policy/utilization_correlation.go (claim_flow_problem, starvation, staffing_gap_confirmed).
@bdd
Feature: Flow-balance advisory (deterministic policy layer)
  For one process path the agent correlates wes-work-planning's rebalance
  recommendation, workforce-management's staffing gap and fulfillment-execution's
  stuck tasks into exactly one lever from a closed vocabulary. The agent only
  recommends: "hold" is its escalation to a human when the evidence does not
  support pulling a lever.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario Outline: The policy ranks one lever from the correlated signals
    Given wes-work-planning recommends "<wes>" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports <planned> planned and <active> active heads for path "pick-zone-a"
    And fulfillment-execution reports <stuck> stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "<action>" with <heads> proposed heads
    And the decision source is "deterministic"
    And the decision is not partial
    And the rationale mentions "<rationale>"

    Examples:
      | wes              | planned | active | stuck | action            | heads | rationale                                  |
      | ReassignLabor    | 5       | 2      | 0     | assign_labor      | 3     | confirms the path is understaffed          |
      | ThrottleUpstream | 5       | 4      | 0     | assign_labor      | 1     | confirms the path is understaffed          |
      | ReassignLabor    | 4       | 4      | 3     | hold              | 0     | blocked claims, not a labor gap            |
      | ReassignLabor    | 4       | 4      | 0     | hold              | 0     | neither a staffing gap nor stuck tasks     |
      | NoActionNeeded   | 4       | 4      | 0     | release_next_work | 0     | releasing the next work unit is safe       |
      | NoActionNeeded   | 4       | 4      | 2     | hold              | 0     | 2 stuck task(s)                            |
      | NoActionNeeded   | 4       | 2      | 0     | hold              | 0     | flags a staffing gap; holding              |

  Scenario: Every decision shows the readings that drove it
    Given wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the JSON array "evidence" has 3 items
    And the evidence cites "wes-work-planning.get_rebalance_recommendation"
    And the evidence cites "workforce-management.get_staffing_gap"
    And the evidence cites "fulfillment-execution.diagnose_stuck_tasks"

  Scenario: The staffing lookup is scoped to the requested building and shift
    Given wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a" in building "B7" shift "NIGHT"
    Then the response status is 200
    And workforce-management was asked about building "B7" and shift "NIGHT"

  Scenario: Without the anchor signal the agent holds and says what is missing
    Given wes-work-planning's MCP tools are unreachable
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "hold" with 0 proposed heads
    And the decision is partial
    And the JSON array "missingSignals" has the item "wes-work-planning.get_rebalance_recommendation"

  Scenario: A missing staffing signal makes the agent hold rather than guess a labor gap
    Given wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management's MCP tools are unreachable
    And fulfillment-execution reports 0 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "hold" with 0 proposed heads
    And the decision is partial
    And the JSON array "missingSignals" has the item "workforce-management.get_staffing_gap"

  Scenario: A missing stuck-task diagnostic stops the agent from calling a path healthy
    Given wes-work-planning recommends "NoActionNeeded" for path "pick-zone-a" with backlog depth 10 and WIP 2
    And workforce-management reports 4 planned and 4 active heads for path "pick-zone-a"
    And fulfillment-execution's MCP tools are unreachable
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "hold" with 0 proposed heads
    And the decision is partial
    And the JSON array "missingSignals" has the item "fulfillment-execution.diagnose_stuck_tasks"

  Scenario: An unrecognised wes action is untrusted input and is rejected, never defaulted
    Given wes-work-planning recommends "SelfDestruct" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 400
    And the response error mentions "unrecognized RebalanceAction"

  Scenario Outline: Labor-utilization overlay corroborates or reframes the lever without changing it
    Given path "pick-zone-a" is bound to process path "PICK"
    And wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth <backlog> and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And labor-performance reports <pct> percent utilization for task type "PICK" with <task> task seconds and <idle> idle seconds
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the JSON field "utilization.kind" equals "<kind>"
    And the evidence cites "labor-performance.get_task_type_utilization"

    Examples:
      | backlog | pct | task | idle | kind                  |
      | 120     | 30  | 300  | 700  | claim_flow_problem    |
      | 10      | 25  | 300  | 900  | starvation            |
      | 120     | 90  | 900  | 100  | staffing_gap_confirmed |

  Scenario: An unreachable labor-performance leaves the decision exactly as it was
    Given path "pick-zone-a" is bound to process path "PICK"
    And wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And labor-performance's MCP tools are unreachable
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 200
    And the recommended action is "assign_labor" with 3 proposed heads
    And the JSON field "utilization" is absent

  Scenario: Nothing observed in the window is never read as zero percent utilization
    Given path "pick-zone-a" is bound to process path "PICK"
    And wes-work-planning recommends "ReassignLabor" for path "pick-zone-a" with backlog depth 120 and WIP 30
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports 0 stuck tasks
    And labor-performance observed nothing for task type "PICK"
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the recommended action is "assign_labor" with 3 proposed heads
    And the JSON field "utilization" is absent

  Scenario: The advisory answers 503 when it is not configured
    Given the "flow-balance" capability is not configured
    When I request the flow-balance advisory for path "pick-zone-a"
    Then the response status is 503
    And the response error mentions "not configured"
