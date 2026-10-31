package approval_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
)

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

const key32 = "0123456789abcdef0123456789abcdef"

func newSigner(t *testing.T, key string, now time.Time) *approval.Signer {
	t.Helper()
	s, err := approval.NewSigner([]byte(key), 5*time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestMintVerifyRoundTrip(t *testing.T) {
	s := newSigner(t, key32, t0)
	tok, p, err := s.Mint(approval.ActionReleaseNextWork, "pick-zone-a", "deterministic", "backlog 12 > wip 3")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !p.ExpiresAt.Equal(t0.Add(5*time.Minute)) || p.Nonce == "" {
		t.Fatalf("proposal = %+v", p)
	}
	got, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Action != approval.ActionReleaseNextWork || got.PathId != "pick-zone-a" || got.Source != "deterministic" || got.Nonce != p.Nonce {
		t.Fatalf("verified = %+v", got)
	}
}

func TestMintedNoncesAreUnique(t *testing.T) {
	s := newSigner(t, key32, t0)
	_, a, _ := s.Mint(approval.ActionReleaseNextWork, "p", "deterministic", "r")
	_, b, _ := s.Mint(approval.ActionReleaseNextWork, "p", "deterministic", "r")
	if a.Nonce == b.Nonce {
		t.Fatal("two mints produced the same nonce")
	}
}

func TestVerifyRejectsPayloadSwappedUnderOriginalSignature(t *testing.T) {
	s := newSigner(t, key32, t0)
	tok, _, _ := s.Mint(approval.ActionReleaseNextWork, "pick-zone-a", "deterministic", "r")
	_, sig, _ := strings.Cut(tok, ".")
	forged, _, _ := s.Mint(approval.ActionReleaseNextWork, "pack-zone-b", "deterministic", "r")
	forgedPayload, _, _ := strings.Cut(forged, ".")
	if _, err := s.Verify(forgedPayload + "." + sig); !errors.Is(err, approval.ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	tok, _, _ := newSigner(t, key32, t0).Mint(approval.ActionReleaseNextWork, "p", "deterministic", "r")
	other := newSigner(t, "ffffffffffffffffffffffffffffffff", t0)
	if _, err := other.Verify(tok); !errors.Is(err, approval.ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyExpiryIsHalfOpen(t *testing.T) {
	tok, _, _ := newSigner(t, key32, t0).Mint(approval.ActionReleaseNextWork, "p", "deterministic", "r")
	justBefore := newSigner(t, key32, t0.Add(5*time.Minute-time.Second))
	if _, err := justBefore.Verify(tok); err != nil {
		t.Fatalf("one second before expiry err = %v, want ok", err)
	}
	atBoundary := newSigner(t, key32, t0.Add(5*time.Minute))
	if _, err := atBoundary.Verify(tok); !errors.Is(err, approval.ErrExpired) {
		t.Fatalf("at the expiry instant err = %v, want ErrExpired", err)
	}
	later := newSigner(t, key32, t0.Add(time.Hour))
	if _, err := later.Verify(tok); !errors.Is(err, approval.ErrExpired) {
		t.Fatalf("after expiry err = %v, want ErrExpired", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	s := newSigner(t, key32, t0)
	for _, bad := range []string{"", "nodot", ".", "a.b", "!!!.???", strings.Repeat("x", 9000)} {
		if _, err := s.Verify(bad); !errors.Is(err, approval.ErrInvalidToken) {
			t.Errorf("Verify(%.20q) err = %v, want ErrInvalidToken", bad, err)
		}
	}
}

func TestMintRejectsActionsThatAreNotActEnabled(t *testing.T) {
	s := newSigner(t, key32, t0)
	for _, a := range []string{"assign_labor", "revoke_reservation", "complete_task", "hold", ""} {
		if _, _, err := s.Mint(approval.Action(a), "p", "deterministic", "r"); !errors.Is(err, approval.ErrUnsupportedAction) {
			t.Errorf("Mint(%q) err = %v, want ErrUnsupportedAction (only release_next_work is act-enabled)", a, err)
		}
	}
}

func TestMintRequiresPath(t *testing.T) {
	s := newSigner(t, key32, t0)
	if _, _, err := s.Mint(approval.ActionReleaseNextWork, "  ", "deterministic", "r"); !errors.Is(err, approval.ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken for a blank path", err)
	}
}

func TestNewSignerRejectsShortKeyAndBadTTL(t *testing.T) {
	if _, err := approval.NewSigner([]byte("short"), time.Minute, time.Now); err == nil {
		t.Error("a key under 32 bytes must be rejected")
	}
	if _, err := approval.NewSigner([]byte(key32), 0, time.Now); err == nil {
		t.Error("a non-positive TTL must be rejected")
	}
	if _, err := approval.NewSigner([]byte(key32), 25*time.Hour, time.Now); err == nil {
		t.Error("a TTL over the 1h ceiling must be rejected")
	}
}

func TestEphemeralSignerRoundTrips(t *testing.T) {
	s, err := approval.NewEphemeralSigner(time.Minute, time.Now)
	if err != nil {
		t.Fatalf("NewEphemeralSigner: %v", err)
	}
	tok, _, err := s.Mint(approval.ActionReleaseNextWork, "p", "deterministic", "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(tok); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	other, _ := approval.NewEphemeralSigner(time.Minute, time.Now)
	if _, err := other.Verify(tok); !errors.Is(err, approval.ErrInvalidToken) {
		t.Fatalf("a token from another ephemeral key must not verify, err = %v", err)
	}
}
