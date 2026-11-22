package usecases_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/application/usecases"
	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// actForged is peer error text that tries to start a forged log line.
const actForged = "path pick-zone-a\r\nlevel=INFO msg=forged"

// actLineRecorder keeps the string form of every attribute value of every record.
type actLineRecorder struct{ values *[]string }

func (h actLineRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h actLineRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h actLineRecorder) WithGroup(string) slog.Handler            { return h }
func (h actLineRecorder) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		*h.values = append(*h.values, a.Value.String())
		return true
	})
	return nil
}

func actAssertOneLine(t *testing.T, values []string, what string) {
	t.Helper()
	if len(values) == 0 {
		t.Fatalf("%s logged nothing", what)
	}
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n") {
			t.Fatalf("%s: logged attribute %q holds a line break", what, v)
		}
	}
}

// The act slices log a peer's error on the approval request path; its text can
// carry request data, so every record must stay on one line.
func TestLogHygiene_ActPeerFailuresAreLoggedOnOneLine(t *testing.T) {
	ctx := context.Background()
	t.Run("release_next_work delegation", func(t *testing.T) {
		var values []string
		prev := slog.Default()
		slog.SetDefault(slog.New(actLineRecorder{values: &values}))
		t.Cleanup(func() { slog.SetDefault(prev) })
		f := newActFixture(t, releaseDecision)
		f.rel.err = errors.New(actForged)
		p, err := f.uc.Propose(ctx, "b1", "s1", "pick-zone-a")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.uc.Approve(ctx, p.Token)
		actAssertOneLine(t, values, "a failed release delegation")
	})
	t.Run("exception context lookup", func(t *testing.T) {
		var values []string
		prev := slog.Default()
		slog.SetDefault(slog.New(actLineRecorder{values: &values}))
		t.Cleanup(func() { slog.SetDefault(prev) })
		f, _ := newExceptionFixture(t, releaseDecision, &fakeExceptions{err: errors.New(actForged)}, time.Second)
		if _, err := f.uc.Propose(ctx, "b1", "s1", "pick-zone-a"); err != nil {
			t.Fatal(err)
		}
		actAssertOneLine(t, values, "a failed exception context lookup")
	})
	t.Run("assign_labor delegation", func(t *testing.T) {
		var values []string
		now := actNow
		clock := func() time.Time { return now }
		signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
		if err != nil {
			t.Fatal(err)
		}
		f := &reallocFixture{in: okStaffing(), asg: &fakeAssigner{err: errors.New(actForged)}, log: &strings.Builder{}, now: &now}
		f.uc, err = usecases.NewLaborReallocation(newOutlook(t, f.in), signer, f.asg, slog.New(actLineRecorder{values: &values}), clock)
		if err != nil {
			t.Fatal(err)
		}
		p := proposeOne(t, f)
		values = values[:0]
		if _, err := f.uc.Approve(ctx, p.ProposalId, p.Token, "sup"); err == nil {
			t.Fatal("a failed delegation must surface its error")
		}
		actAssertOneLine(t, values, "a failed assign_labor delegation")
	})
}

// actForgedToken seals a reallocation token with the fixtures' signing key
// whose nonce (and so the proposal id) carries a line break: the wire format
// is base64url(JSON [action, associate, from, to, nonce, exp]) "." HMAC.
func actForgedToken(t *testing.T, nonce string) string {
	t.Helper()
	payload, err := json.Marshal([]any{string(approval.ActionReallocateLabor), "a1", "pick", "pack", nonce, actNow.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	m := hmac.New(sha256.New, []byte("0123456789abcdef0123456789abcdef"))
	m.Write([]byte(enc))
	return enc + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// The proposal id is read back from the approval request's token; every act
// record that names it must keep it on one line (executed, replay refused,
// failed delegation).
func TestLogHygiene_ReallocationProposalIdIsLoggedOnOneLine(t *testing.T) {
	ctx := context.Background()
	nonce := "n1" + "\r\nlevel=INFO msg=forged"
	for name, assignErr := range map[string]error{"executed then replayed": nil, "failed delegation": errors.New("peer down")} {
		t.Run(name, func(t *testing.T) {
			var values []string
			now := actNow
			clock := func() time.Time { return now }
			signer, err := approval.NewSigner([]byte("0123456789abcdef0123456789abcdef"), 5*time.Minute, clock)
			if err != nil {
				t.Fatal(err)
			}
			asg := &fakeAssigner{out: ports.LaborAssignment{AssociateId: "a1", PathId: "pack", AssignmentId: "a1@pack"}, err: assignErr}
			uc, err := usecases.NewLaborReallocation(newOutlook(t, okStaffing()), signer, asg, slog.New(actLineRecorder{values: &values}), clock)
			if err != nil {
				t.Fatal(err)
			}
			token := actForgedToken(t, nonce)
			_, _ = uc.Approve(ctx, approval.ProposalIdPrefix+nonce, token, "sup")
			_, _ = uc.Approve(ctx, approval.ProposalIdPrefix+nonce, token, "sup")
			actAssertOneLine(t, values, name)
		})
	}
}
