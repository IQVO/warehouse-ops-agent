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

// logReplacer is package-level so the replacer is built once, not per
// call. It is a strings.NewReplacer — not a strings.Map callback —
// because the replacer is the sanitizer shape CodeQL's go/log-injection
// query recognizes; this matches the sanitizeForLog helpers in this
// repo's inbound/http and usecases packages and the variant merged in
// wes-work-planning #243, which cleared the alerts.
var logReplacer = strings.NewReplacer("\n", "", "\r", "")

// Sanitize returns s with the log-forging control characters — newline
// and carriage return — removed. Every other rune, including tab and
// multi-byte UTF-8, is preserved; the output therefore never spans more
// log lines than the caller intended.
func Sanitize(s string) string {
	return logReplacer.Replace(s)
}
