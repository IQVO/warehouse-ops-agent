package http

import "net/http"

// The verb route POST /labor-reallocation/proposals/{proposalId}/approve is
// replaced by .../approvals (fleet REST guideline 1.4, approve -> approvals)
// and kept, unchanged and keyless, until its Sunset date so a caller that
// still uses it does not break (ADR 0031). Every answer on it, errors
// included, carries:
//
//   - Deprecation (RFC 9745): the instant it was deprecated, 2026-11-10T00:00:00Z;
//   - Sunset (RFC 8594): the instant after which it may stop answering;
//   - Link rel="successor-version": the replacement, relative to the request
//     path so it stays right behind any gateway prefix.
//
// It is deliberately absent from apis/openapi.yaml: the contract lists only
// the guideline routes.
const (
	approveDeprecatedAt = "@1794268800"
	approveSunset       = "Wed, 10 Feb 2027 00:00:00 GMT"
	approveSuccessor    = `<approvals>; rel="successor-version"`
)

// deprecatedRoute marks every response of the wrapped route deprecated.
func deprecatedRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Deprecation", approveDeprecatedAt)
		h.Set("Sunset", approveSunset)
		h.Set("Link", approveSuccessor)
		next.ServeHTTP(w, r)
	})
}
