# Derived from: docs/docs/api-surface.md (GET /daily-brief),
# docs/docs/adr/0015-core-flow-balance-and-daily-brief-adoption.md (E3 daily brief: partial-tolerant, read-only),
# docs/docs/adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md (capacityOutlook absent when planning is not configured),
# internal/domain/policy/dailybrief.go (flow_balance_risk needs two independent signals; three is critical; ranked critical-first).
@bdd
Feature: Daily brief
  The daily brief synthesises backlog, staffing, queue and stuck-task facts per
  monitored process path, grouped by facility-layout site. A path is only flagged as
  an open exception when at least two independent signals fire together, and an
  unreachable upstream degrades that fact, never the whole brief.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And facility-layout knows site "SITE-A" as "Main Warehouse"
    And the daily brief monitors path "pick-zone-a" with process path "PICK" at site "SITE-A" in building "B1" for shift "S1"

  Scenario: A healthy path is reported with its facts and no exception
    Given wes-work-planning reports backlog depth 12 and WIP 4 for path "pick-zone-a" within its alarm threshold
    And workforce-management reports 5 planned and 5 active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 7 for process path "PICK"
    And fulfillment-execution reports 0 stuck tasks
    When I request the daily brief
    Then the response status is 200
    And the JSON field "generatedAt" equals "2026-01-01T08:00:00Z"
    And the JSON field "sites.0.siteCode" equals "SITE-A"
    And the JSON field "sites.0.siteName" equals "Main Warehouse"
    And the JSON field "sites.0.paths.0.backlog.backlogDepth" equals "12"
    And the JSON field "sites.0.paths.0.staffing.understaffed" is false
    And the JSON field "sites.0.paths.0.queue.depth" equals "7"
    And the JSON array "openExceptions" is empty

  Scenario: Two correlated signals open a warning exception with its evidence
    Given wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" over its alarm threshold
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 40 for process path "PICK"
    And fulfillment-execution reports 0 stuck tasks
    When I request the daily brief
    Then the response status is 200
    And the JSON array "openExceptions" has 1 item
    And the JSON field "openExceptions.0.kind" equals "flow_balance_risk"
    And the JSON field "openExceptions.0.severity" equals "warning"
    And the JSON field "openExceptions.0.pathId" equals "pick-zone-a"
    And the JSON array "openExceptions.0.evidence" has an item containing "get_backlog_telemetry"
    And the JSON array "openExceptions.0.evidence" has an item containing "get_staffing_gap"

  Scenario: Three correlated signals escalate to a critical exception
    Given wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" over its alarm threshold
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 40 for process path "PICK"
    And fulfillment-execution reports 2 stuck tasks of type "PICK"
    When I request the daily brief
    Then the JSON field "openExceptions.0.severity" equals "critical"
    And the JSON array "openExceptions.0.evidence" has 3 items

  Scenario: A single signal alone is operating noise, not an exception
    Given wes-work-planning reports backlog depth 12 and WIP 4 for path "pick-zone-a" within its alarm threshold
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 7 for process path "PICK"
    And fulfillment-execution reports 0 stuck tasks
    When I request the daily brief
    Then the response status is 200
    And the JSON field "sites.0.paths.0.staffing.understaffed" is true
    And the JSON array "openExceptions" is empty

  Scenario: Open exceptions are ranked critical-first across paths
    Given the daily brief also monitors path "pack-zone-b" with process path "PACK" at site "SITE-A" in building "B1" for shift "S1"
    And wes-work-planning reports backlog depth 120 and WIP 30 for path "pick-zone-a" over its alarm threshold
    And workforce-management reports 5 planned and 2 active heads for path "pick-zone-a"
    And wes-work-planning reports backlog depth 90 and WIP 20 for path "pack-zone-b" over its alarm threshold
    And workforce-management reports 4 planned and 1 active heads for path "pack-zone-b"
    And fulfillment-execution reports a queue depth of 9 for process path "PICK"
    And fulfillment-execution reports a queue depth of 9 for process path "PACK"
    And fulfillment-execution reports 3 stuck tasks of type "PACK"
    When I request the daily brief
    Then the JSON array "openExceptions" has 2 items
    And the JSON field "openExceptions.0.pathId" equals "pack-zone-b"
    And the JSON field "openExceptions.0.severity" equals "critical"
    And the JSON field "openExceptions.1.pathId" equals "pick-zone-a"
    And the JSON field "openExceptions.1.severity" equals "warning"

  Scenario: An unreachable upstream degrades that fact and the brief is still produced
    Given wes-work-planning's MCP tools are unreachable
    And workforce-management reports 5 planned and 5 active heads for path "pick-zone-a"
    And fulfillment-execution reports a queue depth of 7 for process path "PICK"
    And fulfillment-execution reports 0 stuck tasks
    When I request the daily brief
    Then the response status is 200
    And the JSON field "sites.0.paths.0.backlog" is absent
    And the JSON array "sites.0.paths.0.unavailable" has an item containing "wes-work-planning"
    And the JSON field "sites.0.paths.0.staffing.activeHeads" equals "5"
    And the JSON field "sites.0.paths.0.queue.depth" equals "7"

  Scenario: An unreachable facility-layout leaves the site unnamed but the brief intact
    Given facility-layout's MCP tools are unreachable
    And wes-work-planning reports backlog depth 12 and WIP 4 for path "pick-zone-a" within its alarm threshold
    When I request the daily brief
    Then the response status is 200
    And the JSON field "sites.0.siteCode" equals "SITE-A"
    And the JSON field "sites.0.siteName" equals ""
    And the JSON field "sites.0.paths.0.backlog.backlogDepth" equals "12"

  Scenario: The capacity outlook is absent when warehouse-planning is not configured
    Given wes-work-planning reports backlog depth 12 and WIP 4 for path "pick-zone-a" within its alarm threshold
    When I request the daily brief
    Then the response status is 200
    And the JSON field "sites.0.paths.0.capacityOutlook" is absent
