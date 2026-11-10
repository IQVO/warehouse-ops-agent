package memory_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/warehouse-ops-agent/internal/adapters/outbound/memory"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newStore(t *testing.T, ttl time.Duration, maxKeys int) (*memory.IdempotencyStore, *clock) {
	t.Helper()
	c := &clock{now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	s, err := memory.NewIdempotencyStore(ttl, maxKeys, c.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func answer(status int, body string) func(context.Context) (ports.StoredResponse, error) {
	return func(context.Context) (ports.StoredResponse, error) {
		return ports.StoredResponse{Status: status, Header: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(body)}, nil
	}
}

func TestNewIdempotencyStoreRejectsANonsenseConfig(t *testing.T) {
	now := time.Now
	for _, tc := range []struct {
		name string
		ttl  time.Duration
		max  int
		now  func() time.Time
	}{
		{"zero ttl", 0, 10, now},
		{"zero capacity", time.Hour, 0, now},
		{"nil clock", time.Hour, 10, nil},
	} {
		if _, err := memory.NewIdempotencyStore(tc.ttl, tc.max, tc.now); err == nil {
			t.Errorf("%s: want a construction error", tc.name)
		}
	}
}

func TestFreshKeyRunsOnceAndAReplayReturnsTheStoredAnswer(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	var runs atomic.Int32
	run := func(ctx context.Context) (ports.StoredResponse, error) {
		runs.Add(1)
		return answer(http.StatusOK, `{"n":1}`)(ctx)
	}
	first, replayed, err := s.Execute(context.Background(), "k1", "fp", run)
	if err != nil || replayed || first.Status != http.StatusOK || string(first.Body) != `{"n":1}` {
		t.Fatalf("first = %+v replayed=%v err=%v", first, replayed, err)
	}
	again, replayed, err := s.Execute(context.Background(), "k1", "fp", run)
	if err != nil || !replayed || again.Status != http.StatusOK || string(again.Body) != `{"n":1}` {
		t.Fatalf("replay = %+v replayed=%v err=%v", again, replayed, err)
	}
	if runs.Load() != 1 {
		t.Fatalf("run called %d times, want exactly 1", runs.Load())
	}
}

func TestSameKeyWithAnotherFingerprintIsReused(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	if _, _, err := s.Execute(context.Background(), "k1", "fp-a", answer(200, "{}")); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Execute(context.Background(), "k1", "fp-b", func(context.Context) (ports.StoredResponse, error) {
		t.Fatal("run must not be called for a reused key")
		return ports.StoredResponse{}, nil
	})
	if !errors.Is(err, ports.ErrIdempotencyKeyReused) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyReused", err)
	}
}

func TestAFailedRunStoresNothingAndTheRetryReExecutes(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	_, _, err := s.Execute(context.Background(), "k1", "fp", func(context.Context) (ports.StoredResponse, error) {
		return ports.StoredResponse{Status: 502}, ports.ErrResponseNotStored
	})
	if !errors.Is(err, ports.ErrResponseNotStored) {
		t.Fatalf("err = %v, want ErrResponseNotStored", err)
	}
	resp, replayed, err := s.Execute(context.Background(), "k1", "fp", answer(200, "ok"))
	if err != nil || replayed || resp.Status != 200 {
		t.Fatalf("retry = %+v replayed=%v err=%v, want a fresh execution", resp, replayed, err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

func TestAPanickingRunStoresNothing(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic must propagate to the caller")
			}
		}()
		_, _, _ = s.Execute(context.Background(), "k1", "fp", func(context.Context) (ports.StoredResponse, error) {
			panic("boom")
		})
	}()
	if s.Len() != 0 {
		t.Fatalf("Len = %d after a panic, want 0", s.Len())
	}
	if _, replayed, err := s.Execute(context.Background(), "k1", "fp", answer(200, "ok")); err != nil || replayed {
		t.Fatalf("after a panic the key must execute fresh, got replayed=%v err=%v", replayed, err)
	}
}

func TestAKeyIsForgottenAfterItsRetention(t *testing.T) {
	s, c := newStore(t, 24*time.Hour, 10)
	if _, _, err := s.Execute(context.Background(), "k1", "fp", answer(200, "first")); err != nil {
		t.Fatal(err)
	}
	c.advance(24*time.Hour - time.Second)
	if _, replayed, _ := s.Execute(context.Background(), "k1", "fp", answer(200, "second")); !replayed {
		t.Fatal("a key inside its retention must replay")
	}
	c.advance(2 * time.Second)
	resp, replayed, err := s.Execute(context.Background(), "k1", "fp-other", answer(200, "second"))
	if err != nil || replayed || string(resp.Body) != "second" {
		t.Fatalf("an expired key must execute fresh (even with another body), got %q replayed=%v err=%v", resp.Body, replayed, err)
	}
}

func TestAFullStoreEvictsTheOldestAnswer(t *testing.T) {
	s, c := newStore(t, time.Hour, 2)
	for _, k := range []string{"k1", "k2", "k3"} {
		if _, _, err := s.Execute(context.Background(), k, "fp", answer(200, k)); err != nil {
			t.Fatal(err)
		}
		c.advance(time.Second)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want the capacity 2", s.Len())
	}
	if _, replayed, _ := s.Execute(context.Background(), "k3", "fp", answer(200, "x")); !replayed {
		t.Fatal("the newest key must still replay")
	}
	if _, replayed, _ := s.Execute(context.Background(), "k1", "fp", answer(200, "x")); replayed {
		t.Fatal("the oldest key must have been evicted")
	}
}

func TestConcurrentSameKeyRunsOnceAndEveryCallerGetsTheSameAnswer(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	var runs atomic.Int32
	release := make(chan struct{})
	run := func(context.Context) (ports.StoredResponse, error) {
		runs.Add(1)
		<-release
		return ports.StoredResponse{Status: 200, Body: []byte("once")}, nil
	}
	const n = 16
	var wg sync.WaitGroup
	results := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _, err := s.Execute(context.Background(), "k1", "fp", run)
			if err != nil {
				results[i] = "err: " + err.Error()
				return
			}
			results[i] = string(resp.Body)
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("run called %d times, want exactly 1", runs.Load())
	}
	for i, r := range results {
		if r != "once" {
			t.Fatalf("caller %d got %q, want the first answer", i, r)
		}
	}
}

func TestAWaiterTakesOverWhenTheFirstRunIsNotStored(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _, _ = s.Execute(context.Background(), "k1", "fp", func(context.Context) (ports.StoredResponse, error) {
			close(started)
			<-release
			return ports.StoredResponse{Status: 503}, ports.ErrResponseNotStored
		})
	}()
	<-started
	done := make(chan ports.StoredResponse, 1)
	go func() {
		resp, _, _ := s.Execute(context.Background(), "k1", "fp", answer(200, "second"))
		done <- resp
	}()
	time.Sleep(10 * time.Millisecond)
	close(release)
	if got := <-done; got.Status != 200 || string(got.Body) != "second" {
		t.Fatalf("the waiter got %+v, want its own fresh execution", got)
	}
}

func TestAWaiterGivesUpWithItsContext(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, _, _ = s.Execute(context.Background(), "k1", "fp", func(context.Context) (ports.StoredResponse, error) {
			close(started)
			<-release
			return ports.StoredResponse{Status: 200}, nil
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := s.Execute(ctx, "k1", "fp", answer(200, "x")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the waiter's context error", err)
	}
}

func TestAnInFlightKeyWithAnotherFingerprintIsReusedAtOnce(t *testing.T) {
	s, _ := newStore(t, time.Hour, 10)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, _, _ = s.Execute(context.Background(), "k1", "fp-a", func(context.Context) (ports.StoredResponse, error) {
			close(started)
			<-release
			return ports.StoredResponse{Status: 200}, nil
		})
	}()
	<-started
	if _, _, err := s.Execute(context.Background(), "k1", "fp-b", answer(200, "x")); !errors.Is(err, ports.ErrIdempotencyKeyReused) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyReused without waiting", err)
	}
}
