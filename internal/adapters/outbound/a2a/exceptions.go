package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// findExceptionsSkill is the READ-ONLY skill id exception-management
// advertises for listing cases (its A2A adapter, skills sla_status and
// find_exceptions). It is the only skill this client ever calls; the
// zero-write fitness test allowlists it.
const findExceptionsSkill = "find_exceptions"

// openStatus is the case status this client asks for: OPEN means nobody has
// triaged or assigned the case yet, i.e. exactly what a human approving a
// release may not know about.
const openStatus = "OPEN"

// exceptionLookupTimeout bounds one lookup at the transport level; the use
// case adds its own deadline on top (ADR 0028).
const exceptionLookupTimeout = 3 * time.Second

// maxReasonRunes bounds the short reason shown per case.
const maxReasonRunes = 80

// ExceptionClient is the READ-ONLY ExceptionContext port over A2A. It
// implements no other method (pinned by internal/architecture/zerowrite),
// and every failure is ports.ErrExceptionContextUnavailable.
type ExceptionClient struct {
	peer *peer
}

// NewExceptionClient builds the lookup client for the exception-management
// agent reachable at baseURL (its root, e.g.
// http://exception-management-a2a.<ns>:8093/ or its Kong path).
func NewExceptionClient(baseURL string, httpClient *http.Client) *ExceptionClient {
	return &ExceptionClient{peer: newPeer(baseURL, httpClient, exceptionLookupTimeout, ports.ErrExceptionContextUnavailable)}
}

// exceptionsData is the data shape of the find_exceptions answer, reduced to
// the fields this client uses. The peer's case DTO is far larger (frozen
// source payload, audit trail); none of that is read or kept.
type exceptionsData struct {
	Exceptions *struct {
		Count *int            `json:"count"`
		Cases []exceptionWire `json:"cases"`
	} `json:"exceptions"`
}

type exceptionWire struct {
	CaseID     string `json:"caseId"`
	SourceType string `json:"sourceType"`
	Status     string `json:"status"`
	Severity   string `json:"severity"`
	Queue      string `json:"queue"`
	Escalated  bool   `json:"escalated"`
}

// OpenExceptions asks exception-management for its OPEN cases and returns
// the count plus a bounded, ranked list.
func (c *ExceptionClient) OpenExceptions(ctx context.Context, query ports.ExceptionContextQuery) (ports.ExceptionSummary, error) {
	ans, err := c.peer.ask(ctx, findExceptionsSkill, map[string]any{"status": openStatus})
	if err != nil {
		return ports.ExceptionSummary{}, err
	}
	if ans.problem != nil {
		return ports.ExceptionSummary{}, c.peer.fail("agent problem %d %s: %s", ans.problem.Status, ans.problem.Title, ans.problem.Detail)
	}
	var data exceptionsData
	if err := json.Unmarshal(ans.data, &data); err != nil || data.Exceptions == nil || data.Exceptions.Count == nil {
		return ports.ExceptionSummary{}, c.peer.fail("answer carries no exceptions list and no problem")
	}
	list := data.Exceptions
	if *list.Count != len(list.Cases) {
		return ports.ExceptionSummary{}, c.peer.fail("answer count %d does not match its %d cases", *list.Count, len(list.Cases))
	}
	briefs := make([]ports.ExceptionBrief, 0, len(list.Cases))
	for i, w := range list.Cases {
		if w.CaseID == "" {
			return ports.ExceptionSummary{}, c.peer.fail("case %d carries no caseId", i)
		}
		briefs = append(briefs, toBrief(w))
	}
	// Escalated first; the peer already orders newest-opened first and the
	// sort is stable, so ties keep that order.
	sort.SliceStable(briefs, func(i, j int) bool { return briefs[i].Escalated && !briefs[j].Escalated })
	if limit := clampLimit(query.Limit); len(briefs) > limit {
		briefs = briefs[:limit]
	}
	return ports.ExceptionSummary{OpenCount: *list.Count, Cases: briefs}, nil
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return ports.DefaultExceptionCases
	case n > ports.MaxExceptionCases:
		return ports.MaxExceptionCases
	}
	return n
}

func toBrief(w exceptionWire) ports.ExceptionBrief {
	return ports.ExceptionBrief{
		CaseID: w.CaseID, Status: w.Status, Queue: w.Queue, Severity: w.Severity,
		Escalated: w.Escalated, Reason: shortReason(w.SourceType),
	}
}

// shortReason reduces a CloudEvents type such as
// com.warehouse.wms.inbound-receiving.receipt.CartonDiscrepancyRaised to its
// last segment, bounded in length.
func shortReason(sourceType string) string {
	r := sourceType
	if i := strings.LastIndex(r, "."); i >= 0 {
		r = r[i+1:]
	}
	if r == "" {
		return "unknown"
	}
	if utf8.RuneCountInString(r) > maxReasonRunes {
		r = string([]rune(r)[:maxReasonRunes])
	}
	return r
}

// compile-time proof the adapter satisfies the read port.
var _ ports.ExceptionContext = (*ExceptionClient)(nil)
