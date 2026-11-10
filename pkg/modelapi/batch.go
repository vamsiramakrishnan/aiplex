package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Provider batch jobs.
//
// A batch is admitted once, at create, and its usage arrives later, per item,
// in a results file or stream that may be partial, is in arbitrary order, and
// may be fetched more than once. The batch-level usage object some providers
// return (OpenAI Batch "usage") reports the same tokens as the per-item lines
// and is used only as a cross-check, never added.
//
// The gateway keeps one durable BatchRecord per batch it created (BatchStore,
// revisioned). Settlement happens exactly once, when the record first becomes
// complete:
//
//  1. Each results part (Anthropic results stream, OpenAI output file, OpenAI
//     error file) is aggregated only from a fully received, cleanly ended,
//     fully parsed body; a cut or malformed body leaves the record unchanged.
//  2. A record is complete when every item the provider reported is accounted
//     for by provider evidence: the per-item lines (succeeded, errored,
//     cancelled, expired) cover the expected item count, with no duplicate
//     custom_id. Only succeeded items carry usage; the others are released
//     because the results say so, never because a count is missing.
//  3. On completion the gateway calls BatchSettler.SettleBatch with the
//     per-item sums. Quota settlement is idempotent per attempt (the event id
//     is derived from the attempt), so repeating it re-acknowledges the same
//     evidence.
//  4. The gateway then compare-and-swaps the record to settled. Only the
//     exchange that wins the swap calls BatchSettler.RecordBatch, which puts
//     the usage on that exchange's ledger record. A second fetch finds the
//     record settled and settles nothing.
//
// A batch whose results are never fetched keeps its admitted exposure (its
// attempt stays unknown with reason batch_results_pending) and appears in the
// existing aging/unknown reporting.

// BatchRecord is the durable gateway state of one provider batch.
type BatchRecord struct {
	Owner           Owner  `json:"owner"`
	Provider        string `json:"provider"`
	BatchID         string `json:"batch_id"`
	CreateOperation string `json:"create_operation"`
	Model           string `json:"model,omitempty"`
	AttemptID       string `json:"attempt_id,omitempty"`
	// Expected is the provider's item count once known (inline create count or
	// a terminal batch object's request counts); -1 when unknown.
	Expected int64 `json:"expected"`
	// Ended is true once the provider reported a terminal batch status.
	Ended        bool   `json:"ended,omitempty"`
	Status       string `json:"status,omitempty"`
	OutputFileID string `json:"output_file_id,omitempty"`
	ErrorFileID  string `json:"error_file_id,omitempty"`
	// Parts holds the aggregate of each fully received results part.
	Parts map[string]BatchPart `json:"parts,omitempty"`
	// CrossCheck is the batch-object usage total, when the provider reports
	// one. It is compared with the per-item sum and never added to it.
	CrossCheck map[string]int64 `json:"cross_check,omitempty"`
	// Settled and Usage record the one settlement.
	// AliasOf/AliasPart make this record a pointer from a results file id
	// (OpenAI output or error file) to its batch.
	AliasOf            string           `json:"alias_of,omitempty"`
	AliasPart          string           `json:"alias_part,omitempty"`
	Settled            bool             `json:"settled,omitempty"`
	Usage              map[string]int64 `json:"usage,omitempty"`
	CrossCheckMismatch bool             `json:"cross_check_mismatch,omitempty"`
}

// BatchPart aggregates one complete results body.
type BatchPart struct {
	Lines     int64            `json:"lines"`
	Succeeded int64            `json:"succeeded"`
	Failed    int64            `json:"failed"`
	Usage     map[string]int64 `json:"usage,omitempty"`
	// Unknown lists counters reported on some but not all succeeded lines:
	// their sums are unknown, not partial.
	Unknown []string `json:"unknown,omitempty"`
	Models  []string `json:"models,omitempty"`
	IDs     []string `json:"-"`
}

// BatchSettlement is what a complete batch settles.
type BatchSettlement struct {
	Provider  string
	BatchID   string
	AttemptID string
	Model     string
	Items     int64
	Succeeded int64
	// Counters are normalized per-item sums over succeeded items only.
	Counters map[string]int64
}

// ErrBatchConflict is returned by BatchStore.SaveBatch when the revision moved.
var ErrBatchConflict = errors.New("batch record revision conflict")

// ErrBatchNotFound is returned by BatchStore.LoadBatch for an unknown batch.
var ErrBatchNotFound = errors.New("batch record not found")

// BatchStore durably keeps BatchRecords with compare-and-swap revisions. A
// revision of 0 creates. Records are scoped by the owner's tenant.
type BatchStore interface {
	LoadBatch(ctx context.Context, owner Owner, provider, batchID string) (BatchRecord, uint64, error)
	SaveBatch(ctx context.Context, record BatchRecord, revision uint64) (uint64, error)
}

// BatchSettler settles and records a complete batch. SettleBatch must be
// idempotent; RecordBatch is called at most once per batch, by the exchange
// that won the settlement swap.
type BatchSettler interface {
	SettleBatch(ctx context.Context, owner Owner, settlement BatchSettlement) error
	RecordBatch(ctx context.Context, owner Owner, settlement BatchSettlement)
}

// MemoryBatchStore is an in-process BatchStore for tests and single replicas.
type MemoryBatchStore struct {
	mu      sync.Mutex
	records map[string]memoryBatch
}

type memoryBatch struct {
	raw      []byte
	revision uint64
}

func NewMemoryBatchStore() *MemoryBatchStore {
	return &MemoryBatchStore{records: map[string]memoryBatch{}}
}

func (m *MemoryBatchStore) lock()   { m.mu.Lock() }
func (m *MemoryBatchStore) unlock() { m.mu.Unlock() }

func memoryBatchKey(owner Owner, provider, id string) string {
	raw, _ := json.Marshal([]string{owner.TenantID, owner.SurfaceID, provider, id})
	return string(raw)
}

func (m *MemoryBatchStore) LoadBatch(_ context.Context, owner Owner, provider, id string) (BatchRecord, uint64, error) {
	m.lock()
	defer m.unlock()
	value, ok := m.records[memoryBatchKey(owner, provider, id)]
	if !ok {
		return BatchRecord{}, 0, ErrBatchNotFound
	}
	var record BatchRecord
	if err := json.Unmarshal(value.raw, &record); err != nil {
		return BatchRecord{}, 0, err
	}
	return record, value.revision, nil
}

func (m *MemoryBatchStore) SaveBatch(_ context.Context, record BatchRecord, revision uint64) (uint64, error) {
	m.lock()
	defer m.unlock()
	key := memoryBatchKey(record.Owner, record.Provider, record.BatchID)
	current, ok := m.records[key]
	if (!ok && revision != 0) || (ok && current.revision != revision) {
		return 0, ErrBatchConflict
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return 0, err
	}
	m.records[key] = memoryBatch{raw: raw, revision: revision + 1}
	return revision + 1, nil
}

// ---- per-item usage ----

// batchLine is one parsed results line.
type batchLine struct {
	id        string
	succeeded bool
	counters  map[string]int64
	model     string
}

// nativeBodyCounters normalizes the usage of one endpoint response body found
// inside a batch line, with the same vocabulary as the synchronous observers:
// input_tokens is total input (Anthropic's exclusive input is summed with its
// cache counters and kept as uncached_input_tokens); output_tokens includes
// reasoning for OpenAI and Anthropic; Gemini thoughts stay in thought_tokens.
func nativeBodyCounters(provider string, body json.RawMessage) (map[string]int64, string, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil, "", fmt.Errorf("invalid batch item body")
	}
	model := ""
	for _, key := range []string{"model", "modelVersion"} {
		if raw := root[key]; raw != nil {
			_ = json.Unmarshal(raw, &model)
			if model != "" {
				break
			}
		}
	}
	var usage map[string]json.RawMessage
	for _, key := range []string{"usage", "usageMetadata"} {
		if raw := root[key]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if json.Unmarshal(raw, &usage) != nil {
				return nil, "", fmt.Errorf("invalid batch item usage")
			}
		}
	}
	counters := map[string]int64{}
	if usage == nil {
		return counters, model, nil
	}
	read := func(target string, names ...string) error {
		for _, name := range names {
			n, known, err := nonNegativeCounter(usage[name])
			if err != nil {
				return err
			}
			if known {
				if prior, ok := counters[target]; ok && prior != n {
					return fmt.Errorf("conflicting batch item usage")
				}
				counters[target] = n
			}
		}
		return nil
	}
	for target, names := range map[string][]string{
		"input_tokens":       {"input_tokens", "prompt_tokens", "promptTokenCount"},
		"output_tokens":      {"output_tokens", "completion_tokens", "candidatesTokenCount"},
		"total_tokens":       {"total_tokens", "totalTokenCount"},
		"cached_tokens":      {"cache_read_input_tokens", "cachedContentTokenCount"},
		"cache_write_tokens": {"cache_creation_input_tokens"},
		"thought_tokens":     {"thoughtsTokenCount"},
		"tool_use_tokens":    {"toolUsePromptTokenCount"},
	} {
		if err := read(target, names...); err != nil {
			return nil, "", err
		}
	}
	for parent, fields := range map[string][2]string{"input_tokens_details": {"cached_tokens", "cached_tokens"}, "prompt_tokens_details": {"cached_tokens", "cached_tokens"}, "output_tokens_details": {"reasoning_tokens", "thought_tokens"}, "completion_tokens_details": {"reasoning_tokens", "thought_tokens"}} {
		var detail map[string]json.RawMessage
		if raw := usage[parent]; raw != nil && json.Unmarshal(raw, &detail) == nil {
			names := []string{fields[0]}
			if fields[1] == "thought_tokens" {
				names = append(names, "thinking_tokens")
			}
			for _, name := range names {
				n, known, err := nonNegativeCounter(detail[name])
				if err != nil {
					return nil, "", err
				}
				if known {
					counters[fields[1]] = n
				}
			}
			if n, known, err := nonNegativeCounter(detail["cache_write_tokens"]); err == nil && known {
				counters["cache_write_tokens"] = n
			}
		}
	}
	if _, scalar := counters["cache_write_tokens"]; !scalar && provider == ProviderAnthropic {
		var buckets map[string]json.RawMessage
		if raw := usage["cache_creation"]; raw != nil && json.Unmarshal(raw, &buckets) == nil && len(buckets) > 0 {
			var sum int64
			for _, value := range buckets {
				n, _, err := nonNegativeCounter(value)
				if err != nil {
					return nil, "", err
				}
				sum += n
			}
			counters["cache_write_tokens"] = sum
		}
	}
	if modality := map[string]int64{}; modalityCounters(usage, modality) == nil {
		for key, n := range modality {
			counters[key] = n
		}
	}
	if provider == ProviderAnthropic {
		if input, ok := counters["input_tokens"]; ok {
			counters["uncached_input_tokens"] = input
			counters["input_tokens"] = input + counters["cached_tokens"] + counters["cache_write_tokens"]
		}
	}
	return counters, model, nil
}

// parseBatchLines parses a complete JSONL results body. Any malformed line,
// unknown result shape or duplicate id rejects the whole part (nothing is
// aggregated from it).
func parseBatchLines(provider, part string, data []byte) ([]batchLine, error) {
	var lines []batchLine
	seen := map[string]bool{}
	for _, raw := range bytes.Split(data, []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		line, err := parseBatchLine(provider, part, raw)
		if err != nil {
			return nil, err
		}
		if seen[line.id] {
			return nil, fmt.Errorf("duplicate batch item %q", line.id)
		}
		seen[line.id] = true
		lines = append(lines, line)
	}
	return lines, nil
}

func parseBatchLine(provider, part string, raw []byte) (batchLine, error) {
	if validateJSON(raw) != nil {
		return batchLine{}, fmt.Errorf("invalid batch results line")
	}
	switch provider {
	case ProviderAnthropic:
		// https://platform.claude.com/docs/en/api/messages/batches/results
		var line struct {
			CustomID string `json:"custom_id"`
			Result   struct {
				Type    string          `json:"type"`
				Message json.RawMessage `json:"message"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &line) != nil || line.CustomID == "" {
			return batchLine{}, fmt.Errorf("invalid Message Batches result")
		}
		switch line.Result.Type {
		case "succeeded":
			counters, model, err := nativeBodyCounters(provider, line.Result.Message)
			if err != nil {
				return batchLine{}, err
			}
			return batchLine{id: line.CustomID, succeeded: true, counters: counters, model: model}, nil
		case "errored", "canceled", "expired":
			return batchLine{id: line.CustomID}, nil
		}
		return batchLine{}, fmt.Errorf("unknown Message Batches result type")
	case ProviderOpenAI:
		// https://developers.openai.com/api/docs/guides/batch (output and
		// error file line format)
		var line struct {
			CustomID string `json:"custom_id"`
			Response *struct {
				StatusCode int             `json:"status_code"`
				Body       json.RawMessage `json:"body"`
			} `json:"response"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(raw, &line) != nil || line.CustomID == "" {
			return batchLine{}, fmt.Errorf("invalid batch output line")
		}
		if line.Response == nil || line.Response.StatusCode != 200 {
			return batchLine{id: line.CustomID}, nil
		}
		if part != "output" {
			return batchLine{}, fmt.Errorf("successful line in an error file")
		}
		counters, model, err := nativeBodyCounters(provider, line.Response.Body)
		if err != nil {
			return batchLine{}, err
		}
		return batchLine{id: line.CustomID, succeeded: true, counters: counters, model: model}, nil
	}
	return batchLine{}, fmt.Errorf("unsupported batch provider")
}

// aggregateBatchLines sums per-counter usage over succeeded lines. A counter
// known on some succeeded lines but not others has an unknown sum.
func aggregateBatchLines(lines []batchLine) BatchPart {
	part := BatchPart{Usage: map[string]int64{}}
	present := map[string]int64{}
	overflow := map[string]bool{}
	models := map[string]bool{}
	for _, line := range lines {
		part.Lines++
		part.IDs = append(part.IDs, line.id)
		if !line.succeeded {
			part.Failed++
			continue
		}
		part.Succeeded++
		if line.model != "" {
			models[line.model] = true
		}
		for key, n := range line.counters {
			present[key]++
			if part.Usage[key] > math.MaxInt64-n {
				overflow[key] = true
				continue
			}
			part.Usage[key] += n
		}
	}
	for key, count := range present {
		if count != part.Succeeded || overflow[key] {
			delete(part.Usage, key)
			part.Unknown = append(part.Unknown, key)
		}
	}
	for model := range models {
		part.Models = append(part.Models, model)
	}
	sort.Strings(part.Unknown)
	sort.Strings(part.Models)
	return part
}

// batchSettlement decides whether a record is complete and, if so, returns
// what it settles. It never treats a missing part or count as zero.
func batchSettlement(record BatchRecord) (BatchSettlement, bool, error) {
	if record.Settled {
		return BatchSettlement{}, false, nil
	}
	var required []string
	switch record.Provider {
	case ProviderAnthropic:
		required = []string{"results"}
	case ProviderOpenAI:
		if !record.Ended {
			return BatchSettlement{}, false, nil
		}
		if record.OutputFileID != "" {
			required = append(required, "output")
		}
		if record.ErrorFileID != "" {
			required = append(required, "error")
		}
	default:
		return BatchSettlement{}, false, fmt.Errorf("unsupported batch provider")
	}
	if record.Expected < 0 {
		return BatchSettlement{}, false, nil
	}
	settlement := BatchSettlement{Provider: record.Provider, BatchID: record.BatchID, AttemptID: record.AttemptID, Counters: map[string]int64{}}
	present := map[string]int{}
	unknown := map[string]bool{}
	models := map[string]bool{}
	for _, name := range required {
		part, ok := record.Parts[name]
		if !ok {
			return BatchSettlement{}, false, nil
		}
		settlement.Items += part.Lines
		settlement.Succeeded += part.Succeeded
		for _, key := range part.Unknown {
			unknown[key] = true
		}
		for _, model := range part.Models {
			models[model] = true
		}
		if part.Succeeded == 0 {
			continue
		}
		for key, n := range part.Usage {
			present[key]++
			if settlement.Counters[key] > math.MaxInt64-n {
				unknown[key] = true
				continue
			}
			settlement.Counters[key] += n
		}
	}
	if settlement.Items != record.Expected {
		// The lines do not account for every item: no provider evidence for
		// the remainder, so nothing settles and the exposure stays held.
		return BatchSettlement{}, false, nil
	}
	partsWithSuccess := 0
	for _, name := range required {
		if record.Parts[name].Succeeded > 0 {
			partsWithSuccess++
		}
	}
	for key, count := range present {
		if count != partsWithSuccess || unknown[key] {
			delete(settlement.Counters, key)
		}
	}
	for key := range unknown {
		delete(settlement.Counters, key)
	}
	settlement.Counters["batch_requests"] = settlement.Succeeded
	if len(models) == 1 {
		for model := range models {
			settlement.Model = model
		}
	} else if len(models) == 0 {
		settlement.Model = record.Model
	}
	return settlement, true, nil
}

// crossCheckAgrees compares batch-object totals with the per-item sums on
// the counters both report. It is evidence of consistency only.
func crossCheckAgrees(cross, sums map[string]int64) bool {
	for key, value := range cross {
		if sum, ok := sums[key]; ok && sum != value {
			return false
		}
	}
	return true
}

// openAIBatchObjectUsage reads the OpenAI batch object's usage
// (https://developers.openai.com/api/reference/resources/batches/methods/create)
// into the normalized vocabulary for the cross-check.
func openAIBatchObjectUsage(raw json.RawMessage) map[string]int64 {
	counters, _, err := nativeBodyCounters(ProviderOpenAI, append(append([]byte(`{"usage":`), raw...), '}'))
	if err != nil || len(counters) == 0 {
		return nil
	}
	return counters
}
