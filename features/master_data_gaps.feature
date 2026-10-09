# Derived from: docs/docs/api-surface.md (GET /master-data-gaps?kind=&cursor=: 400 unknown kind or rejected cursor, 502 product-master failure, 503 when unconfigured),
# docs/docs/adr/0020-product-master-mcp-client-and-master-data-gaps.md (read-only; never a partial report presented as complete; 5,000-product per-request bound with a resume cursor),
# internal/domain/policy/master_data_gaps.go (unclassified, dimension-discrepancy; counts cover every product scanned).
@bdd
Feature: Master-data gaps
  The agent reports product-master products whose master data is missing or
  contradictory. It only reports: it never classifies, declares or measures a product.

  Background:
    Given a warehouse-ops-agent wired to fake upstream contexts

  Scenario: Unclassified products and dimension discrepancies are both reported
    Given product-master holds product "SKU-1" described as "Widget" with no classification
    And product-master holds product "SKU-2" described as "Gadget" classified as "Fragile" whose measured dimensions disagree with the declared ones
    And product-master holds product "SKU-3" described as "Gizmo" classified as "HighValue" with consistent dimensions
    When I request the master-data gaps
    Then the response status is 200
    And the JSON field "scanned" equals "3"
    And the JSON field "complete" is true
    And the JSON field "unclassifiedCount" equals "1"
    And the JSON field "dimensionDiscrepancyCount" equals "1"
    And the JSON array "gaps" has 2 items
    And the JSON field "gaps.0.sku" equals "SKU-1"
    And the JSON array "gaps.0.kinds" has the item "unclassified"
    And the JSON field "gaps.1.sku" equals "SKU-2"
    And the JSON array "gaps.1.kinds" has the item "dimension-discrepancy"
    And the JSON field "gaps.1.declared.lengthMm" equals "100"
    And the JSON field "gaps.1.measured.lengthMm" equals "140"

  Scenario: Filtering by kind narrows the gaps but not the counts
    Given product-master holds product "SKU-1" described as "Widget" with no classification
    And product-master holds product "SKU-2" described as "Gadget" classified as "Fragile" whose measured dimensions disagree with the declared ones
    When I request the master-data gaps of kind "dimension-discrepancy"
    Then the JSON array "gaps" has 1 item
    And the JSON field "gaps.0.sku" equals "SKU-2"
    And the JSON field "unclassifiedCount" equals "1"
    And the JSON field "dimensionDiscrepancyCount" equals "1"

  Scenario: Asking only for unclassified products asks product-master only for those
    Given product-master holds product "SKU-1" described as "Widget" with no classification
    And product-master holds product "SKU-3" described as "Gizmo" classified as "HighValue" with consistent dimensions
    When I request the master-data gaps of kind "unclassified"
    Then the response status is 200
    And product-master was asked only for unclassified products
    And the JSON array "gaps" has 1 item

  Scenario: A scan that hits the per-request bound says it is incomplete and offers a resume cursor
    Given product-master holds 12 pages of one unclassified product each
    When I request the master-data gaps
    Then the response status is 200
    And the JSON field "complete" is false
    And the JSON field "scanned" equals "10"
    And the JSON field "nextCursor" equals "page-10"

  Scenario: An unknown kind is rejected, never defaulted
    When I request the master-data gaps of kind "obsolete"
    Then the response status is 400
    And the response error mentions "unknown master-data gap kind"

  Scenario: A cursor product-master rejects is the caller's input
    Given product-master rejects the cursor "garbage" as malformed
    When I request the master-data gaps resuming from cursor "garbage"
    Then the response status is 400

  Scenario: An unreachable product-master is a 502, never a partial report
    Given product-master is unreachable
    When I request the master-data gaps
    Then the response status is 502

  Scenario: The endpoint answers 503 when product-master is not configured
    Given the "master-data-gaps" capability is not configured
    When I request the master-data gaps
    Then the response status is 503
