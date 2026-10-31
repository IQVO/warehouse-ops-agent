// Package usecases (this file): the shared honesty envelope of the two
// ADR-0026 network outlooks (NetworkOrderOutlook, NetworkNodeOutlook).
// It mirrors InboundOutlook's own envelope (ADR 0021) exactly; a separate
// type keeps the inbound names honest about which use case owns them.
package usecases

// OutlookSectionStatus is the honesty envelope of one network-outlook
// section, identical in meaning to InboundSectionStatus (ADR 0021):
// Omitted is non-empty when the section could not be produced (an upstream
// failure, a dependency that never became known, or an unconfigured
// client) and says why; its list is then empty and MUST NOT be read as
// "nothing to report". Complete is false when the section's list is a
// lower bound (the per-request read bound was reached, or some records
// could not be read), never a claim of exhaustiveness.
type OutlookSectionStatus struct {
	Omitted  string
	Complete bool
}

// OutlookSection is one section: its status envelope and its items.
type OutlookSection[T any] struct {
	OutlookSectionStatus
	Items []T
}

// notConfiguredReason is the Omitted value of a section whose upstream
// client is not configured (ADR 0023 fail-open wiring): the section is
// omitted with this reason, never an error.
func notConfiguredReason(upstream string) string {
	return "not-configured: " + upstream + " endpoint is unset"
}

// outlookScanBound: the per-request read bounds the two outlooks share.
const (
	// networkPageSize is the list page size the outlooks request: the
	// retail-network upstreams' own maximum, so a scan needs as few calls
	// as possible.
	networkPageSize = 500
	// networkMaxJourneys bounds how many package journeys one order
	// outlook reads (get_journey per tracking number). Beyond the bound
	// the section reports Complete=false with the journeys it has. The
	// listings scan reuses the inbound outlook's own 10-page bound
	// (inboundMaxPages, ADR 0021) via scanInboundPages.
	networkMaxJourneys = 20
)
