package dedup

import (
	"sync"
	"testing"
	"time"
)

func TestSeenDuplicate(t *testing.T) {
	t.Parallel()

	s := New(time.Minute)

	if s.Seen("d1") {
		t.Fatal("first Seen(d1) = true, want false")
	}
	if !s.Seen("d1") {
		t.Fatal("second Seen(d1) = false, want true")
	}
	if s.Seen("d2") {
		t.Fatal("Seen(d2) = true, want false for a distinct id")
	}
}

func TestSeenEmptyID(t *testing.T) {
	t.Parallel()

	s := New(time.Minute)

	if s.Seen("") {
		t.Fatal("first Seen(\"\") = true, want false")
	}
	if s.Seen("") {
		t.Fatal("repeat Seen(\"\") = true; an empty id must never be a duplicate")
	}
}

func TestSeenExpiry(t *testing.T) {
	t.Parallel()

	s := New(time.Minute)
	cur := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return cur }

	if s.Seen("d1") {
		t.Fatal("first Seen = true, want false")
	}

	// Within the window the id is still a duplicate.
	cur = cur.Add(30 * time.Second)
	if !s.Seen("d1") {
		t.Fatal("Seen within window = false, want true")
	}

	// Past the window the id is treated as new again.
	cur = cur.Add(2 * time.Minute)
	if s.Seen("d1") {
		t.Fatal("Seen after window = true, want false")
	}
}

func TestEvictExpired(t *testing.T) {
	t.Parallel()

	s := New(time.Minute)
	cur := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return cur }

	s.Seen("old")
	cur = cur.Add(90 * time.Second)
	s.Seen("fresh")

	s.evictExpired()

	if _, ok := s.seen["old"]; ok {
		t.Error("expired entry was not evicted")
	}
	if _, ok := s.seen["fresh"]; !ok {
		t.Error("live entry was evicted")
	}
}

func TestSeenConcurrent(t *testing.T) {
	t.Parallel()

	s := New(time.Minute)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Seen("shared")
			s.Seen("u")
		}()
	}
	wg.Wait()
}
