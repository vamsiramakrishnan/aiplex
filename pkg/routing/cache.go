package routing

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// CacheEntry is one stored response: the provider's successful, complete,
// non-streamed answer exactly as it came from the provider (before response
// hooks, which run again on every hit), and the usage observed for it.
type CacheEntry struct {
	Status int
	Header http.Header
	Body   []byte
	// Counters is the provider-normalized usage of the original exchange.
	Counters     map[string]int64
	ModelVersion string
	// Backend that produced the answer.
	SurfaceRef  string
	NativeModel string
	// Scope fields, for purging.
	TenantID    string
	AccessPoint string
	Model       string
	StoredAt    time.Time
	Expires     time.Time
}

func (e CacheEntry) size() int64 {
	n := int64(len(e.Body)) + 256
	for k, vs := range e.Header {
		n += int64(len(k))
		for _, v := range vs {
			n += int64(len(v))
		}
	}
	return n + int64(len(e.Counters))*32
}

func (e CacheEntry) clone() CacheEntry {
	e.Header = e.Header.Clone()
	e.Body = append([]byte(nil), e.Body...)
	counters := make(map[string]int64, len(e.Counters))
	for k, v := range e.Counters {
		counters[k] = v
	}
	e.Counters = counters
	return e
}

// CacheStore keeps responses by key. Implementations must be safe for
// concurrent use and must never return an expired entry.
type CacheStore interface {
	Get(key string, now time.Time) (CacheEntry, bool)
	Put(key string, entry CacheEntry)
	// Purge removes the entries of a tenant, optionally narrowed to one
	// access point and one model, and returns how many were removed.
	Purge(tenant, accessPoint, model string) int
	Stats() CacheStats
}

// CacheStats describes a store's occupancy.
type CacheStats struct {
	Entries  int   `json:"entries"`
	Bytes    int64 `json:"bytes"`
	MaxBytes int64 `json:"max_bytes"`
}

// MemoryCache is an in-memory LRU bounded by total bytes. It is per process:
// replicas do not share it and a restart empties it.
type MemoryCache struct {
	mu       sync.Mutex
	maxBytes int64
	bytes    int64
	order    *list.List // front = most recently used
	items    map[string]*list.Element
}

type memoryItem struct {
	key   string
	entry CacheEntry
	size  int64
}

// DefaultCacheBytes is the default process-wide cache size.
const DefaultCacheBytes = 64 << 20

// NewMemoryCache returns an LRU bounded to maxBytes (DefaultCacheBytes when 0).
func NewMemoryCache(maxBytes int64) *MemoryCache {
	if maxBytes <= 0 {
		maxBytes = DefaultCacheBytes
	}
	return &MemoryCache{maxBytes: maxBytes, order: list.New(), items: map[string]*list.Element{}}
}

func (c *MemoryCache) remove(el *list.Element) {
	item := el.Value.(*memoryItem)
	c.order.Remove(el)
	delete(c.items, item.key)
	c.bytes -= item.size
}

// Get returns a copy of a live entry and marks it recently used.
func (c *MemoryCache) Get(key string, now time.Time) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el := c.items[key]
	if el == nil {
		return CacheEntry{}, false
	}
	item := el.Value.(*memoryItem)
	if !now.Before(item.entry.Expires) {
		c.remove(el)
		return CacheEntry{}, false
	}
	c.order.MoveToFront(el)
	return item.entry.clone(), true
}

// Put stores a copy, evicting least recently used entries to stay in bound.
// An entry larger than the whole store is not kept.
func (c *MemoryCache) Put(key string, entry CacheEntry) {
	entry = entry.clone()
	size := entry.size() + int64(len(key))
	c.mu.Lock()
	defer c.mu.Unlock()
	if el := c.items[key]; el != nil {
		c.remove(el)
	}
	if size > c.maxBytes {
		return
	}
	for c.bytes+size > c.maxBytes && c.order.Len() > 0 {
		c.remove(c.order.Back())
	}
	c.items[key] = c.order.PushFront(&memoryItem{key: key, entry: entry, size: size})
	c.bytes += size
}

// Purge removes matching entries.
func (c *MemoryCache) Purge(tenant, accessPoint, model string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for el := c.order.Front(); el != nil; {
		next := el.Next()
		e := el.Value.(*memoryItem).entry
		if e.TenantID == tenant && (accessPoint == "" || e.AccessPoint == accessPoint) && (model == "" || e.Model == model) {
			c.remove(el)
			n++
		}
		el = next
	}
	return n
}

// Stats reports the occupancy.
func (c *MemoryCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CacheStats{Entries: c.order.Len(), Bytes: c.bytes, MaxBytes: c.maxBytes}
}

// CacheKeyParts are the inputs of a cache key. Principal is empty for the
// access_point scope.
type CacheKeyParts struct {
	TenantID    string
	AccessPoint string
	Model       string // the access point's (backend-independent) model name
	Wire        string // the client wire protocol
	Path        string // the client path below /ap/{id}, with the client model
	Scope       string
	Principal   string
	Body        []byte // canonical body (CanonicalJSON)
	Headers     []string
}

// CacheKey hashes the parts with length prefixes, so no two different part
// lists produce the same input.
func CacheKey(p CacheKeyParts) string {
	h := sha256.New()
	write := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	for _, s := range []string{"aiplex.cache.v1", p.TenantID, p.AccessPoint, p.Model, p.Wire, p.Path, p.Scope, p.Principal} {
		write([]byte(s))
	}
	write(p.Body)
	for _, s := range p.Headers {
		write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CanonicalJSON re-encodes a JSON object with sorted keys, no insignificant
// whitespace and numbers spelled as sent, and with the top-level "model"
// (when present) replaced by model, so the key does not depend on which
// backend's native name the body carries. The body must already be
// unambiguous: the gateway refuses duplicate keys before this runs.
func CanonicalJSON(body []byte, model string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data")
	}
	object, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	if _, present := object["model"]; present {
		object["model"] = model
	}
	// encoding/json sorts map keys and keeps json.Number spelling.
	return json.Marshal(object)
}
