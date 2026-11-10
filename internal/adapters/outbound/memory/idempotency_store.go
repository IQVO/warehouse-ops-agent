// Package memory holds this agent's only in-process store: the act slice's
// Idempotency-Key answers (ADR 0030). The agent has no database (ADR 0001),
// so the key store lives where ADR 0027 already keeps the confirmation
// token's used-nonce set: in the single process, bounded in time and size.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// entry is one key: in flight until done is closed, then either stored (a
// replayable answer) or already removed from the map (nothing stored).
type entry struct {
	fingerprint string
	done        chan struct{}
	stored      bool
	resp        ports.StoredResponse
	storedAt    time.Time
}

// IdempotencyStore is the process-local ports.IdempotencyStore. Requests are
// serialised per key, not globally: two different keys run concurrently, two
// requests with one key run once.
type IdempotencyStore struct {
	ttl     time.Duration
	maxKeys int
	now     func() time.Time

	mu   sync.Mutex
	keys map[string]*entry
}

// NewIdempotencyStore keeps every stored answer for ttl, and at most maxKeys
// answers: past that, the oldest stored answer is evicted first.
func NewIdempotencyStore(ttl time.Duration, maxKeys int, now func() time.Time) (*IdempotencyStore, error) {
	switch {
	case ttl <= 0:
		return nil, errors.New("memory: idempotency retention must be positive")
	case maxKeys <= 0:
		return nil, errors.New("memory: idempotency capacity must be positive")
	case now == nil:
		return nil, errors.New("memory: idempotency store needs a clock")
	}
	return &IdempotencyStore{ttl: ttl, maxKeys: maxKeys, now: now, keys: map[string]*entry{}}, nil
}

// Execute implements ports.IdempotencyStore.
func (s *IdempotencyStore) Execute(ctx context.Context, key, fingerprint string,
	run func(ctx context.Context) (ports.StoredResponse, error),
) (ports.StoredResponse, bool, error) {
	for {
		e, fresh := s.claim(key, fingerprint)
		if fresh {
			return s.runFresh(ctx, key, e, run)
		}
		if e.fingerprint != fingerprint {
			return ports.StoredResponse{}, false, ports.ErrIdempotencyKeyReused
		}
		select {
		case <-e.done:
		case <-ctx.Done():
			return ports.StoredResponse{}, false, ctx.Err()
		}
		if e.stored {
			return e.resp, true, nil
		}
		// The first run stored nothing (a 5xx or a panic): the key is free
		// again, so this caller competes to execute it itself.
	}
}

// claim returns the live entry for key, or registers a new in-flight one
// (fresh=true) that the caller must run.
func (s *IdempotencyStore) claim(key, fingerprint string) (*entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if e, ok := s.keys[key]; ok {
		if !e.stored || now.Sub(e.storedAt) < s.ttl {
			return e, false
		}
		delete(s.keys, key)
	}
	s.makeRoom(now)
	e := &entry{fingerprint: fingerprint, done: make(chan struct{})}
	s.keys[key] = e
	return e, true
}

// makeRoom drops expired answers, then the oldest stored answers until a new
// key fits. In-flight keys are never evicted. Caller holds s.mu.
func (s *IdempotencyStore) makeRoom(now time.Time) {
	for k, e := range s.keys {
		if e.stored && now.Sub(e.storedAt) >= s.ttl {
			delete(s.keys, k)
		}
	}
	for len(s.keys) >= s.maxKeys {
		oldest := ""
		for k, e := range s.keys {
			if e.stored && (oldest == "" || e.storedAt.Before(s.keys[oldest].storedAt)) {
				oldest = k
			}
		}
		if oldest == "" {
			return
		}
		delete(s.keys, oldest)
	}
}

// runFresh runs the request for a newly claimed key and stores its answer; an
// error or a panic releases the key instead.
func (s *IdempotencyStore) runFresh(ctx context.Context, key string, e *entry,
	run func(ctx context.Context) (ports.StoredResponse, error),
) (resp ports.StoredResponse, replayed bool, err error) {
	stored := false
	defer func() {
		if !stored {
			s.mu.Lock()
			if s.keys[key] == e {
				delete(s.keys, key)
			}
			s.mu.Unlock()
		}
		close(e.done)
	}()
	resp, err = run(ctx)
	if err != nil {
		return ports.StoredResponse{}, false, err
	}
	s.mu.Lock()
	e.resp, e.stored, e.storedAt = resp, true, s.now()
	s.mu.Unlock()
	stored = true
	return resp, false, nil
}

// Len is the number of keys held, in flight or stored (for tests).
func (s *IdempotencyStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}
