package usecases

// Structured-log and decision-fact keys reused across this package's use
// cases (fleet goconst rule, 2026-11-02: a literal three occurrences
// becomes a constant).
const (
	// keyPathID names the process-path id in log lines and flow-balance
	// decision facts.
	keyPathID = "pathId"

	// factUnavailable states, in a decision's facts map, that one signal
	// was absent (its upstream failed or is not wired). Deliberately an
	// untyped string: the facts map is compared against plain strings in
	// tests and marshaled to JSON as-is.
	factUnavailable = "unavailable"
)
