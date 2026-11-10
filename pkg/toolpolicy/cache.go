package toolpolicy

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
)

var (
	cacheMu sync.Mutex
	cache   = map[[32]byte]*Set{}
)

const maxCached = 256

// CompileCached is Compile with a bounded cache keyed by the rules' content,
// so a hot connection does not recompile its rules on every call. Only
// successful compilations are cached. The returned Set is safe for concurrent
// use.
func CompileCached(rules []Rule) (*Set, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	key := sha256.Sum256(encoded)
	cacheMu.Lock()
	if set, ok := cache[key]; ok {
		cacheMu.Unlock()
		return set, nil
	}
	cacheMu.Unlock()
	set, err := Compile(rules)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	if len(cache) >= maxCached {
		clear(cache)
	}
	cache[key] = set
	cacheMu.Unlock()
	return set, nil
}
