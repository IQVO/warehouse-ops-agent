package mcpclient

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// The fleet's MCP tool-error convention (ADR 0018), first used by
// warehouse-planning and adopted by facility-layout: the text of an isError
// tool result is "<slug>: <detail>", where <slug> is the upstream's REST
// problem slug (the last segment of its RFC 7807 "type") and unexpected
// failures are "internal-error: ...". The slug -- never the prose -- is the
// only thing this package inspects.

// toolErrorSlug matches a leading kebab-case slug followed by ": ". Anything
// else (prose, a pre-convention message, leading whitespace) has no slug.
var toolErrorSlug = regexp.MustCompile(`^([a-z][a-z0-9]*(?:-[a-z0-9]+)*): `)

// validationSlugPrefixes, validationSlugSuffixes and validationSlugs are the
// EXPLICIT table of slugs that mean "the request itself was invalid". Every
// other slug -- *-not-found, duplicate-*, already-*, internal-error, the
// semantic 422 families (unknown-*, no-route, ...) -- is deliberately absent:
// those keep their upstream-failure classification (502 on the REST edge).
var (
	validationSlugPrefixes = []string{"malformed-", "invalid-"}
	validationSlugSuffixes = []string{"-required"}
	validationSlugs        = map[string]struct{}{
		"validation-failed":     {},
		"missing-location-code": {}, // facility-layout: blank/absent location code
	}
)

// classifyToolErrorText extracts the leading slug of a tool-error text
// ("" when there is none) and reports whether that slug is a validation slug.
func classifyToolErrorText(text string) (slug string, invalid bool) {
	m := toolErrorSlug.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	slug = m[1]
	if _, ok := validationSlugs[slug]; ok {
		return slug, true
	}
	for _, p := range validationSlugPrefixes {
		if strings.HasPrefix(slug, p) {
			return slug, true
		}
	}
	for _, s := range validationSlugSuffixes {
		if strings.HasSuffix(slug, s) {
			return slug, true
		}
	}
	return slug, false
}

// ToolError is the error returned when an upstream tool answers with an
// isError result. Its text is exactly what callTool has always returned, so
// no message, log line or omitted-reason changes; it additionally exposes
// the upstream slug and, through errors.Is, classifies validation rejections
// as ports.ErrUpstreamInvalidInput.
type ToolError struct {
	Upstream string
	Tool     string
	Text     string
	// Slug is the leading "<slug>:" of Text, "" for a slug-less message.
	Slug string

	invalidInput bool
	notFound     bool
}

func newToolError(upstream, tool, text string) *ToolError {
	slug, invalid := classifyToolErrorText(text)
	return &ToolError{
		Upstream: upstream, Tool: tool, Text: text, Slug: slug,
		invalidInput: invalid,
		notFound:     strings.HasSuffix(slug, "-not-found"),
	}
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("%s: tool %s reported an error: %s", e.Upstream, e.Tool, e.Text)
}

// Is makes errors.Is(err, ports.ErrUpstreamInvalidInput) true for a
// validation-slug rejection only, and errors.Is(err,
// ports.ErrUpstreamNotFound) true for a "*-not-found" slug only (ADR 0019).
func (e *ToolError) Is(target error) bool {
	switch target {
	case ports.ErrUpstreamInvalidInput:
		return e.invalidInput
	case ports.ErrUpstreamNotFound:
		return e.notFound
	default:
		return false
	}
}
