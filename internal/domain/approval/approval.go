// Package approval is the human-confirmation gate of warehouse-ops-agent's
// act slice (ADR 0027). A recommendation is not authorization to act
// (governance note): propose mints a short-lived, HMAC-signed confirmation
// token that binds EXACTLY what the human was shown (action, path, decision
// source, rationale); approve must present that token, so an execute can
// never run on anything but a recommendation this agent actually made.
//
// This is deliberately NOT caller authentication — auth stays removed
// fleet-wide (ADR 0006). It is an integrity + freshness binding between two
// explicit calls, which is what "human confirmation" needs. It keeps the
// agent stateless (no database): the token carries the proposal; the only
// state is the single-use nonce set held by the use case.
//
// The package depends on the standard library only (domain depends on
// nothing; TestHexagonalDependencyRules).
package approval

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Action is the closed set of act-enabled levers. Only release_next_work is
// act-enabled; assign_labor / revoke_reservation / complete_task stay
// recommendation-only (governance note) and are rejected at mint time.
type Action string

const ActionReleaseNextWork Action = "release_next_work"

// Sentinels. ErrExpired and ErrInvalidToken are distinct so callers can tell
// "ask the human again" from "this is not one of our tokens".
var (
	ErrInvalidToken      = errors.New("approval: invalid confirmation token")
	ErrExpired           = errors.New("approval: confirmation token expired")
	ErrUnsupportedAction = errors.New("approval: action is not act-enabled")
)

const (
	minKeyBytes = 32
	maxTTL      = time.Hour
	// maxTokenLen bounds untrusted input before any decoding work.
	maxTokenLen = 4096
)

// Proposal is what a confirmation token binds.
type Proposal struct {
	Action    Action
	PathId    string
	Source    string // decision source: deterministic | llm | fallback
	Rationale string
	Nonce     string
	ExpiresAt time.Time
}

// Signer mints and verifies confirmation tokens.
type Signer struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewSigner builds a Signer over key (>= 32 bytes) with a token lifetime ttl
// in (0, 1h]. A weak key or a silly TTL is a construction error, never a
// silent default.
func NewSigner(key []byte, ttl time.Duration, now func() time.Time) (*Signer, error) {
	if len(key) < minKeyBytes {
		return nil, fmt.Errorf("approval: signing key must be at least %d bytes, got %d", minKeyBytes, len(key))
	}
	if ttl <= 0 || ttl > maxTTL {
		return nil, fmt.Errorf("approval: token TTL must be in (0, %s], got %s", maxTTL, ttl)
	}
	if now == nil {
		now = time.Now
	}
	return &Signer{key: append([]byte(nil), key...), ttl: ttl, now: now}, nil
}

// NewEphemeralSigner builds a Signer over a fresh random key. Tokens then
// only verify in this process (a restart or a second replica invalidates
// outstanding ones, which just expire) — the safe default when no
// APPROVAL_SIGNING_KEY is configured and replicaCount is 1.
func NewEphemeralSigner(ttl time.Duration, now func() time.Time) (*Signer, error) {
	key := make([]byte, minKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("approval: generating ephemeral key: %w", err)
	}
	return NewSigner(key, ttl, now)
}

// Mint builds the proposal for (action, pathId) and returns its signed token
// together with the bound proposal (so the caller can show the human exactly
// what the token covers).
func (s *Signer) Mint(action Action, pathId, source, rationale string) (string, Proposal, error) {
	if action != ActionReleaseNextWork {
		return "", Proposal{}, fmt.Errorf("%w: %q", ErrUnsupportedAction, action)
	}
	if strings.TrimSpace(pathId) == "" {
		return "", Proposal{}, fmt.Errorf("%w: path id is required", ErrInvalidToken)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", Proposal{}, fmt.Errorf("approval: generating nonce: %w", err)
	}
	p := Proposal{
		Action:    action,
		PathId:    pathId,
		Source:    source,
		Rationale: rationale,
		Nonce:     hex.EncodeToString(nonce),
		// Whole seconds: the token carries unix seconds, and the proposal the
		// caller shows the human must say exactly what the token says.
		ExpiresAt: s.now().UTC().Add(s.ttl).Truncate(time.Second),
	}
	payload, err := encode(p)
	if err != nil {
		return "", Proposal{}, fmt.Errorf("approval: encoding proposal: %w", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	return enc + "." + s.sign(enc), p, nil
}

// Verify authenticates token (constant-time) and checks it has not expired.
// Expiry is half-open: a token is dead AT its ExpiresAt instant.
func (s *Signer) Verify(token string) (Proposal, error) {
	if token == "" || len(token) > maxTokenLen {
		return Proposal{}, ErrInvalidToken
	}
	enc, sig, ok := strings.Cut(token, ".")
	if !ok || enc == "" || sig == "" {
		return Proposal{}, ErrInvalidToken
	}
	if !hmac.Equal([]byte(sig), []byte(s.sign(enc))) {
		return Proposal{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return Proposal{}, ErrInvalidToken
	}
	p, err := decode(payload)
	if err != nil {
		return Proposal{}, ErrInvalidToken
	}
	if p.Action != ActionReleaseNextWork || strings.TrimSpace(p.PathId) == "" || p.Nonce == "" {
		return Proposal{}, ErrInvalidToken
	}
	if !s.now().UTC().Before(p.ExpiresAt) {
		return Proposal{}, ErrExpired
	}
	return p, nil
}

func (s *Signer) sign(enc string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(enc))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// wireFields is the arity of the positional payload:
// [action, pathId, source, rationale, nonce, expiresAtUnixSeconds].
// A positional array (rather than a tagged struct) keeps the wire shape out
// of the domain model — JSON field naming is an adapter concern — and makes
// decoding strict by construction: wrong arity or wrong element type is an
// invalid token, never a defaulted field.
const wireFields = 6

func encode(p Proposal) ([]byte, error) {
	return json.Marshal([]any{string(p.Action), p.PathId, p.Source, p.Rationale, p.Nonce, p.ExpiresAt.Unix()})
}

func decode(payload []byte) (Proposal, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var arr []any
	if err := dec.Decode(&arr); err != nil || len(arr) != wireFields {
		return Proposal{}, ErrInvalidToken
	}
	var str [5]string
	for i := range str {
		v, ok := arr[i].(string)
		if !ok {
			return Proposal{}, ErrInvalidToken
		}
		str[i] = v
	}
	num, ok := arr[5].(json.Number)
	if !ok {
		return Proposal{}, ErrInvalidToken
	}
	secs, err := num.Int64()
	if err != nil {
		return Proposal{}, ErrInvalidToken
	}
	return Proposal{
		Action:    Action(str[0]),
		PathId:    str[1],
		Source:    str[2],
		Rationale: str[3],
		Nonce:     str[4],
		ExpiresAt: time.Unix(secs, 0).UTC(),
	}, nil
}
