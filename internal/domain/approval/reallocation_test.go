package approval_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/domain/approval"
)

func TestReallocationRoundTripBindsTheExactMove(t *testing.T) {
	s := newSigner(t, key32, t0)
	tok, r, err := s.MintReallocation("assoc-7", "pick", "pack")
	if err != nil {
		t.Fatalf("MintReallocation: %v", err)
	}
	if !r.ExpiresAt.Equal(t0.Add(5*time.Minute)) || r.Nonce == "" {
		t.Fatalf("reallocation = %+v", r)
	}
	got, err := s.VerifyReallocation(tok)
	if err != nil {
		t.Fatalf("VerifyReallocation: %v", err)
	}
	if got.AssociateId != "assoc-7" || got.FromPathId != "pick" || got.ToPathId != "pack" || got.Nonce != r.Nonce || !got.ExpiresAt.Equal(r.ExpiresAt) {
		t.Fatalf("verified = %+v, minted = %+v", got, r)
	}
	if got.ProposalId() != "lrp-"+r.Nonce || !strings.HasPrefix(got.ProposalId(), approval.ProposalIdPrefix) {
		t.Fatalf("proposal id = %q", got.ProposalId())
	}
}

func TestReallocationWithoutStatedOriginRoundTrips(t *testing.T) {
	s := newSigner(t, key32, t0)
	tok, _, err := s.MintReallocation("assoc-7", "", "pack")
	if err != nil {
		t.Fatalf("MintReallocation: %v", err)
	}
	got, err := s.VerifyReallocation(tok)
	if err != nil || got.FromPathId != "" {
		t.Fatalf("verified = %+v, err = %v", got, err)
	}
}

func TestReallocationNoncesAreUnique(t *testing.T) {
	s := newSigner(t, key32, t0)
	_, a, _ := s.MintReallocation("a", "", "p")
	_, b, _ := s.MintReallocation("a", "", "p")
	if a.Nonce == b.Nonce || a.ProposalId() == b.ProposalId() {
		t.Fatal("two mints produced the same nonce / proposal id")
	}
}

func TestMintReallocationRequiresAssociateAndTarget(t *testing.T) {
	s := newSigner(t, key32, t0)
	for name, args := range map[string][3]string{
		"empty associate":      {"", "pick", "pack"},
		"blank associate":      {"  ", "pick", "pack"},
		"empty target":         {"a", "pick", ""},
		"blank target":         {"a", "pick", "\t"},
		"both required, empty": {"", "", ""},
	} {
		if _, _, err := s.MintReallocation(args[0], args[1], args[2]); !errors.Is(err, approval.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestVerifyReallocationRejectsTamperedPayloadAndWrongKey(t *testing.T) {
	s := newSigner(t, key32, t0)
	tok, _, _ := s.MintReallocation("assoc-7", "pick", "pack")
	_, sig, _ := strings.Cut(tok, ".")
	other, _, _ := s.MintReallocation("assoc-8", "pick", "pack")
	otherPayload, _, _ := strings.Cut(other, ".")
	if _, err := s.VerifyReallocation(otherPayload + "." + sig); !errors.Is(err, approval.ErrInvalidToken) {
		t.Fatalf("swapped payload err = %v, want ErrInvalidToken", err)
	}
	foreign := newSigner(t, "ffffffffffffffffffffffffffffffff", t0)
	if _, err := foreign.VerifyReallocation(tok); !errors.Is(err, approval.ErrInvalidToken) {
		t.Fatalf("wrong key err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyReallocationExpiryIsHalfOpen(t *testing.T) {
	tok, _, _ := newSigner(t, key32, t0).MintReallocation("a", "pick", "pack")
	if _, err := newSigner(t, key32, t0.Add(5*time.Minute-time.Second)).VerifyReallocation(tok); err != nil {
		t.Fatalf("one second before expiry err = %v, want ok", err)
	}
	if _, err := newSigner(t, key32, t0.Add(5*time.Minute)).VerifyReallocation(tok); !errors.Is(err, approval.ErrExpired) {
		t.Fatalf("at the expiry instant err = %v, want ErrExpired", err)
	}
}

func TestVerifyReallocationRejectsMalformed(t *testing.T) {
	s := newSigner(t, key32, t0)
	for _, tok := range []string{"", "nodot", ".", "a.", ".b", "!!!.???", strings.Repeat("x", 5000)} {
		if _, err := s.VerifyReallocation(tok); !errors.Is(err, approval.ErrInvalidToken) {
			t.Errorf("token %.12q: err = %v, want ErrInvalidToken", tok, err)
		}
	}
}

// A release token and a reallocation token are different kinds under the
// same key: neither can be redeemed as the other.
func TestTokenKindsAreNotInterchangeable(t *testing.T) {
	s := newSigner(t, key32, t0)
	release, _, _ := s.Mint(approval.ActionReleaseNextWork, "pick", "deterministic", "r")
	realloc, _, _ := s.MintReallocation("assoc-7", "pick", "pack")
	if _, err := s.VerifyReallocation(release); !errors.Is(err, approval.ErrInvalidToken) {
		t.Errorf("release token redeemed as reallocation: err = %v, want ErrInvalidToken", err)
	}
	if _, err := s.Verify(realloc); !errors.Is(err, approval.ErrInvalidToken) {
		t.Errorf("reallocation token redeemed as release: err = %v, want ErrInvalidToken", err)
	}
}
