package main_test

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// Steps for the optional MCP contexts: master-data gaps (product-master),
// transfer watch (network-inventory-planning) and the inbound outlook
// (inbound-receiving).

// ---------------------------------------------------------------- Given ----

func (w *world) productMasterHoldsUnclassified(sku, description string) error {
	w.productMaster.products = append(w.productMaster.products, ports.Product{
		Sku: sku, Description: description, Version: 1,
		PhysicalProfile: ports.ProductPhysicalProfile{EffectiveSource: "none"},
	})
	return nil
}

func (w *world) productMasterHoldsClassified(sku, description, tag, consistency string) error {
	declared := &ports.ProductDimensions{LengthMm: 100, WidthMm: 50, HeightMm: 30, WeightG: 200, VolumeMm3: 150000}
	measured := &ports.ProductMeasurement{LengthMm: 100, WidthMm: 50, HeightMm: 30, WeightG: 200, VolumeMm3: 150000, MeasuredAt: "2025-12-30T10:00:00Z", DeviceId: "scanner-7"}
	discrepancy := consistency == "disagree"
	if discrepancy {
		measured.LengthMm, measured.VolumeMm3 = 140, 210000
	}
	w.productMaster.products = append(w.productMaster.products, ports.Product{
		Sku: sku, Description: description, Version: 3,
		Classification: &ports.ProductClassification{HandlingTags: []string{tag}, ClassificationSource: "native"},
		PhysicalProfile: ports.ProductPhysicalProfile{
			Declared: declared, Measured: measured, EffectiveSource: "measured", Discrepancy: discrepancy,
		},
	})
	return nil
}

func (w *world) productMasterHoldsGeneratedPages(n int) error {
	w.productMaster.generated = n
	return nil
}

func (w *world) productMasterRejectsTheCursor(cursor string) error {
	w.productMaster.rejectCursor = cursor
	return nil
}

func (w *world) productMasterIsUnreachable() error {
	w.productMaster.down = true
	return nil
}

func (w *world) nipHoldsTransfer(id, state string, qty int, sku, origin, destination string) error {
	t := ports.Transfer{
		Id: id, State: state, SKU: sku, Quantity: qty, OriginSiteId: origin, DestinationSiteId: destination,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T01:00:00Z",
	}
	w.nip.transfers = append(w.nip.transfers, t)
	w.nip.details[id] = ports.TransferDetail{Transfer: t}
	return nil
}

func (w *world) transferHasAnAuditEntry(id, from, to, cause string) error {
	d, ok := w.nip.details[id]
	if !ok {
		return fmt.Errorf("no transfer %q has been given", id)
	}
	d.Audit = append(d.Audit, ports.TransferAuditEntry{
		Seq: int64(len(d.Audit) + 1), From: from, To: to, Event: "StateChanged", Cause: cause, OccurredAt: "2026-01-01T01:00:00Z",
	})
	w.nip.details[id] = d
	return nil
}

func (w *world) nipSimulatesSite(site string, demand int, capacity float64, headroom int) error {
	w.nip.sites = append(w.nip.sites, ports.SiteSimulation{
		Site: site, OriginEnabled: true, DestinationEnabled: true, TotalDemand: demand,
		CapacityOverWindow: capacity, CapacityHeadroom: headroom,
		WindowStart: "2026-01-01T00:00:00Z", WindowEnd: "2026-01-08T00:00:00Z",
	})
	return nil
}

func (w *world) nipIsUnreachable() error {
	w.nip.down = true
	return nil
}

func (w *world) inboundHoldsAsn(state, asn, supplier, expected string) error {
	w.inbound.asns = append(w.inbound.asns, ports.InboundAsn{AsnNumber: asn, SupplierRef: supplier, ExpectedArrival: expected, State: state, Version: 1})
	return nil
}

func (w *world) inboundHoldsAppointment(id, door, carrier, start, end string) error {
	w.inbound.appointments = append(w.inbound.appointments, ports.InboundAppointment{
		AppointmentID: id, DoorCode: door, Carrier: carrier, WindowStart: start, WindowEnd: end, State: "Booked", Version: 1,
	})
	return nil
}

func (w *world) inboundHoldsOpenReceipt(id, asn, opened string) error {
	w.inbound.receipts = append(w.inbound.receipts, ports.InboundReceipt{
		ReceiptID: id, AsnNumber: asn, DoorCode: "D1", State: "Open", OpenedAt: opened, Version: 1,
	})
	return nil
}

func (w *world) inboundHoldsClosedReceipt(id, asn, closed, kind, sku string, expected, received int64) error {
	closedAt, err := time.Parse(time.RFC3339, closed)
	if err != nil {
		return err
	}
	w.inbound.receipts = append(w.inbound.receipts, ports.InboundReceipt{
		ReceiptID: id, AsnNumber: asn, DoorCode: "D1", State: "Closed",
		OpenedAt: closedAt.Add(-time.Hour).Format(time.RFC3339), ClosedAt: closed,
		Discrepancies: []ports.InboundDiscrepancy{{LineNo: 1, Sku: sku, Kind: kind, ExpectedQty: expected, ReceivedQty: received}},
		Version:       2,
	})
	return nil
}

func (w *world) theStaleReceiptAgeIs(hours int) error {
	w.staleAge = time.Duration(hours) * time.Hour
	return nil
}

func (w *world) inboundReceiptToolsAreUnreachable() error {
	w.inbound.receiptsDown = true
	return nil
}

func (w *world) inboundIsUnreachable() error {
	w.inbound.down = true
	return nil
}

// ----------------------------------------------------------------- When ----

func (w *world) iRequestTheMasterDataGaps(ctx context.Context) error {
	return w.get(ctx, "/master-data-gaps")
}

func (w *world) iRequestTheMasterDataGapsOfKind(ctx context.Context, kind string) error {
	return w.get(ctx, "/master-data-gaps?"+url.Values{"kind": {kind}}.Encode())
}

func (w *world) iRequestTheMasterDataGapsFromCursor(ctx context.Context, cursor string) error {
	return w.get(ctx, "/master-data-gaps?"+url.Values{"cursor": {cursor}}.Encode())
}

func (w *world) iListTheStuckTransfers(ctx context.Context, minutes int) error {
	return w.get(ctx, fmt.Sprintf("/transfer-watch/stuck?olderThanMinutes=%d", minutes))
}

func (w *world) iLookUpTheStatusOfTransfer(ctx context.Context, id string) error {
	return w.get(ctx, "/transfer-watch/transfers/"+url.PathEscape(id))
}

func (w *world) iAskForTheNetworkImbalance(ctx context.Context) error {
	return w.get(ctx, "/transfer-watch/imbalance")
}

func (w *world) iRequestTheInboundOutlook(ctx context.Context) error {
	return w.get(ctx, "/inbound-outlook")
}

// ----------------------------------------------------------------- Then ----

func (w *world) productMasterWasAskedOnlyForUnclassified() error {
	if c := w.productMaster.lastQuery.Classified; c == nil || *c {
		return fmt.Errorf("expected product-master to be asked for classified=false, got %v", c)
	}
	return nil
}

func (w *world) nipWasAskedForTransfersOlderThan(minutes int) error {
	if got := w.nip.lastStuck.OlderThanMinutes; got != minutes {
		return fmt.Errorf("expected network-inventory-planning to be asked for transfers older than %d minutes, got %d", minutes, got)
	}
	return nil
}

func (w *world) nipWasNotCalled() error {
	if w.nip.calls != 0 {
		return fmt.Errorf("expected network-inventory-planning not to be called, got %d call(s)", w.nip.calls)
	}
	return nil
}

func registerOptionalContextSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^product-master holds product "([^"]*)" described as "([^"]*)" with no classification$`, w.productMasterHoldsUnclassified)
	sc.Step(`^product-master holds product "([^"]*)" described as "([^"]*)" classified as "([^"]*)" whose measured dimensions disagree with the declared ones$`, w.productMasterHoldsDisagreeing)
	sc.Step(`^product-master holds product "([^"]*)" described as "([^"]*)" classified as "([^"]*)" with consistent dimensions$`, w.productMasterHoldsConsistent)
	sc.Step(`^product-master holds (\d+) pages of one unclassified product each$`, w.productMasterHoldsGeneratedPages)
	sc.Step(`^product-master rejects the cursor "([^"]*)" as malformed$`, w.productMasterRejectsTheCursor)
	sc.Step(`^product-master is unreachable$`, w.productMasterIsUnreachable)
	sc.Step(`^network-inventory-planning holds transfer "([^"]*)" in state "([^"]*)" for (\d+) units of SKU "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.nipHoldsTransfer)
	sc.Step(`^transfer "([^"]*)" has an audit entry moving it from "([^"]*)" to "([^"]*)" because "([^"]*)"$`, w.transferHasAnAuditEntry)
	sc.Step(`^network-inventory-planning simulates site "([^"]*)" with demand (\d+) against capacity ([\d.]+) and headroom (-?\d+)$`, w.nipSimulatesSite)
	sc.Step(`^network-inventory-planning is unreachable$`, w.nipIsUnreachable)
	sc.Step(`^inbound-receiving holds a (Registered|Receiving) ASN "([^"]*)" from supplier "([^"]*)" expected at "([^"]*)"$`, w.inboundHoldsAsn)
	sc.Step(`^inbound-receiving holds a Booked appointment "([^"]*)" at door "([^"]*)" for carrier "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.inboundHoldsAppointment)
	sc.Step(`^inbound-receiving holds an Open receipt "([^"]*)" for ASN "([^"]*)" opened at "([^"]*)"$`, w.inboundHoldsOpenReceipt)
	sc.Step(`^inbound-receiving holds a Closed receipt "([^"]*)" for ASN "([^"]*)" closed at "([^"]*)" with a "([^"]*)" discrepancy on SKU "([^"]*)" expecting (\d+) and receiving (\d+)$`, w.inboundHoldsClosedReceipt)
	sc.Step(`^the stale receipt age is (\d+) hours$`, w.theStaleReceiptAgeIs)
	sc.Step(`^inbound-receiving's receipt tools are unreachable$`, w.inboundReceiptToolsAreUnreachable)
	sc.Step(`^inbound-receiving is unreachable$`, w.inboundIsUnreachable)

	sc.Step(`^I request the master-data gaps$`, w.iRequestTheMasterDataGaps)
	sc.Step(`^I request the master-data gaps of kind "([^"]*)"$`, w.iRequestTheMasterDataGapsOfKind)
	sc.Step(`^I request the master-data gaps resuming from cursor "([^"]*)"$`, w.iRequestTheMasterDataGapsFromCursor)
	sc.Step(`^I list the transfers stuck for more than (\d+) minutes$`, w.iListTheStuckTransfers)
	sc.Step(`^I look up the status of transfer "([^"]*)"$`, w.iLookUpTheStatusOfTransfer)
	sc.Step(`^I ask for the network imbalance$`, w.iAskForTheNetworkImbalance)
	sc.Step(`^I request the inbound outlook$`, w.iRequestTheInboundOutlook)

	sc.Step(`^product-master was asked only for unclassified products$`, w.productMasterWasAskedOnlyForUnclassified)
	sc.Step(`^network-inventory-planning was asked for transfers older than (\d+) minutes$`, w.nipWasAskedForTransfersOlderThan)
	sc.Step(`^network-inventory-planning was not called$`, w.nipWasNotCalled)
}

func (w *world) productMasterHoldsDisagreeing(sku, description, tag string) error {
	return w.productMasterHoldsClassified(sku, description, tag, "disagree")
}

func (w *world) productMasterHoldsConsistent(sku, description, tag string) error {
	return w.productMasterHoldsClassified(sku, description, tag, "consistent")
}
