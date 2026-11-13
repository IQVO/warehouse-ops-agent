//go:build integration

// Boot-level integration test for the REAL composition root: cmd/agent's
// run(), booted end to end over DOUBLED upstreams.
//
// warehouse-ops-agent owns no database and no Kafka (AGENTS.md: "a pure
// decision-policy layer ... holds no database"); its only integration seams
// are HTTP and MCP. Per AGENTS.md and .claude/skills/how-to-test/SKILL.md
// ("No Kafka/Postgres integration tests in this repo"), testcontainers is
// inapplicable here by design — the fleet's container rule covers stateful
// surfaces, and this repo has none. Instead, every documented upstream MCP
// endpoint the daily brief reads (ADR 0007's wiring, AGENTS.md's env table)
// is pointed at an in-process double: a real go-sdk MCP server (the same
// Streamable-HTTP production handler the two existing integration test
// files fake with) wrapped in a recorder that captures EVERY request, so
// the zero-write contract (internal/architecture/zerowrite, pinned at
// source level there) is additionally pinned HERE at the wire level: the
// fakes see only MCP protocol traffic, never a mutating verb.
//
// No env-gates, no skips: `go test -tags=integration ./cmd/agent/` boots
// the agent, drives both documented read skills (GET /daily-brief and the
// get_daily_brief MCP tool over /mcp, docs/docs/mcp/tools.md), then
// SIGTERMs the process (run() takes no context; serveAgent drains in-flight
// requests for up to 10s per its own doc comment).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- the doubled upstreams -------------------------------------------------

// recordedRequest is one request a double saw on the wire.
type recordedRequest struct {
	Method    string // HTTP verb
	RPCMethod string // JSON-RPC "method" when the body carries one
	ToolName  string // tools/call target
	SessionID string // Mcp-Session-Id header value
	HasBody   bool
}

// upstreamDouble is one doubled upstream: a real SDK MCP server answering
// canned tool replies over the production Streamable-HTTP handler, with a
// recording middleware in front so every request the agent issues is kept
// for the wire-level zero-write audit.
type upstreamDouble struct {
	Name string
	URL  string

	mu       sync.Mutex
	requests []recordedRequest
}

func (d *upstreamDouble) record(r recordedRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, r)
}

func (d *upstreamDouble) snapshot() []recordedRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]recordedRequest, len(d.requests))
	copy(out, d.requests)
	return out
}

// newUpstreamDouble serves tools (name -> canned JSON reply) the same way
// internal/adapters/outbound/mcpclient/mcpclient_integration_test.go's
// upstreamServer does, plus the recording wrapper this test adds.
func newUpstreamDouble(t *testing.T, name string, tools map[string]string) *upstreamDouble {
	t.Helper()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: name, Version: "1.0.0"}, nil)
	for tool, reply := range tools {
		reply := reply
		sdkmcp.AddTool(server, &sdkmcp.Tool{Name: tool, Description: "doubled read tool"},
			func(ctx context.Context, req *sdkmcp.CallToolRequest, in map[string]any) (*sdkmcp.CallToolResult, map[string]any, error) {
				var payload map[string]any
				if err := json.Unmarshal([]byte(reply), &payload); err != nil {
					return nil, nil, err
				}
				return nil, payload, nil
			})
	}

	d := &upstreamDouble{Name: name}
	inner := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		entry := recordedRequest{
			Method:    r.Method,
			SessionID: r.Header.Get("Mcp-Session-Id"),
			HasBody:   len(body) > 0,
		}
		var envelope struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			entry.RPCMethod = envelope.Method
			if envelope.Method == "tools/call" {
				entry.ToolName, _ = envelope.Params["name"].(string)
			}
		}
		d.record(entry)
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)
	d.URL = hs.URL
	return d
}

// --- local mirrors of the documented brief DTOs ----------------------------

// The JSON the router serves for GET /daily-brief and the get_daily_brief
// tool both return (docs/docs/mcp/tools.md: "Same answer as GET
// /daily-brief"); mirrored locally so the inbound DTOs stay free to evolve.
type dailyBriefBody struct {
	Sites          []briefSite      `json:"sites"`
	OpenExceptions []briefException `json:"openExceptions"`
}

type briefSite struct {
	SiteCode string      `json:"siteCode"`
	SiteName string      `json:"siteName"`
	Paths    []briefPath `json:"paths"`
}

type briefPath struct {
	PathId      string           `json:"pathId"`
	ProcessPath string           `json:"processPath"`
	Backlog     *briefBacklog    `json:"backlog"`
	Staffing    *briefStaffing   `json:"staffing"`
	Queue       *briefQueue      `json:"queue"`
	Stuck       *briefStuck      `json:"stuck"`
	Unavailable []string         `json:"unavailable"`
	Exceptions  []briefException `json:"exceptions"`
}

type briefBacklog struct {
	BacklogDepth       int  `json:"backlogDepth"`
	WIP                int  `json:"wip"`
	OverAlarmThreshold bool `json:"overAlarmThreshold"`
}

type briefStaffing struct {
	PlannedHeads int  `json:"plannedHeads"`
	ActiveHeads  int  `json:"activeHeads"`
	Understaffed bool `json:"understaffed"`
}

type briefQueue struct {
	Depth int `json:"depth"`
}

type briefStuck struct {
	Count int `json:"count"`
}

type briefException struct {
	Kind     string   `json:"kind"`
	Severity string   `json:"severity"`
	Evidence []string `json:"evidence"`
}

// --- helpers ---------------------------------------------------------------

// freeLocalAddr reserves an ephemeral 127.0.0.1 port and hands it back for
// AGENT_ADDR, so the booted agent never collides with a fixed test port.
func freeLocalAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a local port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return addr
}

// waitUntilHealthy polls the documented probe endpoint (GET /healthz
// answers {"status":"ok"}, internal/adapters/inbound/http/router.go) until
// the booted agent is serving.
func waitUntilHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK {
				var probe struct {
					Status string `json:"status"`
				}
				if json.Unmarshal(body, &probe) == nil && probe.Status == "ok" {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the booted agent never answered GET %s/healthz", base)
}

// fetchDailyBrief drives the documented REST read skill.
func fetchDailyBrief(t *testing.T, base string) dailyBriefBody {
	t.Helper()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(base + "/daily-brief")
	if err != nil {
		t.Fatalf("GET /daily-brief against the booted agent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /daily-brief: status %d, body %s", resp.StatusCode, body)
	}
	var body dailyBriefBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode the daily brief: %v", err)
	}
	return body
}

// callDailyBriefTool drives the documented MCP read skill over the agent's
// own /mcp mount (the same Streamable-HTTP surface a model host uses) and
// returns the tool's structured content decoded through the same DTO.
//
// The client pins protocol 2025-11-25 — the same LEGACY version the agent's
// own outbound sessions pin (internal/adapters/outbound/mcpclient/session.go:
// "every deployed server accepts" it). Driving /mcp at the go-sdk's modern
// default instead exposes a KNOWN, REPORTED composition bug this test does
// NOT pin: the inbound session's protocol version rides the tool handler's
// context into the outbound initialize's MCP-Protocol-Version header
// (go-sdk v1.8.0 streamable.go's context fallback), and stateful upstream
// servers — the fleet standard per that same comment — then reject the
// initialize, degrading every read into the brief's `unavailable` list
// while the REST path stays healthy. See the PR body.
func callDailyBriefTool(t *testing.T, endpoint string) dailyBriefBody {
	t.Helper()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "boot-it-host", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &sdkmcp.StreamableClientTransport{
		Endpoint: endpoint, DisableStandaloneSSE: true,
	}, &sdkmcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatalf("connect to the agent's own /mcp endpoint: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "get_daily_brief"})
	if err != nil {
		t.Fatalf("tools/call get_daily_brief over /mcp: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_daily_brief answered a tool error: %+v", res)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal the tool's structured content: %v", err)
	}
	var body dailyBriefBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode the tool's structured content: %v", err)
	}
	return body
}

// assertDoubledFacts pins that the brief is COMPOSED from the doubles'
// replies: every fact below can only exist because run() wired the real
// outbound mcpclient adapters to the faked endpoints and correlated the
// results through the real policy layer (three firing signals => one
// critical flow_balance_risk exception, internal/domain/policy's
// deriveExceptions rule).
func assertDoubledFacts(t *testing.T, label string, body dailyBriefBody) {
	t.Helper()
	if len(body.Sites) != 1 {
		t.Fatalf("%s: expected the one monitored site, got %+v", label, body.Sites)
	}
	site := body.Sites[0]
	if site.SiteCode != "WH1" || site.SiteName != "Doubled Site One" {
		t.Errorf("%s: site identity must come from the facility-layout double, got %q/%q", label, site.SiteCode, site.SiteName)
	}
	if len(site.Paths) != 1 {
		t.Fatalf("%s: expected the one monitored path, got %+v", label, site.Paths)
	}
	p := site.Paths[0]
	if p.PathId != "pick-zone-a" || p.ProcessPath != "PICK" {
		t.Errorf("%s: path identity mismatch: %+v", label, p)
	}
	if p.Backlog == nil || p.Backlog.BacklogDepth != 12 || p.Backlog.WIP != 4 || !p.Backlog.OverAlarmThreshold {
		t.Errorf("%s: backlog fact must mirror the wes-work-planning double, got %+v", label, p.Backlog)
	}
	if p.Staffing == nil || p.Staffing.PlannedHeads != 3 || p.Staffing.ActiveHeads != 1 || !p.Staffing.Understaffed {
		t.Errorf("%s: staffing fact must mirror the workforce-management double, got %+v", label, p.Staffing)
	}
	if p.Queue == nil || p.Queue.Depth != 7 {
		t.Errorf("%s: queue fact must mirror the fulfillment-execution double, got %+v", label, p.Queue)
	}
	if p.Stuck == nil || p.Stuck.Count != 1 {
		t.Errorf("%s: stuck fact must mirror the fulfillment-execution double, got %+v", label, p.Stuck)
	}
	if len(p.Unavailable) != 0 {
		t.Errorf("%s: every configured upstream answered, so unavailable must be empty, got %v", label, p.Unavailable)
	}

	// Three independent signals fire together (over-alarm, understaffed,
	// one stuck task): the correlation rule must surface exactly one
	// CRITICAL flow-balance-risk exception whose evidence names the
	// upstream reads it came from.
	if len(body.OpenExceptions) != 1 {
		t.Fatalf("%s: expected exactly one correlated exception, got %+v", label, body.OpenExceptions)
	}
	e := body.OpenExceptions[0]
	if e.Kind != "flow_balance_risk" || e.Severity != "critical" {
		t.Errorf("%s: exception kind/severity mismatch: %+v", label, e)
	}
	for _, upstream := range []string{"get_backlog_telemetry", "get_staffing_gap", "diagnose_stuck_tasks"} {
		found := false
		for _, ev := range e.Evidence {
			if bytes.Contains([]byte(ev), []byte(upstream)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: exception evidence must cite %s, got %v", label, upstream, e.Evidence)
		}
	}
}

// assertReadOnlyWire is the wire-level zero-write pin: every request every
// double saw must be Streamable-HTTP MCP protocol traffic — a POST carrying
// a handshake or tools/call JSON-RPC method, the protocol's own SSE
// listening stream (GET, always with the Mcp-Session-Id the handshake
// established; router.go's own mount comment: "POST for messages, GET for
// the SSE stream"), or the protocol's session-termination DELETE (session
// id set, no body). Any other verb, or any other JSON-RPC method, fails.
// This is the runtime counterpart of internal/architecture/zerowrite's
// static AST rule.
func assertReadOnlyWire(t *testing.T, doubles ...*upstreamDouble) {
	t.Helper()
	allowedRPC := map[string]bool{
		"initialize":                true,
		"notifications/initialized": true,
		"tools/call":                true,
	}
	for _, d := range doubles {
		reqs := d.snapshot()
		if len(reqs) == 0 {
			t.Fatalf("%s double recorded no traffic: the booted agent never dialled it", d.Name)
		}
		for _, r := range reqs {
			switch r.Method {
			case http.MethodPost:
				if !allowedRPC[r.RPCMethod] {
					t.Errorf("%s: POST carried non-protocol JSON-RPC method %q — the agent may only speak the MCP read protocol upstream", d.Name, r.RPCMethod)
				}
			case http.MethodGet:
				if r.SessionID == "" {
					t.Errorf("%s: GET without an Mcp-Session-Id is not the protocol's SSE listening stream", d.Name)
				}
			case http.MethodDelete:
				if r.SessionID == "" || r.HasBody {
					t.Errorf("%s: DELETE is only lawful as MCP session termination (session id set, no body), got id=%q hasBody=%v", d.Name, r.SessionID, r.HasBody)
				}
			default:
				t.Errorf("%s: the agent issued HTTP %s to an upstream — only the MCP protocol's own verbs may leave this process", d.Name, r.Method)
			}
		}
	}
}

// assertCalledTools proves the audit cannot pass vacuously: each double
// must have served every documented read tool the brief consumes.
func assertCalledTools(t *testing.T, d *upstreamDouble, want ...string) {
	t.Helper()
	served := map[string]bool{}
	for _, r := range d.snapshot() {
		if r.ToolName != "" {
			served[r.ToolName] = true
		}
	}
	for _, w := range want {
		if !served[w] {
			t.Errorf("%s: the documented read tool %s never reached the wire", d.Name, w)
		}
	}
}

// --- the boot test ----------------------------------------------------------

// TestRun_CompositionRootBootsOverDoubledUpstreams is the end-to-end
// composition gap the portfolio flagged: nothing exercised cmd/agent's real
// run() wiring before. It boots the actual composition root (env config ->
// every outbound client -> use cases -> both inbound adapters), waits for
// the documented probe endpoint, drives BOTH documented daily-brief read
// skills against data that can only come from the doubles, SIGTERMs the
// process and asserts a clean drain, then audits every wire request the
// doubles recorded.
func TestRun_CompositionRootBootsOverDoubledUpstreams(t *testing.T) {
	wes := newUpstreamDouble(t, "wes-work-planning", map[string]string{
		"get_backlog_telemetry": `{"pathId":"pick-zone-a","backlogDepth":12,"wip":4,"mode":"ReleaseFed","overAlarmThreshold":true}`,
	})
	fe := newUpstreamDouble(t, "fulfillment-execution", map[string]string{
		"get_queue_status":     `{"processPath":"PICK","depth":7}`,
		"diagnose_stuck_tasks": `{"count":1,"tasks":[{"taskId":"t-1","type":"PICK","reason":"lease expired"}]}`,
	})
	wfm := newUpstreamDouble(t, "workforce-management", map[string]string{
		"get_staffing_gap": `{"buildingId":"wh1","shiftId":"shift-1","pathId":"pick-zone-a","plannedHeads":3,"activeHeads":1,"understaffed":true}`,
	})
	facility := newUpstreamDouble(t, "facility-layout", map[string]string{
		"list_sites": `{"sites":[{"code":"WH1","name":"Doubled Site One"}]}`,
	})

	addr := freeLocalAddr(t)
	t.Setenv("AGENT_ADDR", addr)
	t.Setenv("WES_WORK_PLANNING_MCP_ENDPOINT", wes.URL)
	t.Setenv("FULFILLMENT_EXECUTION_MCP_ENDPOINT", fe.URL)
	t.Setenv("WORKFORCE_MANAGEMENT_MCP_ENDPOINT", wfm.URL)
	t.Setenv("FACILITY_LAYOUT_MCP_ENDPOINT", facility.URL)
	// The monitored path is pinned explicitly (a set-but-empty or malformed
	// DAILY_BRIEF_PATH_TARGETS is a startup error by design, ADR 0017).
	t.Setenv("DAILY_BRIEF_PATH_TARGETS", `[{"siteCode":"WH1","pathId":"pick-zone-a","processPath":"PICK","buildingId":"wh1","shiftId":"shift-1"}]`)
	// Every other documented endpoint stays unset: the composition root must
	// boot fail-open without them (ADR 0023), which this test exercises for
	// the remaining ~23 upstreams, both A2A act slices (ADR 0027/0029) and
	// the reasoner (LLM_MODE defaults to off).

	var runErr error
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		runErr = run()
	}()

	// Guarantee the SIGTERM dance below always drains run(), even when an
	// assertion fails first, so no booted agent outlives the test.
	var termOnce sync.Once
	t.Cleanup(func() {
		termOnce.Do(func() {
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		})
		select {
		case <-returned:
		case <-time.After(15 * time.Second):
			t.Error("run() did not drain within 15s of SIGTERM")
		}
	})

	base := "http://" + addr
	waitUntilHealthy(t, base)

	assertDoubledFacts(t, "REST", fetchDailyBrief(t, base))
	assertDoubledFacts(t, "MCP", callDailyBriefTool(t, base+"/mcp"))

	// run() takes no context: the documented shutdown path is SIGINT/SIGTERM
	// (serveAgent drains in-flight requests for up to 10s). Give the
	// composition root a beat past readiness so its signal.Notify — armed
	// immediately after the listener goroutine starts — is certain to be in
	// place, then deliver SIGTERM to this same process.
	time.Sleep(250 * time.Millisecond)
	termOnce.Do(func() {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatalf("deliver SIGTERM to the booted agent: %v", err)
		}
	})
	select {
	case <-returned:
	case <-time.After(15 * time.Second):
		t.Fatal("run() did not return after SIGTERM")
	}
	if runErr != nil {
		t.Fatalf("run() must drain cleanly on SIGTERM, got %v", runErr)
	}

	// With the process drained the doubles are quiescent: audit everything
	// they ever saw on the wire.
	assertReadOnlyWire(t, wes, fe, wfm, facility)
	assertCalledTools(t, wes, "get_backlog_telemetry")
	assertCalledTools(t, fe, "get_queue_status", "diagnose_stuck_tasks")
	assertCalledTools(t, wfm, "get_staffing_gap")
	assertCalledTools(t, facility, "list_sites")
}
