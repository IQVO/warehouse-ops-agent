package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// getMasterDataGaps handles GET /master-data-gaps?kind=&cursor= (ADR 0020):
// product-master products that are unclassified or whose declared and
// measured dimensions disagree. Both query params are optional; an unknown
// kind (or a cursor product-master rejects) is a 400, product-master being
// unreachable is a 502, and an unwired use case (PRODUCT_MASTER_MCP_ENDPOINT
// unset) is a 503 -- same convention as every optional route here.
func (h *Handlers) getMasterDataGaps(w http.ResponseWriter, r *http.Request) {
	if h.MasterDataGaps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "product-master not configured"})
		return
	}
	res, err := h.MasterDataGaps.Execute(r.Context(), usecases.MasterDataGapsRequest{
		Kind:   r.URL.Query().Get("kind"),
		Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, usecases.ErrInvalidInput):
			status = http.StatusBadRequest
		case errors.Is(err, usecases.ErrProductMasterNotConfigured):
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toMasterDataGapsDTO(res))
}

// masterDataGapsDTO is the GET /master-data-gaps body. Complete is false
// when the per-request scan bound was reached; NextCursor then resumes it.
type masterDataGapsDTO struct {
	Scanned                   int                `json:"scanned"`
	Complete                  bool               `json:"complete"`
	NextCursor                string             `json:"nextCursor,omitempty"`
	UnclassifiedCount         int                `json:"unclassifiedCount"`
	DimensionDiscrepancyCount int                `json:"dimensionDiscrepancyCount"`
	Gaps                      []masterDataGapDTO `json:"gaps"`
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

type productDimsDTO struct {
	LengthMm  int64 `json:"lengthMm"`
	WidthMm   int64 `json:"widthMm"`
	HeightMm  int64 `json:"heightMm"`
	WeightG   int64 `json:"weightG"`
	VolumeMm3 int64 `json:"volumeMm3"`
}

func toProductDimsDTO(d *policy.ProductDimensionsFact) *productDimsDTO {
	if d == nil {
		return nil
	}
	return &productDimsDTO{LengthMm: d.LengthMm, WidthMm: d.WidthMm, HeightMm: d.HeightMm, WeightG: d.WeightG, VolumeMm3: d.VolumeMm3}
}

func toMasterDataGapsDTO(res usecases.MasterDataGapsResult) masterDataGapsDTO {
	r := res.Report
	out := masterDataGapsDTO{
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
	return out
}
