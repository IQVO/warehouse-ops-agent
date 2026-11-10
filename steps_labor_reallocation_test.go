package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
)

// Steps for the labor-reallocation act slice (ADR 0029).

const laborSigningKey = "0123456789abcdef0123456789abcdef"

// laborReallocation builds the act use case over the fakes the way cmd/agent
// wires it: the read outlook inside, the signer on the scenario clock.
func (w *world) laborReallocation() (*usecases.LaborReallocation, error) {
	outlook, err := usecases.NewLaborReallocationOutlook(w.laborStaffing)
	if err != nil {
		return nil, err
	}
	signer, err := approval.NewSigner([]byte(laborSigningKey), 5*time.Minute, func() time.Time { return w.now })
	if err != nil {
		return nil, err
	}
	return usecases.NewLaborReallocation(outlook, signer, w.laborPeer, nil, func() time.Time { return w.now })
}

// ---------------------------------------------------------------- Given ----

func (w *world) workforceReportsHeads(planned, active int, path string) error {
	w.laborStaffing.set(path, planned, active)
	return nil
}

func (w *world) theWorkforceAgentRejectsWithADomainRule() error {
	w.laborPeer.err = errLaborRule
	return nil
}

func (w *world) theWorkforceAgentDoesNotKnowTheAssociate() error {
	w.laborPeer.err = errLaborNotFound
	return nil
}

func (w *world) theWorkforceAgentIsUnreachable() error {
	w.laborPeer.err = errLaborDown
	return nil
}

// iHaveProposedMoving proposes one move and remembers its token and id.
func (w *world) iHaveProposedMoving(ctx context.Context, associate, from, to string) error {
	body := fmt.Sprintf(`{"siteCode":"WH1","shiftId":"S1","toPathId":%q,"candidates":[{"associateId":%q,"fromPathId":%q}]}`, to, associate, from)
	if err := w.postJSON(ctx, "/labor-reallocation/proposals", body); err != nil {
		return err
	}
	if w.status != http.StatusOK {
		return fmt.Errorf("the proposal was refused: %d %s", w.status, w.body)
	}
	return w.rememberFirstProposal()
}

func (w *world) rememberFirstProposal() error {
	var out struct {
		Proposals []struct {
			ProposalId string `json:"proposalId"`
			Token      string `json:"token"`
		} `json:"proposals"`
	}
	if err := json.Unmarshal(w.body, &out); err != nil || len(out.Proposals) == 0 {
		return fmt.Errorf("no proposal in %s (%w)", w.body, err)
	}
	w.firstProposalId, w.firstToken = out.Proposals[0].ProposalId, out.Proposals[0].Token
	return nil
}

// ----------------------------------------------------------------- When ----

func (w *world) postJSON(ctx context.Context, target, body string) error {
	return w.postBody(ctx, target, "application/json", body)
}

func (w *world) postBody(ctx context.Context, target, contentType, body string) error {
	if err := w.start(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.server.URL+target, bytes.NewBufferString(body))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if key := w.nextIdempotencyKey(); key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	started := time.Now()
	resp, err := w.server.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return err
	}
	w.response = response{status: resp.StatusCode, body: buf.Bytes(), headers: resp.Header, sent: req.Header.Clone(), elapsed: time.Since(started)}
	return nil
}

// nextIdempotencyKey is the key the next POST sends ("" = none).
func (w *world) nextIdempotencyKey() string {
	switch {
	case w.noIdempotencyKey:
		return ""
	case w.idempotencyKey != "":
		return w.idempotencyKey
	}
	w.keySeq++
	return fmt.Sprintf("bdd-key-%d", w.keySeq)
}

func (w *world) iUseTheIdempotencyKey(key string) error {
	w.idempotencyKey, w.noIdempotencyKey = key, false
	return nil
}

func (w *world) iSendNoIdempotencyKey() error {
	w.idempotencyKey, w.noIdempotencyKey = "", true
	return nil
}

// theResponseLinksItsSuccessorVersion checks the RFC 8288 Link a deprecated
// route answers with (ADR 0031).
func (w *world) theResponseLinksItsSuccessorVersion(target string) error {
	want := "<" + target + `>; rel="successor-version"`
	if got := w.headers.Get("Link"); got != want {
		return fmt.Errorf("Link = %q, want %q", got, want)
	}
	return nil
}

func (w *world) iPOSTJSON(ctx context.Context, target string, doc *godog.DocString) error {
	return w.postJSON(ctx, target, doc.Content)
}

func (w *world) iPOSTWithContentType(ctx context.Context, target, contentType, body string) error {
	return w.postBody(ctx, target, contentType, body)
}

func (w *world) timePasses(minutes int) error {
	w.now = w.now.Add(time.Duration(minutes) * time.Minute)
	return nil
}

func (w *world) approveBody(token, approvedBy string) string {
	raw, _ := json.Marshal(map[string]string{"token": token, "approvedBy": approvedBy})
	return string(raw)
}

func (w *world) iApproveTheFirstProposalAs(ctx context.Context, approvedBy string) error {
	return w.postJSON(ctx, "/labor-reallocation/proposals/"+w.firstProposalId+"/approvals", w.approveBody(w.firstToken, approvedBy))
}

// iApproveThroughTheDeprecatedRoute uses the verb route ADR 0031 replaced.
func (w *world) iApproveThroughTheDeprecatedRoute(ctx context.Context, approvedBy string) error {
	return w.postJSON(ctx, "/labor-reallocation/proposals/"+w.firstProposalId+"/approve", w.approveBody(w.firstToken, approvedBy))
}

func (w *world) iApproveWithAForgedToken(ctx context.Context, approvedBy string) error {
	return w.postJSON(ctx, "/labor-reallocation/proposals/"+w.firstProposalId+"/approvals", w.approveBody(w.firstToken+"x", approvedBy))
}

func (w *world) iApproveAnotherProposalWithTheFirstToken(ctx context.Context, proposalId, approvedBy string) error {
	return w.postJSON(ctx, "/labor-reallocation/proposals/"+proposalId+"/approvals", w.approveBody(w.firstToken, approvedBy))
}

// ----------------------------------------------------------------- Then ----

func (w *world) theResponseCarriesATokenAndAProposalId() error {
	if err := w.rememberFirstProposal(); err != nil {
		return err
	}
	if w.firstToken == "" || !strings.HasPrefix(w.firstProposalId, "lrp-") {
		return fmt.Errorf("token %q / proposal id %q", w.firstToken, w.firstProposalId)
	}
	return nil
}

func (w *world) theResponseCarriesNoToken() error {
	if strings.Contains(string(w.body), "token") && !strings.Contains(string(w.body), `"error"`) {
		return fmt.Errorf("a refused proposal leaked a token: %s", w.body)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.body, &doc); err != nil {
		return err
	}
	if _, has := doc["proposals"]; has {
		return fmt.Errorf("a refused proposal carries proposals: %s", w.body)
	}
	return nil
}

func (w *world) theWorkforceAgentReceived(n int) error {
	if got := len(w.laborPeer.calls); got != n {
		return fmt.Errorf("the workforce-management agent received %d assignment(s), want %d", got, n)
	}
	return nil
}

func (w *world) theWorkforceAgentWasAskedTo(associate, path, approvedBy, agentId string) error {
	if len(w.laborPeer.calls) == 0 {
		return fmt.Errorf("the workforce-management agent received nothing")
	}
	got := w.laborPeer.calls[len(w.laborPeer.calls)-1]
	if got.AssociateId != associate || got.PathId != path || got.ApprovedBy != approvedBy || got.AgentId != agentId || got.ProposalId != w.firstProposalId || w.firstProposalId == "" {
		return fmt.Errorf("the agent was asked %+v, want associate=%s path=%s approvedBy=%s agentId=%s proposalId=%s", got, associate, path, approvedBy, agentId, w.firstProposalId)
	}
	return nil
}

func registerLaborReallocationSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^workforce-management reports (\d+) planned and (\d+) active heads on path "([^"]*)"$`, w.workforceReportsHeads)
	sc.Step(`^the workforce-management agent rejects the assignment with a domain rule$`, w.theWorkforceAgentRejectsWithADomainRule)
	sc.Step(`^the workforce-management agent does not know the associate$`, w.theWorkforceAgentDoesNotKnowTheAssociate)
	sc.Step(`^the workforce-management agent is unreachable$`, w.theWorkforceAgentIsUnreachable)
	sc.Step(`^I have proposed moving "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.iHaveProposedMoving)

	sc.Step(`^I POST "([^"]*)" with JSON:$`, w.iPOSTJSON)
	sc.Step(`^I POST "([^"]*)" with content type "([^"]*)" and body "([^"]*)"$`, w.iPOSTWithContentType)
	sc.Step(`^(\d+) minutes pass$`, w.timePasses)
	sc.Step(`^I approve the first proposal as "([^"]*)"$`, w.iApproveTheFirstProposalAs)
	sc.Step(`^I approve the first proposal with a forged token as "([^"]*)"$`, w.iApproveWithAForgedToken)
	sc.Step(`^I approve the proposal "([^"]*)" with the first token as "([^"]*)"$`, w.iApproveAnotherProposalWithTheFirstToken)
	sc.Step(`^I approve the first proposal through the deprecated route as "([^"]*)"$`, w.iApproveThroughTheDeprecatedRoute)
	sc.Step(`^I use the Idempotency-Key "([^"]*)"$`, w.iUseTheIdempotencyKey)
	sc.Step(`^I send no Idempotency-Key$`, w.iSendNoIdempotencyKey)
	sc.Step(`^the response links its successor version "([^"]*)"$`, w.theResponseLinksItsSuccessorVersion)

	sc.Step(`^the response carries a token and a proposal id for the first proposal$`, w.theResponseCarriesATokenAndAProposalId)
	sc.Step(`^the response carries no token$`, w.theResponseCarriesNoToken)
	sc.Step(`^the workforce-management agent received (\d+) assignments?$`, w.theWorkforceAgentReceived)
	sc.Step(`^the workforce-management agent was asked to assign "([^"]*)" to "([^"]*)" approved by "([^"]*)" as agent "([^"]*)" under the first proposal$`, w.theWorkforceAgentWasAskedTo)
}
