package membership

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type cacheEntry struct {
	snapshot Snapshot
	until    time.Time
	used     uint64
}
type cacheFlight struct {
	done       chan struct{}
	snapshot   Snapshot
	err        error
	generation uint64
}

// Cache coalesces reads without extending authority freshness. Its TTL is a
// revocation-latency bound, not a synthetic directory-observation timestamp.
// Directory event consumers can Invalidate with a minimum observation time;
// in-flight results acquired before that invalidation cannot repopulate it.
type Cache struct {
	source     Authority
	ttl        time.Duration
	capacity   int
	digest     string
	mu         sync.Mutex
	entries    map[string]cacheEntry
	flights    map[string]*cacheFlight
	generation map[string]uint64
	notBefore  map[string]time.Time
	sequence   uint64
}

func NewCache(source Authority, ttl time.Duration, capacity int) (*Cache, error) {
	if !ValidAuthority(source) || ttl < 0 || ttl > 5*time.Minute || capacity < 1 || capacity > 100000 {
		return nil, fmt.Errorf("bounded membership cache configuration required")
	}
	return &Cache{source: source, ttl: ttl, capacity: capacity, digest: Digest(struct {
		Source   string
		TTL      time.Duration
		Capacity int
	}{source.ConfigurationDigest(), ttl, capacity}), entries: map[string]cacheEntry{}, flights: map[string]*cacheFlight{}, generation: map[string]uint64{}, notBefore: map[string]time.Time{}}, nil
}
func (c *Cache) ConfigurationDigest() string { return c.digest }
func (c *Cache) Invalidate(q Query, notBefore time.Time) error {
	if q.Validate() != nil || notBefore.IsZero() {
		return ErrInvalidEvidence
	}
	key := Digest(q)
	c.mu.Lock()
	defer c.mu.Unlock()
	// Revocation watermarks are retained. Bound their storage as strictly as
	// normal entries instead of forgetting a revoked subject under pressure.
	if _, ok := c.notBefore[key]; !ok && len(c.notBefore) >= c.capacity {
		return ErrUnavailable
	}
	if notBefore.After(c.notBefore[key]) {
		c.notBefore[key] = notBefore
	}
	c.generation[key]++
	delete(c.entries, key)
	return nil
}
func (c *Cache) Resolve(ctx context.Context, q Query, now time.Time) (Snapshot, error) {
	if q.Validate() != nil || now.IsZero() {
		return Snapshot{}, ErrInvalidEvidence
	}
	key := Digest(q)
	for {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		c.mu.Lock()
		if e, ok := c.entries[key]; ok && now.Before(e.until) && now.Before(e.snapshot.ExpiresAt) && !e.snapshot.IssuedAt.Before(c.notBefore[key]) {
			c.sequence++
			e.used = c.sequence
			c.entries[key] = e
			c.mu.Unlock()
			return Clone(e.snapshot), nil
		}
		if flight := c.flights[key]; flight != nil {
			c.mu.Unlock()
			select {
			case <-flight.done:
				c.mu.Lock()
				err := flight.err
				if c.generation[key] != flight.generation || flight.snapshot.IssuedAt.Before(c.notBefore[key]) {
					err = ErrUnavailable
				}
				snapshot := Clone(flight.snapshot)
				c.mu.Unlock()
				if err != nil {
					return Snapshot{}, err
				}
				return snapshot, nil
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			}
		}
		if len(c.flights) >= c.capacity {
			c.mu.Unlock()
			return Snapshot{}, ErrUnavailable
		}
		generation := c.generation[key]
		flight := &cacheFlight{done: make(chan struct{}), generation: generation}
		c.flights[key] = flight
		c.mu.Unlock()
		snapshot, err := c.source.Resolve(ctx, q, now)
		c.mu.Lock()
		if c.generation[key] != generation || snapshot.IssuedAt.Before(c.notBefore[key]) {
			err = ErrUnavailable
		}
		if err == nil {
			if len(c.entries) >= c.capacity {
				var oldest string
				var age uint64
				for k, e := range c.entries {
					if oldest == "" || e.used < age {
						oldest, age = k, e.used
					}
				}
				delete(c.entries, oldest)
			}
			until := now.Add(c.ttl)
			if snapshot.ExpiresAt.Before(until) {
				until = snapshot.ExpiresAt
			}
			c.sequence++
			c.entries[key] = cacheEntry{Clone(snapshot), until, c.sequence}
		}
		delete(c.flights, key)
		flight.snapshot, flight.err = Clone(snapshot), err
		close(flight.done)
		c.mu.Unlock()
		if err != nil {
			return Snapshot{}, err
		}
		return Clone(snapshot), nil
	}
}
