package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/httpclient"
)

// wellKnownPath is the Agent Card discovery path (A2A spec); served by every
// agent in this fleet behind its Kong prefix as well as in-cluster.
const wellKnownPath = "/.well-known/agent-card.json"

// problem is the peer's RFC 7807-shaped in-band rejection (a business
// answer inside the message envelope, never a JSON-RPC error).
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// answer is a peer's decoded skill answer: exactly one of data or problem
// (the fleet's skill-answer envelope; wes-work-planning ADR-0046,
// exception-management's A2A adapter). data is left raw so each client
// decodes only the shape of the skill it called.
type answer struct {
	data    json.RawMessage
	problem *problem
}

// envelope is the wire form of the skill-answer envelope.
type envelope struct {
	Skill   string          `json:"skill"`
	Data    json.RawMessage `json:"data"`
	Problem *problem        `json:"problem"`
}

// peer is the shared transport of both clients: lazy Agent Card discovery,
// one SDK client cached per peer, one skill call per ask. unreachable is
// the port sentinel every transport or off-contract failure wraps, so each
// port keeps its own typed error and the application layer never sees A2A.
//
// The protocol client is resolved on first use and cached: boot must not
// depend on a peer being up (a cold cluster starts everything at once).
type peer struct {
	baseURL     string
	http        *http.Client
	unreachable error

	mu     sync.Mutex
	client *a2aclient.Client
}

// newPeer builds the transport for the agent reachable at baseURL (trailing
// slashes normalized). A nil httpClient gets the repo's otelhttp-instrumented
// client (fitness-tested): the trace context crosses the A2A hop.
func newPeer(baseURL string, httpClient *http.Client, timeout time.Duration, unreachable error) *peer {
	if httpClient == nil {
		httpClient = httpclient.New(timeout)
	}
	return &peer{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient, unreachable: unreachable}
}

func (p *peer) fail(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{p.unreachable}, args...)...)
}

// ask sends one skill call ({"skill": ..., "arguments": ...} as a data
// part) and returns the decoded answer. Transport failures and answers that
// break the envelope contract wrap p.unreachable; a peer problem is
// returned in the answer for the caller to classify.
func (p *peer) ask(ctx context.Context, skill string, args map[string]any) (answer, error) {
	client, err := p.resolve(ctx)
	if err != nil {
		return answer{}, err
	}
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
	resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return answer{}, p.fail("SendMessage: %v", err)
	}
	agentMsg, ok := resp.(*a2a.Message)
	if !ok {
		return answer{}, p.fail("agent answered %T, want a message", resp)
	}
	if len(agentMsg.Parts) == 0 {
		return answer{}, p.fail("agent answer carries no parts")
	}
	data, ok := agentMsg.Parts[0].Content.(a2a.Data)
	if !ok {
		return answer{}, p.fail("agent answer part is %T, want data", agentMsg.Parts[0].Content)
	}
	raw, err := json.Marshal(data.Value)
	if err != nil {
		return answer{}, p.fail("decoding answer: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return answer{}, p.fail("answer is not the skill envelope: %v", err)
	}
	return answer{data: env.Data, problem: env.Problem}, nil
}

// resolve fetches the peer's Agent Card (once, cached) and builds the
// protocol client from it.
func (p *peer) resolve(ctx context.Context) (*a2aclient.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return p.client, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+wellKnownPath, nil)
	if err != nil {
		return nil, p.fail("building card request: %v", err)
	}
	res, err := p.http.Do(req)
	if err != nil {
		return nil, p.fail("fetching agent card: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, p.fail("agent card fetch answered %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, p.fail("reading agent card: %v", err)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		return nil, p.fail("agent card is not valid JSON: %v", err)
	}
	// The SDK's JSON-RPC transport reuses OUR instrumented client, so the
	// SendMessage call is traced too, not only the card fetch above.
	client, err := a2aclient.NewFromCard(ctx, &card, a2aclient.WithJSONRPCTransport(p.http))
	if err != nil {
		return nil, p.fail("building client from card: %v", err)
	}
	p.client = client
	return client, nil
}
