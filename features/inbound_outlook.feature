# Derived from: docs/docs/api-surface.md (GET /inbound-outlook: four sections each {omitted?, complete, count, items[]}; `omitted` means unknown, not none; 502 only when every attempted section failed; 503 when unconfigured),
# docs/docs/adr/0021-inbound-receiving-mcp-client-and-inbound-outlook.md (read-only; stale-receipt age is an operator input with no default),
# internal/domain/policy/inbound_outlook.go (overdue, 24 h appointment horizon, stale open receipts, closed-today discrepancies).
@bdd
Feature: Inbound outlook
  The agent shows the inbound dock's near-term picture. A section it cannot produce is
  marked omitted with the reason, so an empty list is never mistaken for "nothing to
  report".

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario: ASNs awaiting arrival are flagged overdue only when their own expected arrival has passed
    Given inbound-receiving holds a Registered ASN "ASN-1" from supplier "SUP-1" expected at "2025-12-31T20:00:00Z"
    And inbound-receiving holds a Registered ASN "ASN-2" from supplier "SUP-2" expected at "2026-01-02T09:00:00Z"
    And inbound-receiving holds a Receiving ASN "ASN-3" from supplier "SUP-3" expected at "2025-12-30T09:00:00Z"
    When I request the inbound outlook
    Then the response status is 200
    And the JSON field "awaitingAsns.count" equals "2"
    And the JSON field "awaitingAsns.items.0.asnNumber" equals "ASN-1"
    And the JSON field "awaitingAsns.items.0.overdue" is true
    And the JSON field "awaitingAsns.items.1.asnNumber" equals "ASN-2"
    And the JSON field "awaitingAsns.items.1.overdue" is false
    And the JSON field "awaitingAsns.complete" is true

  Scenario: Appointments in the next 24 hours are listed and later ones are not
    Given inbound-receiving holds a Booked appointment "APPT-1" at door "D1" for carrier "ACME" from "2026-01-01T10:00:00Z" to "2026-01-01T11:00:00Z"
    And inbound-receiving holds a Booked appointment "APPT-2" at door "D2" for carrier "ACME" from "2026-01-03T10:00:00Z" to "2026-01-03T11:00:00Z"
    When I request the inbound outlook
    Then the JSON field "upcomingAppointments.count" equals "1"
    And the JSON field "upcomingAppointments.items.0.appointmentId" equals "APPT-1"

  Scenario: Without a configured stale age the stale-receipt section is omitted, not empty
    Given inbound-receiving holds an Open receipt "R-1" for ASN "ASN-1" opened at "2025-12-31T20:00:00Z"
    When I request the inbound outlook
    Then the response status is 200
    And the JSON field "staleReceipts.omitted" contains "INBOUND_STALE_RECEIPT_AGE"
    And the JSON field "staleReceipts.count" equals "0"
    And the JSON field "staleAfterSeconds" is absent

  Scenario: A receipt open longer than the configured age is stale
    Given the stale receipt age is 4 hours
    And inbound-receiving holds an Open receipt "R-1" for ASN "ASN-1" opened at "2025-12-31T22:00:00Z"
    And inbound-receiving holds an Open receipt "R-2" for ASN "ASN-2" opened at "2026-01-01T07:00:00Z"
    When I request the inbound outlook
    Then the JSON field "staleAfterSeconds" equals "14400"
    And the JSON field "staleReceipts.count" equals "1"
    And the JSON field "staleReceipts.items.0.receiptId" equals "R-1"
    And the JSON field "staleReceipts.items.0.openForSeconds" equals "36000"

  Scenario: A receipt closed today with a discrepancy is reported
    Given inbound-receiving holds a Closed receipt "R-9" for ASN "ASN-9" closed at "2026-01-01T06:30:00Z" with a "Short" discrepancy on SKU "SKU-1" expecting 10 and receiving 7
    When I request the inbound outlook
    Then the JSON field "discrepantReceipts.count" equals "1"
    And the JSON field "discrepantReceipts.items.0.receiptId" equals "R-9"
    And the JSON field "discrepantReceipts.items.0.discrepancies.0.kind" equals "Short"

  Scenario: One failing section is omitted with its reason while the others are still served
    Given inbound-receiving holds a Registered ASN "ASN-1" from supplier "SUP-1" expected at "2026-01-02T09:00:00Z"
    And inbound-receiving's receipt tools are unreachable
    When I request the inbound outlook
    Then the response status is 200
    And the JSON field "awaitingAsns.count" equals "1"
    And the JSON field "discrepantReceipts.omitted" contains "list_receipts"
    And the JSON field "discrepantReceipts.count" equals "0"

  Scenario: Every section failing is a 502
    Given the stale receipt age is 4 hours
    And inbound-receiving is unreachable
    When I request the inbound outlook
    Then the response status is 502

  Scenario: The endpoint answers 503 when inbound-receiving is not configured
    Given the "inbound-outlook" capability is not configured
    When I request the inbound outlook
    Then the response status is 503
