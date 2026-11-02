package http

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// RFC 9457 problem details for every error this adapter answers. The REST
// edge used to answer {"error": "<text>"} with the raw error string —
// including any upstream tool-error prose. It now answers
// application/problem+json whose detail is caller-safe by construction:
// handler-authored sentences, or a fixed sentence plus the upstream's ADR
// 0018 classification slug. Upstream free text never crosses this
// boundary (portfolio rec, 2026-11-02).
type problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// problemSpec maps each status this adapter emits to its problem "type"
// (a stable, dereferenceable-by-convention URI) and RFC-title.
func problemSpec(status int) (typ, title string) {
	switch status {
	case http.StatusBadRequest:
		return "https://problems.warehouse-ops-agent.io/bad-request", "Bad Request"
	case http.StatusNotFound:
		return "https://problems.warehouse-ops-agent.io/not-found", "Not Found"
	case http.StatusConflict:
		return "https://problems.warehouse-ops-agent.io/conflict", "Conflict"
	case http.StatusGone:
		return "https://problems.warehouse-ops-agent.io/gone", "Gone"
	case http.StatusUnsupportedMediaType:
		return "https://problems.warehouse-ops-agent.io/unsupported-media-type", "Unsupported Media Type"
	case http.StatusRequestEntityTooLarge:
		return "https://problems.warehouse-ops-agent.io/payload-too-large", "Payload Too Large"
	case http.StatusInternalServerError:
		return "https://problems.warehouse-ops-agent.io/internal", "Internal Server Error"
	case http.StatusBadGateway:
		return "https://problems.warehouse-ops-agent.io/upstream-unavailable", "Upstream Unavailable"
	case http.StatusServiceUnavailable:
		return "https://problems.warehouse-ops-agent.io/not-configured", "Service Unavailable"
	default:
		return "about:blank", http.StatusText(status)
	}
}

// writeProblem answers with an RFC 9457 application/problem+json body.
// detail must already be caller-safe (see callerDetail and upstreamDetail).
func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	typ, title := problemSpec(status)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type: typ, Title: title, Status: status, Detail: detail, Instance: r.URL.Path,
	})
}

// upstreamErrorSlugRe extracts the ADR 0018 classification slug from an
// error chain that carries a *mcpclient.ToolError: those format as
// "<upstream>: tool <tool> reported an error: <slug>: <prose>". The marker
// keeps the match inside the tool-error text, so service names ("wes-work-
// planning: tool ...") and use-case prose are never mistaken for a slug.
var upstreamErrorSlugRe = regexp.MustCompile(`reported an error: ([a-z][a-z0-9]*(?:-[a-z0-9]+)*): `)

// leadingSlugRe matches an error whose text IS an ADR 0018 tool error
// ("<slug>: <prose>") — the shape a raw upstream failure has when it
// reaches this adapter unwrapped by the ToolError formatter.
var leadingSlugRe = regexp.MustCompile(`^([a-z][a-z0-9]*(?:-[a-z0-9]+)*): `)

// upstreamSlug returns the upstream tool-error classification slug carried
// by err, "" when there is none. The marker expression runs first so a
// ToolError's upstream name ("facility-layout: tool ... reported an
// error: ...") is never mistaken for the slug.
func upstreamSlug(err error) string {
	msg := err.Error()
	if m := upstreamErrorSlugRe.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	if m := leadingSlugRe.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// callerDetail sanitizes an error the caller is responsible for (invalid
// input, malformed parameters). Such errors are authored by this agent's
// own use cases and carry no upstream prose — except when a validation
// slug from the upstream is wrapped in, in which case only the slug is
// quoted back, never the prose after it.
func callerDetail(err error) string {
	if slug := upstreamSlug(err); slug != "" {
		return "invalid request (" + slug + ")"
	}
	return err.Error()
}

// upstreamDetail sanitizes an upstream failure: a fixed sentence naming
// the service, plus the ADR 0018 slug when the upstream supplied one.
// The upstream's own error prose never reaches the response body.
func upstreamDetail(service string, err error) string {
	if slug := upstreamSlug(err); slug != "" {
		return service + " rejected the request (" + slug + ")"
	}
	return service + " is unavailable"
}
