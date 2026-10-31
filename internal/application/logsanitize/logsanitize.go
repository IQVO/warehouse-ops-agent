// Package logsanitize neutralizes control characters in untrusted values
// before they reach a log sink, preventing log forging (CWE-117, CWE-93):
// a crafted work-item field must not be able to inject fake log lines or
// headers. This is the fleet-wide sanitizer pattern for go/log-injection
// findings — mirror this helper rather than inlining replacements.
//
// It lives under internal/application (not internal/pkg) because the
// hexagonal dependency rules forbid the application layer from importing
// anything outside domain/application/ports, and adapters may reuse it too.
package logsanitize

import "strings"

// Sanitize returns s with the log-forging control runes — newline, carriage
// return and tab — each mapped to a single space. Every other rune, including
// multi-byte UTF-8, is preserved; the output therefore never spans more log
// lines than the caller intended.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			return r
		}
	}, s)
}
