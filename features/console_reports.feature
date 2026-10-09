# Derived from: docs/docs/api-surface.md (GET /console/reports/wms and /wes: optional RFC3339 from/to defaulting to the trailing 24 h; 400 if malformed or `to` not after `from`; each section degrades independently),
# docs/docs/adr/0003-console-bff-report-dashboards.md (sections, chart kinds, availability and freshness annotations; null efficiency is skipped, never plotted as 0),
# internal/application/usecases/console_reports*.go (aggregation rules and display order).
@bdd
Feature: Console report dashboards
  The console-bff assembles chart-ready dashboards from every context's analytics
  reader. A dead or unconfigured reader costs the operator one panel, never the screen,
  and a panel always says whether it is available and why not.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario: The WMS dashboard aggregates three sections in display order
    Given order-management's funnel report shows 10 received, 7 allocated, 1 partially allocated, 6 released, 2 cancelled and 1 allocation failed
    And inventory-storage's flow accuracy report shows 40 stowed, 25 picked, 3 discrepancies and 1 unlocated
    And facility-layout's catalog growth report has 12 slots registered on day "2026-01-01T00:00:00Z"
    And facility-layout's catalog growth report has 8 slots registered on day "2025-12-31T00:00:00Z"
    When I open the WMS dashboard
    Then the response status is 200
    And the dashboard lists the sections "order-funnel, inventory-flow-accuracy, catalog-growth"
    And the dashboard section "order-funnel" is available with the series "Received=10, Allocated=8, Released=6, Cancelled / Failed=3"
    And the JSON field "sections.0.chartKind" equals "funnel"
    And the dashboard section "inventory-flow-accuracy" is available with the series "Stowed=40, Picked=25, Discrepancies=3, Unlocated=1"
    And the dashboard section "catalog-growth" is available with the series "2025-12-31T00:00:00Z=8, 2026-01-01T00:00:00Z=12"

  Scenario: The WES dashboard aggregates four sections in display order
    Given wes-work-planning's throughput report has 5 completed work units in hour "2026-01-01T06:00:00Z"
    And wes-work-planning's throughput report has 7 completed work units in hour "2026-01-01T06:00:00Z"
    And wes-work-planning's throughput report has 4 completed work units in hour "2026-01-01T07:00:00Z"
    And fulfillment-execution's throughput report has 30 completions for task type "PICK"
    And fulfillment-execution's throughput report has 22 completions for task type "PACK"
    And workforce-management's labor report shows 4 shifts started, 9 labor assigned and 1 understaffing events
    And labor-performance's report shows task type "PICK" at 87.5 percent mean efficiency
    When I open the WES dashboard
    Then the response status is 200
    And the dashboard lists the sections "planning-throughput, fulfillment-throughput, labor-management, labor-performance"
    And the dashboard section "planning-throughput" is available with the series "2026-01-01T06:00:00Z=12, 2026-01-01T07:00:00Z=4"
    And the dashboard section "fulfillment-throughput" is available with the series "PACK=22, PICK=30"
    And the dashboard section "labor-management" is available with the series "Shifts Started=4, Labor Assigned=9, Understaffing Events=1"
    And the dashboard section "labor-performance" is available with the series "PICK=87.5"

  Scenario: A task type with nothing scorable gets no bar, never a zero bar
    Given labor-performance's report shows task type "PICK" at 87.5 percent mean efficiency
    And labor-performance's report shows task type "PACK" with no scorable tasks
    When I open the WES dashboard
    Then the dashboard section "labor-performance" is available with the series "PICK=87.5"

  Scenario: An unreachable reader degrades its own section only
    Given the "order-management" reports reader is unreachable
    And inventory-storage's flow accuracy report shows 40 stowed, 25 picked, 3 discrepancies and 1 unlocated
    When I open the WMS dashboard
    Then the response status is 200
    And the dashboard section "order-funnel" is unavailable with the error "order-management reports not available"
    And the dashboard section "inventory-flow-accuracy" is available with the series "Stowed=40, Picked=25, Discrepancies=3, Unlocated=1"

  Scenario: An unconfigured reader is reported as not configured, not as an outage
    Given the "labor-performance" reports reader is not configured
    When I open the WES dashboard
    Then the response status is 200
    And the dashboard section "labor-performance" is unavailable with the error "labor-performance reports not configured"
    And the dashboard section "planning-throughput" is available with the series ""

  Scenario: Freshness is an annotation and its failure does not degrade the section
    Given the "order-management" reports freshness is 42 seconds
    And the "inventory-storage" reports freshness is unavailable
    When I open the WMS dashboard
    Then the dashboard section "order-funnel" reports a freshness lag of 42 seconds
    And the dashboard section "inventory-flow-accuracy" has no freshness lag
    And the JSON field "sections.1.available" is true

  Scenario: Without a window the dashboard covers the trailing 24 hours
    When I open the WMS dashboard
    Then the JSON field "from" equals "2025-12-31T08:00:00Z"
    And the JSON field "to" equals "2026-01-01T08:00:00Z"
    And the JSON field "generatedAt" equals "2026-01-01T08:00:00Z"

  Scenario: An explicit window is echoed back
    When I open the WES dashboard from "2025-12-01T00:00:00Z" to "2025-12-02T00:00:00Z"
    Then the response status is 200
    And the JSON field "from" equals "2025-12-01T00:00:00Z"
    And the JSON field "to" equals "2025-12-02T00:00:00Z"

  Scenario: A malformed timestamp is rejected, never silently replaced by the default
    When I open the WMS dashboard from "yesterday" to "2026-01-01T00:00:00Z"
    Then the response status is 400
    And the response error mentions "RFC3339"

  Scenario: An inverted window is rejected before any upstream is asked
    When I open the WMS dashboard from "2026-01-02T00:00:00Z" to "2026-01-01T00:00:00Z"
    Then the response status is 400
    And the response error mentions "must be after"

  Scenario Outline: The dashboards answer 503 when the console reports are not configured
    Given the "console-reports" capability is not configured
    When I GET "<path>"
    Then the response status is 503

    Examples:
      | path                 |
      | /console/reports/wms |
      | /console/reports/wes |
