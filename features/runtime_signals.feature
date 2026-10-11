# Derived from: docs/docs/api-surface.md (GET /runtime-signals: warning >= 1% error rate / >= 1000 ms p99, critical >= 5% / >= 3000 ms, any recent error log lifts a service to at least warning; a failing Prometheus or Loki is listed in unavailableSources instead of failing the request; 503 if not wired),
# docs/docs/adr/0014-runtime-signals-and-stranded-reservation-adoption.md,
# internal/domain/policy/runtime_signals.go (thresholds, worst-of severity).
@bdd
Feature: Runtime signals
  The agent reads Prometheus and Loki and classifies each monitored service. When an
  observability source is down the report says so and keeps what it has.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts
    And runtime signals monitor the services "order-management, inventory-storage"

  Scenario Outline: Error rate and p99 latency are classified against the fleet thresholds
    Given service "order-management" served <total> requests of which <failed> failed with 5xx, with a p99 latency of <p99> ms
    When I request the runtime signals
    Then the response status is 200
    And the JSON field "services.0.serviceName" equals "order-management"
    And the JSON field "services.0.severity" equals "<severity>"
    And the JSON field "services.0.sampleWindowMins" equals "10"
    And the JSON field "services.1.severity" equals "normal"

    Examples:
      | total | failed | p99  | severity |
      | 1000  | 5      | 200  | normal   |
      | 1000  | 10     | 200  | warning  |
      | 1000  | 50     | 200  | critical |
      | 1000  | 0      | 1000 | warning  |
      | 1000  | 0      | 3000 | critical |

  Scenario: A recent error log lifts an otherwise healthy service to warning
    Given Loki holds 2 error log lines for service "order-management"
    When I request the runtime signals
    Then the JSON field "services.0.severity" equals "warning"
    And the JSON field "services.0.recentErrorLogs" equals "2"
    And the JSON field "unavailableSources" is absent

  Scenario: An unreachable Loki is listed as unavailable and the metrics are still reported
    Given service "order-management" served 1000 requests of which 50 failed with 5xx, with a p99 latency of 200 ms
    And Loki is unreachable
    When I request the runtime signals
    Then the response status is 200
    And the JSON array "unavailableSources" has the item "loki"
    And the JSON field "services.0.severity" equals "critical"

  Scenario: An unreachable Prometheus is listed as unavailable
    Given Prometheus is unreachable
    When I request the runtime signals
    Then the response status is 200
    And the JSON array "unavailableSources" has the item "prometheus"
    And the JSON array "services" has 2 items

  Scenario: An unconfigured Loki is reported as unavailable, not as a clean bill of health
    Given Loki is not configured
    When I request the runtime signals
    Then the response status is 200
    And the JSON array "unavailableSources" has the item "loki"

  Scenario: The endpoint answers 503 when it is not configured
    Given the "runtime-signals" capability is not configured
    When I request the runtime signals
    Then the response status is 503
