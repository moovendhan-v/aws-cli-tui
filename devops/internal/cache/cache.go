// Package cache keeps recently fetched AWS data per profile/region so screens
// render instantly and refreshes only hit AWS when data is stale or the user
// asks (r). Entries never cross credential contexts.
package cache

import (
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Value any
	At    time.Time
}

func (e Entry) Age() time.Duration { return time.Since(e.At) }

type Store struct {
	mu      sync.Mutex
	enabled bool
	ttl     func(kind string) time.Duration
	m       map[string]Entry
	hits    int
	misses  int
}

func New(enabled bool, ttl func(kind string) time.Duration) *Store {
	return &Store{enabled: enabled, ttl: ttl, m: map[string]Entry{}}
}

// Key identifies cached data: context first so invalidation by prefix works.
func Key(profile, region, kind string, parts ...string) string {
	return strings.Join(append([]string{profile, region, kind}, parts...), "\x1f")
}

func kindOf(key string) string {
	p := strings.SplitN(key, "\x1f", 4)
	if len(p) < 3 {
		return ""
	}
	return p[2]
}

// Get returns an entry and whether it is still fresh.
func (s *Store) Get(key string) (e Entry, ok, fresh bool) {
	if s == nil {
		return Entry{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return Entry{}, false, false
	}
	e, ok = s.m[key]
	if !ok {
		s.misses++
		return e, false, false
	}
	s.hits++
	return e, true, e.Age() < s.ttl(kindOf(key))
}

// SetEnabled turns caching on or off; turning it off drops all entries.
func (s *Store) SetEnabled(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = on
	if !on {
		s.m = map[string]Entry{}
	}
}

func (s *Store) Put(key string, v any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled {
		s.m[key] = Entry{Value: v, At: time.Now()}
	}
}

// InvalidateContext drops everything for a profile/region (after mutations).
func (s *Store) InvalidateContext(profile, region string) {
	if s == nil {
		return
	}
	prefix := profile + "\x1f" + region + "\x1f"
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			delete(s.m, k)
		}
	}
}

// Stats returns entry count and hit/miss counters.
func (s *Store) Stats() (entries, hits, misses int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m), s.hits, s.misses
}
