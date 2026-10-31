// Package a2a is the outbound Agent2Agent adapter, built on the official
// a2a-go/v2 SDK. It is the ONLY package in this agent allowed to import the
// SDK (internal/architecture/zerowrite pins that) and it holds exactly TWO
// clients over one shared transport (peer.go: real Agent Card discovery,
// the repo's otelhttp-instrumented http client, strict decoding of the
// peer's in-band envelope and problems):
//
//   - Client (this file) implements the WRITE port WorkReleaser: the act
//     slice's human-confirmed release, delegated to wes-work-planning
//     (ADR 0027, that agent's ADR-0046). It is the single write-port
//     implementation.
//   - ExceptionClient (exceptions.go) implements the READ-ONLY port
//     ExceptionContext: an advisory question to exception-management
//     (ADR 0028). It implements no write verb and calls one read skill.
//
// The card is fetched from the well-known path and the protocol client is
// built from it, so the wire contract is the one the peer advertises, not
// an assumption. This package depends inward on the ports only; it never
// touches the domain. It speaks JSON-RPC 2.0 over HTTP (the transport the
// peers' cards declare) and is safe for concurrent use.
package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// releaseSkill is the skill id the wes-work-planning agent advertises for
// admitting the next work unit (its ADR-0046 card).
const releaseSkill = "release_next_work"

// Client is the WorkReleaser port over A2A (the act slice's one write
// transport). The protocol client is resolved lazily from the peer's Agent
// Card on first use (see peer): boot must not depend on the planning agent
// being up, but a card that cannot be resolved surfaces as
// ports.ErrWorkPlanningUnreachable on the requests that need it.
type Client struct {
	baseURL string
	peer    *peer
}

// NewClient builds the delegation client for the planning agent reachable at
// baseURL (the agent's root, e.g. http://wes-work-planning-a2a.<ns>:8093/ or
// its Kong path). baseURL's trailing slash is normalized.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	p := newPeer(baseURL, httpClient, 10*time.Second, ports.ErrWorkPlanningUnreachable)
	return &Client{baseURL: p.baseURL, peer: p}
}

// releasedWorkData is the data shape of the wes-work-planning release skill
// answer (its ADR-0046 wire contract). A local copy by design: if the
// peer's contract drifts, this adapter fails loudly instead of misreading.
type releasedWorkData struct {
	ReleasedWork *struct {
		WorkUnitId string `json:"workUnitId"`
		CPT        string `json:"cpt"`
		Ref        string `json:"ref"`
	} `json:"releasedWork"`
}

// ReleaseNextWork delegates "admit the next unit for path" to the planning
// agent and maps its answer onto the port's typed outcomes.
func (c *Client) ReleaseNextWork(ctx context.Context, pathId string) (ports.ReleasedWork, error) {
	ans, err := c.peer.ask(ctx, releaseSkill, map[string]any{"pathId": pathId})
	if err != nil {
		return ports.ReleasedWork{}, err
	}
	if ans.problem != nil {
		return ports.ReleasedWork{}, mapProblem(ans.problem)
	}
	var data releasedWorkData
	if err := json.Unmarshal(ans.data, &data); err != nil || data.ReleasedWork == nil {
		return ports.ReleasedWork{}, fmt.Errorf("%w: answer carries no releasedWork and no problem", ports.ErrWorkPlanningUnreachable)
	}
	rw := data.ReleasedWork
	return ports.ReleasedWork{WorkUnitId: rw.WorkUnitId, CPT: rw.CPT, Ref: rw.Ref}, nil
}

// mapProblem maps the peer's RFC 7807-shaped in-band problem onto this
// context's typed delegation outcomes, preserving its detail text.
func mapProblem(p *problem) error {
	switch p.Status {
	case 404:
		return fmt.Errorf("%w: %s", ports.ErrUnknownWorkPlanningPath, p.Detail)
	case 409:
		return fmt.Errorf("%w: %s", ports.ErrNoPendingWork, p.Detail)
	default:
		return fmt.Errorf("%w: agent problem %d %s: %s", ports.ErrWorkPlanningUnreachable, p.Status, p.Title, p.Detail)
	}
}

// compile-time proof the adapter satisfies the port.
var _ ports.WorkReleaser = (*Client)(nil)
