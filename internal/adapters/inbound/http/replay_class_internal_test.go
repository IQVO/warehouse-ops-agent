package http

import "testing"

// Every catalogue entry (sentinel-driven and standalone) declares a class the
// capture understands; the fleet template's unclassified-entry test holds the
// same from outside the package, this catches a misspelt literal at unit
// level (ADR 0033).
func TestEveryCatalogueEntryDeclaresAValidClass(t *testing.T) {
	entries := allProblemTypes()
	if len(entries) < 20 {
		t.Fatalf("only %d problem types in the catalogue: it must list every problem type of the keyed routes", len(entries))
	}
	for _, e := range entries {
		if !validReplayClass(replayClass(e.Class)) {
			t.Errorf("problem type %q (%d) declares no valid replay class (%q)", e.Slug, e.Status, e.Class)
		}
		if e.Status >= 500 && e.Class != string(classRetryable) {
			t.Errorf("problem type %q answers %d: a 5xx is never stored, so its class is RETRYABLE, got %q", e.Slug, e.Status, e.Class)
		}
	}
}
