package http

import (
	"net/http"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/policy"
)

// getRuntimeSignals handles GET /runtime-signals. Nil RuntimeSignals
// (not wired by the composition root) responds 503, same convention as
// every other optional use case in this router.
func (h *Handlers) getRuntimeSignals(w http.ResponseWriter, r *http.Request) {
	if h.RuntimeSignals == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "runtime signals not configured")
		return
	}
	report := h.RuntimeSignals.Execute(r.Context())
	writeJSON(w, toRuntimeSignalsDTO(report))
}

type runtimeSignalsDTO struct {
	GeneratedAt        string             `json:"generatedAt"`
	Services           []serviceSignalDTO `json:"services"`
	UnavailableSources []string           `json:"unavailableSources,omitempty"`
}

type serviceSignalDTO struct {
	ServiceName      string  `json:"serviceName"`
	Severity         string  `json:"severity"`
	ErrorRate        float64 `json:"errorRate"`
	ErrorRateSev     string  `json:"errorRateSeverity"`
	LatencyP99MS     float64 `json:"latencyP99Ms"`
	LatencyP99Sev    string  `json:"latencyP99Severity"`
	RecentErrorLogs  int     `json:"recentErrorLogs"`
	SampleWindowMins int     `json:"sampleWindowMins"`
}

func toRuntimeSignalsDTO(r policy.RuntimeSignalsReport) runtimeSignalsDTO {
	dto := runtimeSignalsDTO{
		GeneratedAt:        r.GeneratedAt,
		UnavailableSources: r.UnavailableSources,
	}
	for _, s := range r.Services {
		dto.Services = append(dto.Services, serviceSignalDTO{
			ServiceName:      s.ServiceName,
			Severity:         string(s.Severity()),
			ErrorRate:        s.ErrorRate,
			ErrorRateSev:     string(s.ErrorRateSev),
			LatencyP99MS:     s.LatencyP99MS,
			LatencyP99Sev:    string(s.LatencyP99Sev),
			RecentErrorLogs:  s.RecentErrorLogs,
			SampleWindowMins: s.SampleWindowMins,
		})
	}
	return dto
}
