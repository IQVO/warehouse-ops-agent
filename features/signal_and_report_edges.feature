# Derived from: warehouse-docs docs/contexts/warehouse-ops-agent/use-cases.md (Runtime signals thresholds: warning from 1 % / 1000 ms, critical from 5 % / 3000 ms,
#   worst of the two, at least warning on recent error logs, Loki limited to 50 lines; Inbound outlook rules; Master-data gaps 5,000-product bound),
#   business-context.md (runtime signals report), troubleshooting.md (unavailableSources, staleReceipts omitted rows) and the in-repo
# docs/docs/adr/0021-inbound-receiving-mcp-client-and-inbound-outlook.md, docs/docs/adr/0020-product-master-mcp-client-and-master-data-gaps.md.
# Complements runtime_signals.feature, inbound_outlook.feature and master_data_gaps.feature at the edges of every documented threshold.
@bdd
Feature: Thresholds and bounds of the signal, dock and catalogue reports
  A threshold is a rule only if the value just below it and the value on it behave
  differently. These scenarios pin each documented edge, and the "never read a missing
  fact as a clean one" rules around them.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  # ---------------------------------------------------------- runtime signals --

  Scenario Outline: Just below a warning threshold a service is normal, just below critical it is warning
    Given runtime signals monitor the services "order-management, inventory-storage"
    And service "order-management" served <total> requests of which <failed> failed with 5xx, with a p99 latency of <p99> ms
    When I request the runtime signals
    Then the JSON field "services.0.severity" equals "<severity>"
    And the JSON field "services.1.severity" equals "normal"

    Examples:
      | total | failed | p99  | severity |
      | 1000  | 9      | 999  | normal   |
      | 1000  | 49     | 200  | warning  |
      | 1000  | 0      | 2999 | warning  |

  Scenario Outline: A service takes the worse of its error-rate and latency severities and a log line never lowers it
    Given runtime signals monitor the services "order-management, inventory-storage"
    And service "order-management" served 1000 requests of which <failed> failed with 5xx, with a p99 latency of <p99> ms
    And Loki holds <logs> error log lines for service "order-management"
    When I request the runtime signals
    Then the JSON field "services.0.severity" equals "<severity>"

    Examples:
      | failed | p99  | logs | severity |
      | 10     | 3000 | 0    | critical |
      | 50     | 200  | 3    | critical |
      | 0      | 1000 | 3    | warning  |

  Scenario: A service that served no request has an error rate of zero, not a failure
    Given runtime signals monitor the services "order-management, inventory-storage"
    And service "order-management" served 0 requests of which 0 failed with 5xx, with a p99 latency of 0 ms
    When I request the runtime signals
    Then the response status is 200
    And the JSON field "services.0.severity" equals "normal"
    And the JSON field "unavailableSources" is absent

  Scenario: Error lines are counted per monitored service and another service's lines are ignored
    Given runtime signals monitor the services "order-management, inventory-storage"
    And Loki holds 2 error log lines for service "order-management"
    And Loki holds 4 error log lines for service "kiali"
    When I request the runtime signals
    Then the JSON field "services.0.recentErrorLogs" equals "2"
    And the JSON field "services.0.severity" equals "warning"
    And the JSON field "services.1.recentErrorLogs" equals "0"
    And the JSON field "services.1.severity" equals "normal"

  Scenario: Loki is asked for at most fifty error lines
    Given runtime signals monitor the services "order-management, inventory-storage"
    When I request the runtime signals
    Then Loki was asked for at most 50 lines

  # ----------------------------------------------------------- inbound outlook --

  Scenario: An ASN with no expected arrival is never overdue and is listed after the dated ones
    Given inbound-receiving holds a Registered ASN "ASN-7" from supplier "SUP-7" expected at ""
    And inbound-receiving holds a Registered ASN "ASN-8" from supplier "SUP-8" expected at "2026-01-02T09:00:00Z"
    When I request the inbound outlook
    Then the JSON field "awaitingAsns.items.0.asnNumber" equals "ASN-8"
    And the JSON field "awaitingAsns.items.1.asnNumber" equals "ASN-7"
    And the JSON field "awaitingAsns.items.1.overdue" is false

  Scenario Outline: An ASN is overdue only once its expected arrival has strictly passed
    Given inbound-receiving holds a Registered ASN "ASN-1" from supplier "SUP-1" expected at "<expected>"
    When I request the inbound outlook
    Then the JSON field "awaitingAsns.items.0.overdue" is <overdue>

    Examples:
      | expected             | overdue |
      | 2026-01-01T07:59:59Z | true    |
      | 2026-01-01T08:00:00Z | false   |

  Scenario Outline: An appointment is listed when it is Booked or CheckedIn and its window overlaps the next 24 hours
    Given inbound-receiving holds a "<state>" appointment "APPT-1" at door "D1" for carrier "ACME" from "<start>" to "<end>"
    When I request the inbound outlook
    Then the JSON field "upcomingAppointments.count" equals "<listed>"

    Examples: now is 2026-01-01T08:00:00Z, so the horizon is 2026-01-02T08:00:00Z
      | state     | start                | end                  | listed |
      | Booked    | 2026-01-01T07:00:00Z | 2026-01-01T09:00:00Z | 1      |
      | CheckedIn | 2026-01-01T07:30:00Z | 2026-01-01T10:00:00Z | 1      |
      | Booked    | 2026-01-01T06:00:00Z | 2026-01-01T08:00:00Z | 0      |
      | Booked    | 2026-01-02T07:59:59Z | 2026-01-02T09:00:00Z | 1      |
      | Booked    | 2026-01-02T08:00:00Z | 2026-01-02T09:00:00Z | 0      |
      | Completed | 2026-01-01T07:00:00Z | 2026-01-01T09:00:00Z | 0      |
      | Cancelled | 2026-01-01T09:00:00Z | 2026-01-01T10:00:00Z | 0      |

  Scenario: The appointment lookup asks inbound-receiving for exactly the next 24 hours
    When I request the inbound outlook
    Then inbound-receiving was asked for appointments from "2026-01-01T08:00:00Z" to "2026-01-02T08:00:00Z"
    And the JSON field "appointmentsUntil" equals "2026-01-02T08:00:00Z"

  Scenario Outline: A receipt is stale only when it has been open strictly longer than the configured age
    Given the stale receipt age is 4 hours
    And inbound-receiving holds an Open receipt "R-1" for ASN "ASN-1" opened at "<opened>"
    When I request the inbound outlook
    Then the JSON field "staleReceipts.count" equals "<stale>"

    Examples: now is 2026-01-01T08:00:00Z
      | opened               | stale |
      | 2026-01-01T04:00:00Z | 0     |
      | 2026-01-01T03:59:59Z | 1     |

  Scenario Outline: Only receipts closed today with a discrepancy are reported
    Given inbound-receiving holds a Closed receipt "R-9" for ASN "ASN-9" closed at "<closed>" with a "Over" discrepancy on SKU "SKU-1" expecting 10 and receiving 12
    When I request the inbound outlook
    Then the JSON field "discrepantReceipts.count" equals "<reported>"

    Examples: today is 2026-01-01 in the agent's local zone
      | closed               | reported |
      | 2026-01-01T00:00:00Z | 1        |
      | 2025-12-31T23:59:59Z | 0        |
      | 2026-01-02T00:00:00Z | 0        |

  Scenario: A receipt closed today without a discrepancy is not reported
    Given inbound-receiving holds a Closed receipt "R-10" for ASN "ASN-10" closed at "2026-01-01T07:00:00Z" with no discrepancy
    When I request the inbound outlook
    Then the JSON field "discrepantReceipts.count" equals "0"
    And the JSON field "discrepantReceipts.omitted" is absent

  Scenario: Every attempted section failing is a 502 even when the stale age was never configured
    Given inbound-receiving is unreachable
    When I request the inbound outlook
    Then the response status is 502

  Scenario Outline: A scan stops at ten pages and says so, the tenth page being the last that fits
    Given inbound-receiving holds <pages> pages of one Registered ASN each
    When I request the inbound outlook
    Then the JSON field "awaitingAsns.complete" is <complete>
    And the JSON field "awaitingAsns.count" equals "10"

    Examples:
      | pages | complete |
      | 10    | true     |
      | 11    | false    |

  # ---------------------------------------------------------- master-data gaps --

  Scenario: A product that is both unclassified and dimension-discrepant is one gap of two kinds
    Given product-master holds product "SKU-9" described as "Pallet jack" with no classification and measured dimensions that disagree with the declared ones
    When I request the master-data gaps
    Then the JSON array "gaps" has 1 item
    And the JSON array "gaps.0.kinds" has 2 items
    And the JSON field "gaps.0.kinds.0" equals "unclassified"
    And the JSON field "gaps.0.kinds.1" equals "dimension-discrepancy"
    And the JSON field "unclassifiedCount" equals "1"
    And the JSON field "dimensionDiscrepancyCount" equals "1"

  Scenario: A catalogue of exactly ten pages is a complete scan
    Given product-master holds 10 pages of one unclassified product each
    When I request the master-data gaps
    Then the JSON field "scanned" equals "10"
    And the JSON field "complete" is true
    And the JSON field "nextCursor" is absent

  Scenario: An incomplete scan resumes from its cursor and finishes
    Given product-master holds 12 pages of one unclassified product each
    When I request the master-data gaps resuming from cursor "page-10"
    Then the JSON field "scanned" equals "2"
    And the JSON field "complete" is true

  Scenario: A page that fails in the middle of a scan is a 502, never a partial report
    Given product-master holds 5 pages of one unclassified product each
    And product-master fails on the page after the cursor "page-2"
    When I request the master-data gaps
    Then the response status is 502

  Scenario: Asking for dimension discrepancies reads the whole catalogue
    Given product-master holds product "SKU-1" described as "Widget" with no classification
    When I request the master-data gaps of kind "dimension-discrepancy"
    Then the response status is 200
    And product-master was asked for every product
    And the JSON field "unclassifiedCount" equals "1"
