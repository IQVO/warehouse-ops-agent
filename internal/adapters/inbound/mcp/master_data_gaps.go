package mcp

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// --- find_master_data_gaps (ADR 0020) -----------------------------------------

// masterDataGapsInput's fields are both optional, untrusted caller input:
// kind is validated by the use case (an unknown value is a tool error,
// never "return everything"), cursor is passed to product-master as given.
type masterDataGapsInput struct {
	Kind   string `json:"kind,omitempty" jsonschema:"optional gap kind filter: unclassified or dimension-discrepancy; omit to report both"`
	Cursor string `json:"cursor,omitempty" jsonschema:"the nextCursor of a previous incomplete scan, to resume it; omit to scan from the first product"`
}

type productDimsDTO struct {
	LengthMm  int64 `json:"lengthMm"`
	WidthMm   int64 `json:"widthMm"`
	HeightMm  int64 `json:"heightMm"`
	WeightG   int64 `json:"weightG"`
	VolumeMm3 int64 `json:"volumeMm3"`
}

type masterDataGapDTO struct {
	Sku         string          `json:"sku"`
	Description string          `json:"description"`
	Version     int64           `json:"version"`
	Kinds       []string        `json:"kinds"`
	Rationale   []string        `json:"rationale"`
	Declared    *productDimsDTO `json:"declared,omitempty"`
	Measured    *productDimsDTO `json:"measured,omitempty"`
	MeasuredAt  string          `json:"measuredAt,omitempty"`
	DeviceId    string          `json:"deviceId,omitempty"`
}

type masterDataGapsOutput struct {
	Scanned                   int                `json:"scanned"`
	Complete                  bool               `json:"complete"`
	NextCursor                string             `json:"nextCursor,omitempty"`
	UnclassifiedCount         int                `json:"unclassifiedCount"`
	DimensionDiscrepancyCount int                `json:"dimensionDiscrepancyCount"`
	Gaps                      []masterDataGapDTO `json:"gaps"`
}

func (d Deps) findMasterDataGaps(ctx context.Context, in masterDataGapsInput) (masterDataGapsOutput, error) {
	res, err := d.MasterDataGaps.Execute(ctx, usecases.MasterDataGapsRequest{Kind: in.Kind, Cursor: in.Cursor})
	if err != nil {
		return masterDataGapsOutput{}, err
	}
	r := res.Report
	out := masterDataGapsOutput{
		Scanned:                   r.Scanned,
		Complete:                  res.Complete,
		NextCursor:                res.NextCursor,
		UnclassifiedCount:         r.UnclassifiedCount,
		DimensionDiscrepancyCount: r.DimensionDiscrepancyCount,
		Gaps:                      make([]masterDataGapDTO, 0, len(r.Gaps)),
	}
	for _, g := range r.Gaps {
		kinds := make([]string, 0, len(g.Kinds))
		for _, k := range g.Kinds {
			kinds = append(kinds, string(k))
		}
		out.Gaps = append(out.Gaps, masterDataGapDTO{
			Sku: g.Sku, Description: g.Description, Version: g.Version,
			Kinds: kinds, Rationale: g.Rationale,
			Declared: toProductDimsDTO(g.Declared), Measured: toProductDimsDTO(g.Measured),
			MeasuredAt: g.MeasuredAt, DeviceId: g.DeviceId,
		})
	}
	return out, nil
}

func toProductDimsDTO(d *policy.ProductDimensionsFact) *productDimsDTO {
	if d == nil {
		return nil
	}
	return &productDimsDTO{LengthMm: d.LengthMm, WidthMm: d.WidthMm, HeightMm: d.HeightMm, WeightG: d.WeightG, VolumeMm3: d.VolumeMm3}
}
