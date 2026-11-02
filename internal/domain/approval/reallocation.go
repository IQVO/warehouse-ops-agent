package approval

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ActionReallocateLabor is the second act-enabled lever (ADR 0029, act slice
// #2): move ONE associate onto a path, delegated to workforce-management's
// assign_labor A2A skill. It has its own token kind: a release token is not
// redeemable here and a reallocation token is not redeemable as a release.
const ActionReallocateLabor Action = "reallocate_labor"

// ProposalIdPrefix starts every labor-reallocation proposal id.
const ProposalIdPrefix = "lrp-"

// Reallocation is what a labor-reallocation confirmation token binds: the
// EXACT move the human was shown. FromPathId is empty when the proposal
// stated no current path for the associate.
type Reallocation struct {
	AssociateId string
	FromPathId  string
	ToPathId    string
	Nonce       string
	ExpiresAt   time.Time
}

// ProposalId is the stable id of this proposal. It is derived from the
// single-use nonce, so the id in the approve URL, the token and the
// attribution workforce-management records are one and the same value.
func (r Reallocation) ProposalId() string { return ProposalIdPrefix + r.Nonce }

// MintReallocation builds the proposal for one move and returns its signed
// token with the bound proposal. associateId and toPathId are required.
func (s *Signer) MintReallocation(associateId, fromPathId, toPathId string) (string, Reallocation, error) {
	if strings.TrimSpace(associateId) == "" || strings.TrimSpace(toPathId) == "" {
		return "", Reallocation{}, fmt.Errorf("%w: associate id and target path id are required", ErrInvalidToken)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", Reallocation{}, fmt.Errorf("approval: generating nonce: %w", err)
	}
	r := Reallocation{
		AssociateId: associateId,
		FromPathId:  fromPathId,
		ToPathId:    toPathId,
		Nonce:       hex.EncodeToString(nonce),
		// Whole seconds, as for release tokens: the proposal shown to the
		// human must say exactly what the token says.
		ExpiresAt: s.now().UTC().Add(s.ttl).Truncate(time.Second),
	}
	token, err := s.seal([wireStrings]string{string(ActionReallocateLabor), r.AssociateId, r.FromPathId, r.ToPathId, r.Nonce}, r.ExpiresAt)
	if err != nil {
		return "", Reallocation{}, err
	}
	return token, r, nil
}

// VerifyReallocation authenticates a labor-reallocation token (constant-time)
// and checks it has not expired (half-open, like Verify). A forged, tampered,
// foreign-kind or malformed token is ErrInvalidToken; a genuine one past its
// expiry is ErrExpired.
func (s *Signer) VerifyReallocation(token string) (Reallocation, error) {
	f, exp, err := s.open(token)
	if err != nil {
		return Reallocation{}, err
	}
	r := Reallocation{AssociateId: f[1], FromPathId: f[2], ToPathId: f[3], Nonce: f[4], ExpiresAt: exp}
	if Action(f[0]) != ActionReallocateLabor || strings.TrimSpace(r.AssociateId) == "" || strings.TrimSpace(r.ToPathId) == "" || r.Nonce == "" {
		return Reallocation{}, ErrInvalidToken
	}
	if err := s.live(r.ExpiresAt); err != nil {
		return Reallocation{}, err
	}
	return r, nil
}
