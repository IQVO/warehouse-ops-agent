// Package policy: master-data gap rule (ADR 0020).
//
// A master-data gap is a product whose product-master record is not yet good
// enough for the floor to rely on. Two kinds exist, both read straight off
// product-master's own published facts -- this rule never recomputes them:
//
//   - unclassified: product-master holds no handling classification, so the
//     hazmat / fragile / temperature handling of the SKU is unknown to every
//     context that consumes ProductClassified (inventory-storage,
//     fulfillment-execution, ...).
//   - dimension-discrepancy: product-master's own discrepancy flag says the
//     declared and measured unit dimensions disagree beyond its tolerance
//     (product-master ADR 0002). The tolerance is product-master's; this
//     rule only reports the flag and echoes the two dimension sets.
//
// Pure values and functions only (no ports, no I/O).
package policy

// MasterDataGapKind names one kind of master-data gap.
type MasterDataGapKind string

const (
	// GapUnclassified: the product has no handling classification.
	GapUnclassified MasterDataGapKind = "unclassified"
	// GapDimensionDiscrepancy: declared and measured dimensions disagree
	// beyond product-master's tolerance.
	GapDimensionDiscrepancy MasterDataGapKind = "dimension-discrepancy"
)

// ParseMasterDataGapKind validates an untrusted kind filter. The empty
// string is NOT a kind; callers treat "" as "no filter" before calling this.
// An unknown value is rejected, never defaulted.
func ParseMasterDataGapKind(raw string) (MasterDataGapKind, bool) {
	switch MasterDataGapKind(raw) {
	case GapUnclassified:
		return GapUnclassified, true
	case GapDimensionDiscrepancy:
		return GapDimensionDiscrepancy, true
	}
	return "", false
}

// ProductDimensionsFact is one set of unit dimensions as product-master
// reports it (millimetres, grams, cubic millimetres).
type ProductDimensionsFact struct {
	LengthMm  int64
	WidthMm   int64
	HeightMm  int64
	WeightG   int64
	VolumeMm3 int64
}

// ProductMasterFact is the slice of one product-master record this rule
// reads. Declared/Measured are nil when product-master has none.
type ProductMasterFact struct {
	Sku         string
	Description string
	Version     int64
	Classified  bool
	Discrepancy bool
	Declared    *ProductDimensionsFact
	Measured    *ProductDimensionsFact
	MeasuredAt  string
	DeviceId    string
}

// MasterDataGap is one product with at least one gap. Kinds is in the
// stable order unclassified, dimension-discrepancy; Rationale has one line
// per kind, in the same order. Declared/Measured/MeasuredAt/DeviceId are
// echoed only for a dimension-discrepancy gap.
type MasterDataGap struct {
	Sku         string
	Description string
	Version     int64
	Kinds       []MasterDataGapKind
	Rationale   []string
	Declared    *ProductDimensionsFact
	Measured    *ProductDimensionsFact
	MeasuredAt  string
	DeviceId    string
}

// MasterDataGapReport is the result of one scan: the gaps found among
// Scanned products and the per-kind counts.
type MasterDataGapReport struct {
	Scanned                   int
	UnclassifiedCount         int
	DimensionDiscrepancyCount int
	Gaps                      []MasterDataGap
}

// The rationale lines are single literals on purpose: a `+` concatenation in
// a const is reported by gremlins as an uncoverable mutant.
const (
	rationaleUnclassified = "product-master holds no handling classification for this SKU: its hazmat, fragile and temperature handling are unknown to every context that consumes ProductClassified; classify it in product-master"
	rationaleDiscrepancy  = "product-master flags a declared-vs-measured dimension discrepancy beyond its own tolerance: the measured dimensions are in effect; re-declare the product or re-measure it"
)

// FindMasterDataGaps returns the master-data gaps among products, in input
// order. only restricts the report to one kind; the empty kind reports both.
// The counts always cover every product scanned, independent of only, so a
// filtered report still says how big the other gap is.
func FindMasterDataGaps(products []ProductMasterFact, only MasterDataGapKind) MasterDataGapReport {
	report := MasterDataGapReport{Scanned: len(products), Gaps: []MasterDataGap{}}
	for _, p := range products {
		if !p.Classified {
			report.UnclassifiedCount++
		}
		if p.Discrepancy {
			report.DimensionDiscrepancyCount++
		}
		if gap, ok := gapOf(p, only); ok {
			report.Gaps = append(report.Gaps, gap)
		}
	}
	return report
}

// gapOf builds the gap entry for one product under the kind filter, or
// reports false when the product has no (selected) gap.
func gapOf(p ProductMasterFact, only MasterDataGapKind) (MasterDataGap, bool) {
	gap := MasterDataGap{Sku: p.Sku, Description: p.Description, Version: p.Version}
	if !p.Classified && only != GapDimensionDiscrepancy {
		gap.Kinds = append(gap.Kinds, GapUnclassified)
		gap.Rationale = append(gap.Rationale, rationaleUnclassified)
	}
	if p.Discrepancy && only != GapUnclassified {
		gap.Kinds = append(gap.Kinds, GapDimensionDiscrepancy)
		gap.Rationale = append(gap.Rationale, rationaleDiscrepancy)
		gap.Declared, gap.Measured = p.Declared, p.Measured
		gap.MeasuredAt, gap.DeviceId = p.MeasuredAt, p.DeviceId
	}
	return gap, len(gap.Kinds) > 0
}
