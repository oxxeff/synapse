// Package dedup suppresses repeated webhook deliveries.
//
// Gitea retries a failed delivery with the same delivery identifier; a duplicate
// must be acknowledged without triggering work twice. The store keeps seen
// identifiers in memory with a TTL window. State is ephemeral by design: on
// restart the window resets, and the residual risk of reprocessing one delivery
// is covered by idempotency at the executor.
package dedup

import (
	"context"
	"sync"
	"time"
)

// Store is an in-memory set of recently seen identifiers with a TTL window. It
// is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Duration
	// now is the time source, swapped in tests to drive expiry deterministically.
	now func() time.Time
}

// New returns a Store whose entries expire after window.
func New(window time.Duration) *Store {
	return &Store{
		seen:   make(map[string]time.Time),
		window: window,
		now:    time.Now,
	}
}

// Seen records id and reports whether it was already present within the window.
// An empty id cannot be deduplicated: it is never reported as a duplicate and is
// not recorded.
func (s *Store) Seen(id string) bool {
	if id == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if at, ok := s.seen[id]; ok && now.Sub(at) < s.window {
		return true
	}

	s.seen[id] = now

	return false
}

// Run periodically evicts expired entries until ctx is cancelled. It is meant to
// run in its own goroutine for the service lifetime; cancelling ctx stops it.
func (s *Store) Run(ctx context.Context) {
	// A non-positive window has no sensible eviction interval; Validate rejects it
	// in real configs, so here just block until shutdown rather than panic.
	if s.window <= 0 {
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(s.window)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evictExpired()
		}
	}
}

func (s *Store) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	for id, at := range s.seen {
		if now.Sub(at) >= s.window {
			delete(s.seen, id)
		}
	}
}
