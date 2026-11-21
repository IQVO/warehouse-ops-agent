package http_test

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	inboundhttp "github.com/claudioed/warehouse-ops-agent/internal/adapters/inbound/http"
)

const adr0033Path = "../../../../docs/docs/adr/0033-act-problem-types-have-a-replay-class.md"

// The ADR table is held equal to the catalogue: a problem type added, removed,
// reclassified or re-statused without the ADR fails here (ADR 0033).
func TestTheADRDeclaresTheClassOfEveryProblemType(t *testing.T) {
	raw, err := os.ReadFile(adr0033Path)
	if err != nil {
		t.Fatalf("the ADR of the replay classes is missing: %v", err)
	}
	row := regexp.MustCompile("(?m)^\\s*\\| `([a-z-]+)` \\| (FINAL|OUTCOME|RETRYABLE) \\| (\\d{3}) \\|$")
	got := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
		got[m[1]] = m[2] + " " + m[3]
	}
	want := map[string]string{}
	for _, e := range inboundhttp.ReplayCatalogue() {
		want[e.Slug] = e.Class + " " + strconv.Itoa(e.Status)
	}
	var diffs []string
	for slug, w := range want {
		if got[slug] != w {
			diffs = append(diffs, fmt.Sprintf("%s: the ADR says %q, the catalogue %q", slug, got[slug], w))
		}
	}
	for slug := range got {
		if _, ok := want[slug]; !ok {
			diffs = append(diffs, slug+": in the ADR, not in the catalogue")
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Fatalf("ADR 0033 differs from the catalogue:\n%s", strings.Join(diffs, "\n"))
	}
}

// One slug is one class, whichever error reaches it.
func TestOneSlugHasOneClass(t *testing.T) {
	seen := map[string]string{}
	for _, e := range inboundhttp.ReplayCatalogue() {
		if c, ok := seen[e.Slug]; ok {
			t.Errorf("%s is listed twice (%s and %s): a slug has one class", e.Slug, c, e.Class)
		}
		seen[e.Slug] = e.Class
	}
}

// OUTCOME is conservative: only the approvals' post-consume peer refusals —
// the refusals whose nonce consumption act_replay_classes_test.go proves.
func TestOnlyThePostConsumePeerRefusalsAreOutcomes(t *testing.T) {
	want := map[string]bool{
		"assignment-invalid": true, "no-pending-work": true, "assignment-rejected": true,
		"unknown-work-planning-path": true, "assignment-not-found": true,
	}
	for _, e := range inboundhttp.ReplayCatalogue() {
		if e.Class == "OUTCOME" && !want[e.Slug] {
			t.Errorf("%s is OUTCOME: only a refusal the use case proves committed state may be (ADR 0033)", e.Slug)
		}
		delete(want, e.Slug)
	}
	for slug := range want {
		t.Errorf("%s should be OUTCOME (ADR 0033) and is missing from the catalogue", slug)
	}
}
