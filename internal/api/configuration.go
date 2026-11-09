package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func configurationETag(w http.ResponseWriter, revision int64) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(revision, 10)))
}
func configurationStore(w http.ResponseWriter, r *http.Request, store any) (registry.VersionedConfigurationStore, int64, bool) {
	versioned, ok := store.(registry.VersionedConfigurationStore)
	if !ok {
		Error(w, r, 503, "CONFIGURATION_CAS_UNAVAILABLE", "revisioned configuration storage is required")
		return nil, 0, false
	}
	generation, err := versioned.ConfigurationGeneration(r.Context())
	if err != nil {
		Error(w, r, 503, "CONFIGURATION_UNAVAILABLE", "configuration generation is unavailable")
		return nil, 0, false
	}
	return versioned, generation, true
}

// A missing precondition is create-only, never permission to replace an object.
// Existing objects require an exact strong revision; wildcard/weak/list tags fail.
func configurationExpected(w http.ResponseWriter, r *http.Request, current int64) (int64, bool) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		if current == 0 {
			return 0, true
		}
		Error(w, r, 428, "PRECONDITION_REQUIRED", "read the current revision and supply If-Match")
		return 0, false
	}
	if len(values) != 1 {
		Error(w, r, 400, "INVALID_PRECONDITION", "one exact configuration revision is required")
		return 0, false
	}
	raw := values[0]
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		raw = raw[1 : len(raw)-1]
	}
	rev, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || rev < 0 || strconv.FormatInt(rev, 10) != raw {
		Error(w, r, 400, "INVALID_PRECONDITION", "one exact configuration revision is required")
		return 0, false
	}
	if rev != current {
		Error(w, r, 412, "REVISION_CONFLICT", "configuration changed; reload before retrying")
		return 0, false
	}
	return rev, true
}
func configurationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, registry.ErrConfigurationConflict) {
		Error(w, r, 412, "REVISION_CONFLICT", "configuration or a validated dependency changed; reload before retrying")
		return
	}
	if errors.Is(err, registry.ErrNotFound) {
		Error(w, r, 404, "NOT_FOUND", "configuration not found")
		return
	}
	Error(w, r, 503, "CONFIGURATION_UNAVAILABLE", "configuration mutation could not be acknowledged")
}
func putConfiguration(w http.ResponseWriter, r *http.Request, store registry.VersionedConfigurationStore, kind, id string, value any, expected, generation int64) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		configurationError(w, r, err)
		return false
	}
	record, err := store.PutConfiguration(r.Context(), registry.ConfigurationRecord{Kind: kind, ID: id, Spec: raw}, expected, generation)
	if err != nil {
		configurationError(w, r, err)
		return false
	}
	if err = json.Unmarshal(record.Spec, value); err != nil {
		configurationError(w, r, err)
		return false
	}
	configurationETag(w, record.Revision)
	return true
}
