package memplex

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// LocalBackend is an in-memory KV + naive cosine-similarity vector backend.
// Used for tests, local dev, and the `aiplex up --local` quickstart.
// Not durable; all data lost on process restart.
type LocalBackend struct {
	mu          sync.RWMutex
	store       map[string]map[string]*Value // namespace.URI → key → value
	subscribers map[string][]chan Change     // namespace.URI → list of channels
}

// NewLocalBackend creates a fresh in-memory backend.
func NewLocalBackend() *LocalBackend {
	return &LocalBackend{
		store:       make(map[string]map[string]*Value),
		subscribers: make(map[string][]chan Change),
	}
}

func (l *LocalBackend) Name() string { return "local" }

func (l *LocalBackend) namespace(uri string) map[string]*Value {
	bucket, ok := l.store[uri]
	if !ok {
		bucket = make(map[string]*Value)
		l.store[uri] = bucket
	}
	return bucket
}

func (l *LocalBackend) Read(_ context.Context, ns Namespace, key string) (*Value, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	bucket, ok := l.store[ns.URI.String()]
	if !ok {
		return nil, ErrNotFound
	}
	v, ok := bucket[key]
	if !ok {
		return nil, ErrNotFound
	}
	if v.TTL > 0 && time.Since(v.UpdatedAt) > v.TTL {
		return nil, ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (l *LocalBackend) Write(_ context.Context, ns Namespace, key string, val Value, opts WriteOpts) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.namespace(ns.URI.String())
	if opts.IfNoneMatch {
		if _, exists := bucket[key]; exists {
			return ErrConflict
		}
	}

	now := time.Now()
	if val.CreatedAt.IsZero() {
		if existing, ok := bucket[key]; ok {
			val.CreatedAt = existing.CreatedAt
		} else {
			val.CreatedAt = now
		}
	}
	val.UpdatedAt = now
	if opts.TTL > 0 {
		val.TTL = opts.TTL
	}
	if opts.Embedding != nil {
		val.Embedding = opts.Embedding
	}
	stored := val
	bucket[key] = &stored

	l.broadcast(ns.URI.String(), key, Change{Op: "put", Key: key, Value: &stored, At: now})
	return nil
}

func (l *LocalBackend) Delete(_ context.Context, ns Namespace, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket, ok := l.store[ns.URI.String()]
	if !ok {
		return ErrNotFound
	}
	if _, ok := bucket[key]; !ok {
		return ErrNotFound
	}
	delete(bucket, key)

	l.broadcast(ns.URI.String(), key, Change{Op: "delete", Key: key, At: time.Now()})
	return nil
}

func (l *LocalBackend) Search(_ context.Context, ns Namespace, q Query) ([]Hit, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	bucket, ok := l.store[ns.URI.String()]
	if !ok {
		return nil, nil
	}

	hits := make([]Hit, 0, len(bucket))
	for k, v := range bucket {
		if v.TTL > 0 && time.Since(v.UpdatedAt) > v.TTL {
			continue
		}
		score := float32(0)
		if len(q.Embedding) > 0 && len(v.Embedding) == len(q.Embedding) {
			score = cosineSim(q.Embedding, v.Embedding)
		} else if q.Text != "" && containsTextMatch(v.Data, q.Text) {
			score = 1.0
		} else if len(q.Embedding) == 0 && q.Text == "" {
			// no query; treat as listing
			score = 1.0
		} else {
			continue
		}
		hits = append(hits, Hit{Key: k, Value: *v, Score: score})
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })

	topK := q.TopK
	if topK <= 0 || topK > len(hits) {
		topK = len(hits)
	}
	return hits[:topK], nil
}

func (l *LocalBackend) List(_ context.Context, ns Namespace, prefix string, page Page) (Listing, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	bucket, ok := l.store[ns.URI.String()]
	if !ok {
		return Listing{}, nil
	}

	keys := make([]string, 0, len(bucket))
	for k := range bucket {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	start := 0
	if page.Cursor != "" {
		for i, k := range keys {
			if k > page.Cursor {
				start = i
				break
			}
			start = i + 1
		}
	}
	limit := page.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	end := start + limit
	if end > len(keys) {
		end = len(keys)
	}
	out := Listing{Keys: keys[start:end]}
	if end < len(keys) {
		out.NextPage = keys[end-1]
	}
	return out, nil
}

func (l *LocalBackend) Subscribe(ctx context.Context, ns Namespace, prefix string) (<-chan Change, error) {
	ch := make(chan Change, 16)
	l.mu.Lock()
	l.subscribers[ns.URI.String()] = append(l.subscribers[ns.URI.String()], ch)
	l.mu.Unlock()

	go func() {
		<-ctx.Done()
		l.mu.Lock()
		defer l.mu.Unlock()
		subs := l.subscribers[ns.URI.String()]
		out := subs[:0]
		for _, c := range subs {
			if c == ch {
				close(c)
				continue
			}
			out = append(out, c)
		}
		l.subscribers[ns.URI.String()] = out
	}()

	// Wrap with prefix filter.
	if prefix == "" {
		return ch, nil
	}
	filtered := make(chan Change, 16)
	go func() {
		defer close(filtered)
		for c := range ch {
			if strings.HasPrefix(c.Key, prefix) {
				filtered <- c
			}
		}
	}()
	return filtered, nil
}

func (l *LocalBackend) broadcast(uri, _ string, change Change) {
	for _, ch := range l.subscribers[uri] {
		select {
		case ch <- change:
		default:
			// slow subscriber — drop
		}
	}
}

// cosineSim returns a cosine similarity in [0, 1] for non-negative vectors,
// or [-1, 1] otherwise. Used by Search.
func cosineSim(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// containsTextMatch is a naive substring search over the value's stringified
// data. Production backends would use FTS / inverted index.
func containsTextMatch(data map[string]any, text string) bool {
	q := strings.ToLower(text)
	for _, v := range data {
		if s, ok := v.(string); ok {
			if strings.Contains(strings.ToLower(s), q) {
				return true
			}
		}
	}
	return false
}
