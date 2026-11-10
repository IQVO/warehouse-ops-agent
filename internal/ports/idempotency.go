package ports

import (
	"context"
	"errors"
)

// ErrIdempotencyKeyReused means the Idempotency-Key was already used with a
// different request fingerprint (method + path + body hash). The REST edge
// answers it 409 idempotency-key-reused (fleet REST guideline 2.3, ADR 0030).
var ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different request")

// ErrResponseNotStored is returned by the run function of
// IdempotencyStore.Execute for an answer that must NOT be stored (a 5xx): the
// key is released and the retry re-executes.
var ErrResponseNotStored = errors.New("response is not stored")

// StoredResponse is the deterministic answer of a keyed request, kept so a
// retry replays it byte for byte.
type StoredResponse struct {
	Status int
	Header map[string][]string
	Body   []byte
}

// IdempotencyStore serialises the act slice's POST requests by
// Idempotency-Key (ADR 0030).
//
// Execute runs run at most once per key while the key is retained. A key seen
// before with the same fingerprint returns the stored response with
// replayed=true and does not call run; with a different fingerprint it
// returns ErrIdempotencyKeyReused. A concurrent call with the same key and
// fingerprint waits for the first one and replays its answer. If run returns
// an error (ErrResponseNotStored included) or panics, nothing is stored and
// the key is free again.
type IdempotencyStore interface {
	Execute(ctx context.Context, key, fingerprint string,
		run func(ctx context.Context) (StoredResponse, error)) (resp StoredResponse, replayed bool, err error)
}
