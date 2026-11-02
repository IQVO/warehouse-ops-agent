package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// assignLaborSkill is the skill id the workforce-management agent
// advertises for placing one associate on a path (its ADR-0039 card, tag
// `write`, authority L3).
const assignLaborSkill = "assign_labor"

// laborAssignTimeout bounds one delegation at the transport level.
const laborAssignTimeout = 10 * time.Second

// LaborClient is the LaborAssigner port over A2A: the labor-reallocation act
// slice's one write transport (ADR 0029). It implements no other write verb
// and calls exactly one skill; the zero-write fitness tests pin both.
type LaborClient struct {
	peer *peer
}

// NewLaborClient builds the delegation client for the workforce-management
// agent reachable at baseURL (its root, e.g.
// http://workforce-management-a2a.<ns>:8093/ or its Kong path).
func NewLaborClient(baseURL string, httpClient *http.Client) *LaborClient {
	return &LaborClient{peer: newPeer(baseURL, httpClient, laborAssignTimeout, ports.ErrWorkforceAgentUnreachable)}
}

// laborAssignedData is the data shape of the assign_labor answer
// (workforce-management ADR-0039): a local copy by design, so a drifting
// peer contract fails loudly here instead of being misread.
type laborAssignedData struct {
	LaborAssigned *struct {
		AssociateId  string `json:"associateId"`
		PathId       string `json:"pathId"`
		AssignmentId string `json:"assignmentId"`
	} `json:"laborAssigned"`
}

// AssignLabor delegates "place this associate on this path" to the
// workforce-management agent and maps its answer onto the port's typed
// outcomes. The attribution arguments (proposalId, approvedBy, agentId) are
// always sent: they are how the peer records who asked and who approved.
func (c *LaborClient) AssignLabor(ctx context.Context, req ports.AssignLaborRequest) (ports.LaborAssignment, error) {
	ans, err := c.peer.ask(ctx, assignLaborSkill, map[string]any{
		"associateId": req.AssociateId,
		"pathId":      req.PathId,
		"proposalId":  req.ProposalId,
		"approvedBy":  req.ApprovedBy,
		"agentId":     req.AgentId,
	})
	if err != nil {
		return ports.LaborAssignment{}, err
	}
	if ans.problem != nil {
		return ports.LaborAssignment{}, mapLaborProblem(ans.problem)
	}
	var data laborAssignedData
	if err := json.Unmarshal(ans.data, &data); err != nil || data.LaborAssigned == nil {
		return ports.LaborAssignment{}, fmt.Errorf("%w: answer carries no laborAssigned and no problem", ports.ErrWorkforceAgentUnreachable)
	}
	la := data.LaborAssigned
	return ports.LaborAssignment{AssociateId: la.AssociateId, PathId: la.PathId, AssignmentId: la.AssignmentId}, nil
}

// mapLaborProblem maps the peer's RFC 7807-shaped in-band problem onto this
// context's typed outcomes, preserving its detail text: 409 is a domain rule
// (certification, break, shift ended, ...), 404 an unknown associate or
// path, 400 refused arguments; anything else is an upstream failure.
func mapLaborProblem(p *problem) error {
	switch p.Status {
	case 404:
		return fmt.Errorf("%w: %s", ports.ErrAssignmentNotFound, p.Detail)
	case 409:
		return fmt.Errorf("%w: %s", ports.ErrAssignmentRejected, p.Detail)
	case 400:
		return fmt.Errorf("%w: %s", ports.ErrAssignmentInvalid, p.Detail)
	default:
		return fmt.Errorf("%w: agent problem %d %s: %s", ports.ErrWorkforceAgentUnreachable, p.Status, p.Title, p.Detail)
	}
}

// compile-time proof the adapter satisfies the port.
var _ ports.LaborAssigner = (*LaborClient)(nil)
