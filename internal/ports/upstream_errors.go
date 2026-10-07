package ports

import "errors"

// ErrUpstreamInvalidInput marks an error returned by an outbound client when
// an upstream bounded context REJECTED the call because of its input, as
// opposed to failing: the upstream's MCP tool answered with an isError result
// whose text starts with one of the fleet's validation slugs (ADR 0018).
// Clients wrap it (errors.Is) without changing the error text; use cases that
// can attribute the rejection to their caller's input translate it to their
// own invalid-input error. It is never set for transport failures, upstream
// 5xx, not-found, conflicts, "internal-error" or an unrecognised/slug-less
// message -- those stay plain upstream errors.
var ErrUpstreamInvalidInput = errors.New("upstream rejected the input")
