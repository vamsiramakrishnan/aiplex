package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// ErrBatchUsageIncomplete is returned by a BatchSettler whose protected
// meters are not all reported by the batch's results. The results are still
// delivered; the batch keeps its admitted exposure.
var ErrBatchUsageIncomplete = errors.New("batch results omit a protected meter")

const (
	maxBatchLineBytes   = 16 << 20
	batchFileAliasKind  = "openai-file"
	batchSaveAttempts   = 8
	batchDimensionValue = "true"
)

// ---- media ----

// mediaUsage is one parsed media observation.
type mediaUsage struct {
	counters   map[string]int64
	dimensions map[string]string
	terminal   bool
}

// transcriptionUsage reads the Transcription usage union: {type:"tokens",
// input_tokens, output_tokens, total_tokens, input_token_details{audio_tokens,
// text_tokens}} or {type:"duration", seconds}
// (https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create).
// Seconds become audio_input_milliseconds (rounded up); they are never mapped
// into tokens.
func mediaTranscriptionUsage(raw json.RawMessage) (map[string]int64, error) {
	var usage map[string]json.RawMessage
	if json.Unmarshal(raw, &usage) != nil || usage == nil {
		return nil, fmt.Errorf("invalid transcription usage")
	}
	var kind string
	_ = json.Unmarshal(usage["type"], &kind)
	counters := map[string]int64{}
	switch kind {
	case "duration":
		ms, err := secondsToMilliseconds(usage["seconds"])
		if err != nil {
			return nil, err
		}
		counters[CounterAudioInputMilliseconds] = ms
	case "tokens":
		for target, name := range map[string]string{"input_tokens": "input_tokens", "output_tokens": "output_tokens", "total_tokens": "total_tokens"} {
			n, known, err := nonNegativeCounter(usage[name])
			if err != nil {
				return nil, err
			}
			if known {
				counters[target] = n
			}
		}
		if err := modalityCounters(usage, counters); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown transcription usage type")
	}
	return counters, nil
}

// imageTokenUsage reads the GPT image models' usage object
// (https://developers.openai.com/api/reference/resources/images/methods/generate).
func imageTokenUsage(raw json.RawMessage, counters map[string]int64) error {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var usage map[string]json.RawMessage
	if json.Unmarshal(raw, &usage) != nil || usage == nil {
		return fmt.Errorf("invalid image usage")
	}
	for _, name := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		n, known, err := nonNegativeCounter(usage[name])
		if err != nil {
			return err
		}
		if known {
			counters[name] = n
		}
	}
	return modalityCounters(usage, counters)
}

func imageDimensions(object map[string]json.RawMessage) map[string]string {
	out := map[string]string{}
	for key, target := range map[string]string{"size": DimensionImageSize, "quality": DimensionImageQuality} {
		var value string
		if raw := object[key]; raw != nil && json.Unmarshal(raw, &value) == nil && value != "" && len(value) <= 64 && validSegment(value) {
			out[target] = value
		}
	}
	return out
}

// mediaJSONUsage reads a complete JSON media response.
func mediaJSONUsage(format string, data []byte) (mediaUsage, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return mediaUsage{}, fmt.Errorf("invalid media response")
	}
	out := mediaUsage{counters: map[string]int64{}}
	switch format {
	case FormatOpenAITranscription:
		if raw := root["usage"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			counters, err := mediaTranscriptionUsage(raw)
			if err != nil {
				return mediaUsage{}, err
			}
			out.counters, out.terminal = counters, true
		}
	case FormatOpenAIImages:
		var items []json.RawMessage
		if raw := root["data"]; raw != nil {
			if json.Unmarshal(raw, &items) != nil {
				return mediaUsage{}, fmt.Errorf("invalid image data")
			}
			out.counters[CounterGeneratedImages] = int64(len(items))
			out.terminal = true
		}
		if err := imageTokenUsage(root["usage"], out.counters); err != nil {
			return mediaUsage{}, err
		}
		out.dimensions = imageDimensions(root)
	}
	return out, nil
}

func (x *extendedExchange) serveMedia() {
	media := x.mediaType()
	switch {
	case media == "text/event-stream":
		x.serveMediaSSE()
		return
	case media != "application/json" || x.spec.kind == kindSpeech:
		// Raw audio, srt/vtt/text transcripts: no usage is documented in these
		// bodies; relayed without buffering, counted as one exchange.
		x.streamPassthrough()
		return
	}
	data, err := x.readBounded()
	if err != nil {
		x.fail(err, http.StatusBadGateway, "upstream response exceeds observation bound")
		return
	}
	usage, err := mediaJSONUsage(x.spec.format, data)
	if err != nil {
		if x.run != nil {
			x.fail(err, http.StatusBadGateway, "upstream response could not be acknowledged")
			return
		}
		usage = mediaUsage{}
	}
	if usage.terminal {
		if err = x.emit(UsageObservation{Counters: usage.counters, Dimensions: usage.dimensions, Terminal: true, Cumulative: true}); err != nil {
			x.fail(err, http.StatusBadGateway, "upstream response could not be acknowledged")
			return
		}
	}
	if err = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true, Terminal: usage.terminal}); err != nil {
		writeExecutionError(x.w, err)
		return
	}
	x.writeHeaders()
	_, _ = x.w.Write(data)
}

// serveMediaSSE relays transcription, image and speech event streams. Frames
// are forwarded as they arrive until the terminal event (transcript.text.done,
// or the n-th image_generation.completed / image_edit.completed); from there
// the tail is held until the stream ends cleanly and its usage is
// acknowledged. Partial-image events carry no usage. With more than one
// completed image event the token usage of each is not established as
// per-image or cumulative (UNCONFIRMED), so tokens stay unknown and only the
// image count is observed.
func (x *extendedExchange) serveMediaSSE() {
	x.writeHeaders()
	input := bufio.NewReaderSize(x.response.Body, 32<<10)
	var frame, tail bytes.Buffer
	usage := mediaUsage{counters: map[string]int64{}, dimensions: map[string]string{}}
	completed := int64(0)
	tokenEvents := 0
	terminal := false
	write := func(data []byte) error {
		_, err := flushWriter{x.w}.Write(data)
		return err
	}
	for {
		chunk, readErr := input.ReadSlice('\n')
		if frame.Len()+tail.Len()+len(chunk) > 1<<20 && x.spec.kind != kindSpeech {
			_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: fmt.Errorf("SSE observation bound exceeded")})
			return
		}
		_, _ = frame.Write(chunk)
		complete := bytes.Equal(chunk, []byte("\n")) || bytes.Equal(chunk, []byte("\r\n"))
		if complete {
			raw := append([]byte(nil), frame.Bytes()...)
			frame.Reset()
			data, _ := sseFields(raw)
			if len(data) > 0 && !bytes.Equal(data, []byte("[DONE]")) && x.spec.kind != kindSpeech {
				var event map[string]json.RawMessage
				if json.Unmarshal(data, &event) != nil {
					_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: fmt.Errorf("invalid media event")})
					return
				}
				var kind string
				_ = json.Unmarshal(event["type"], &kind)
				switch {
				case kind == "transcript.text.done":
					if raw := event["usage"]; raw != nil {
						counters, err := mediaTranscriptionUsage(raw)
						if err != nil {
							_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: err})
							return
						}
						usage.counters = counters
					}
					terminal = true
				case kind == "image_generation.completed" || kind == "image_edit.completed":
					completed++
					if event["usage"] != nil {
						tokenEvents++
						if err := imageTokenUsage(event["usage"], usage.counters); err != nil {
							_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: err})
							return
						}
					}
					for key, value := range imageDimensions(event) {
						if prior, ok := usage.dimensions[key]; ok && prior != value {
							usage.dimensions[key] = "mixed"
						} else {
							usage.dimensions[key] = value
						}
					}
					terminal = completed >= x.images
				}
			}
			if terminal {
				_, _ = tail.Write(raw)
			} else if err := write(raw); err != nil {
				_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: err})
				return
			}
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			if !errors.Is(readErr, io.EOF) || frame.Len() != 0 || x.r.Context().Err() != nil {
				_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: errors.Join(readErr, io.ErrUnexpectedEOF)})
				return
			}
			if x.spec.kind == kindSpeech {
				_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true})
				return
			}
			if !terminal {
				_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true, Err: fmt.Errorf("media stream ended without its completion event")})
				return
			}
			if x.spec.format == FormatOpenAIImages {
				usage.counters[CounterGeneratedImages] = completed
				if tokenEvents > 1 {
					for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", CounterInputTextTokens, CounterInputImageTokens, CounterOutputTextTokens, CounterOutputImageTokens} {
						delete(usage.counters, key)
					}
				}
			}
			if err := x.emit(UsageObservation{Counters: usage.counters, Dimensions: usage.dimensions, Terminal: true, Cumulative: true}); err != nil {
				_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true, Err: err})
				return
			}
			if err := x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true, Terminal: true}); err != nil {
				return
			}
			_ = write(tail.Bytes())
			return
		}
		if x.r.Context().Err() != nil {
			_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: x.r.Context().Err()})
			return
		}
	}
}

// ---- files and batches ----

func (x *extendedExchange) batchMarker() error {
	return x.emit(UsageObservation{Dimensions: map[string]string{DimensionBatch: batchDimensionValue}, Cumulative: true})
}

func objectID(data []byte) (string, map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return "", nil, fmt.Errorf("invalid resource response")
	}
	var id string
	if json.Unmarshal(object["id"], &id) != nil || !validSegment(id) {
		return "", nil, fmt.Errorf("resource response omitted a valid id")
	}
	return id, object, nil
}

// serveFileUpload binds an uploaded batch input file to its owner before the
// caller learns its id.
func (x *extendedExchange) serveFileUpload() {
	data, err := x.readBounded()
	if err != nil {
		x.fail(err, http.StatusBadGateway, "upstream response exceeds observation bound")
		return
	}
	id, _, err := objectID(data)
	if err == nil {
		err = x.g.options.Ownership.Bind(x.r.Context(), x.owner, Resource{openAIBatchFileResource, id})
	}
	if err != nil {
		x.fail(err, http.StatusBadGateway, "resource ownership could not be persisted")
		return
	}
	if err = x.batchMarker(); err == nil {
		err = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true})
	}
	if err != nil {
		writeExecutionError(x.w, err)
		return
	}
	x.writeHeaders()
	_, _ = x.w.Write(data)
}

func anthropicBatchItems(body []byte) int64 {
	var doc struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return -1
	}
	return int64(len(doc.Requests))
}

// serveBatchCreate binds and pins the new batch, records it durably with its
// admitted attempt, and only then releases the batch id to the caller.
func (x *extendedExchange) serveBatchCreate() {
	data, err := x.readBounded()
	if err != nil {
		x.fail(err, http.StatusBadGateway, "upstream response exceeds observation bound")
		return
	}
	id, object, err := objectID(data)
	if err == nil {
		err = x.g.options.Ownership.Bind(x.r.Context(), x.owner, Resource{providerBatchResource, id})
	}
	if err == nil {
		err = x.run.resource(Resource{providerBatchResource, id})
	}
	if err == nil && x.g.options.Batches != nil {
		record := BatchRecord{Owner: x.owner, Provider: x.spec.provider, BatchID: id, CreateOperation: x.op.ID, Model: x.metadata.Model, Expected: -1}
		if x.run != nil {
			if receipt, ok := x.run.lease.(execution.Receipt); ok {
				record.AttemptID = receipt.AttemptID()
			}
		}
		if x.spec.provider == ProviderAnthropic {
			record.Expected = anthropicBatchItems(x.body)
		}
		applyBatchObject(x.spec.provider, object, &record)
		ctx, cancel := executionContext(x.r.Context())
		_, err = x.g.options.Batches.SaveBatch(ctx, record, 0)
		cancel()
	}
	if err != nil {
		x.fail(err, http.StatusBadGateway, "batch could not be durably tracked")
		return
	}
	if err = x.batchMarker(); err == nil {
		err = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true})
	}
	if err != nil {
		writeExecutionError(x.w, err)
		return
	}
	x.writeHeaders()
	_, _ = x.w.Write(data)
}

// applyBatchObject folds a provider batch object into the record.
func applyBatchObject(provider string, object map[string]json.RawMessage, record *BatchRecord) {
	switch provider {
	case ProviderOpenAI:
		var status string
		_ = json.Unmarshal(object["status"], &status)
		record.Status = status
		switch status {
		case "completed", "expired", "cancelled", "failed":
			var counts struct {
				Total *int64 `json:"total"`
			}
			if json.Unmarshal(object["request_counts"], &counts) == nil && counts.Total != nil && *counts.Total >= 0 {
				record.Expected = *counts.Total
			}
			for key, target := range map[string]*string{"output_file_id": &record.OutputFileID, "error_file_id": &record.ErrorFileID} {
				var value string
				if raw := object[key]; raw != nil && json.Unmarshal(raw, &value) == nil && validSegment(value) {
					*target = value
				}
			}
			if raw := object["usage"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				record.CrossCheck = openAIBatchObjectUsage(raw)
			}
			record.Ended = true
		}
	case ProviderAnthropic:
		var status string
		_ = json.Unmarshal(object["processing_status"], &status)
		record.Status = status
		if status != "ended" {
			return
		}
		var counts map[string]int64
		if json.Unmarshal(object["request_counts"], &counts) == nil {
			var total int64
			for _, key := range []string{"processing", "succeeded", "errored", "canceled", "expired"} {
				total += counts[key]
			}
			switch {
			case record.Expected < 0:
				record.Expected = total
			case record.Expected != total:
				// The provider's counts disagree with the created request
				// list: no completeness evidence, nothing may settle.
				record.Expected = -1
			}
		}
		record.Ended = true
	}
}

// serveBatchRead relays a batch object (retrieve or cancel), records its
// status, and binds its results files to the batch owner.
func (x *extendedExchange) serveBatchRead() {
	data, err := x.readBounded()
	if err != nil {
		x.fail(err, http.StatusBadGateway, "upstream response exceeds observation bound")
		return
	}
	id, object, err := objectID(data)
	if err != nil || id != x.metadata.Resource.ID {
		x.fail(fmt.Errorf("batch object identity mismatch"), http.StatusBadGateway, "upstream batch object is invalid")
		return
	}
	var files []Resource
	err = x.g.advanceBatch(x.r.Context(), x.owner, x.spec.provider, id, func(record *BatchRecord) {
		applyBatchObject(x.spec.provider, object, record)
		for _, file := range []struct{ id, part string }{{record.OutputFileID, "output"}, {record.ErrorFileID, "error"}} {
			if file.id != "" {
				files = append(files, Resource{file.part, file.id})
			}
		}
	})
	for _, file := range files {
		if err != nil {
			break
		}
		err = x.g.options.Ownership.Bind(x.r.Context(), x.owner, Resource{openAIBatchFileResource, file.ID})
		if err == nil && x.g.options.Batches != nil {
			alias := BatchRecord{Owner: x.owner, Provider: batchFileAliasKind, BatchID: file.ID, AliasOf: id, AliasPart: file.Kind, Expected: -1}
			ctx, cancel := executionContext(x.r.Context())
			if _, saveErr := x.g.options.Batches.SaveBatch(ctx, alias, 0); saveErr != nil && !errors.Is(saveErr, ErrBatchConflict) {
				err = saveErr
			}
			cancel()
		}
	}
	if err != nil {
		x.fail(err, http.StatusBadGateway, "batch state could not be acknowledged")
		return
	}
	if err = x.batchMarker(); err == nil {
		err = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true})
	}
	if err != nil {
		writeExecutionError(x.w, err)
		return
	}
	x.writeHeaders()
	_, _ = x.w.Write(data)
}

// serveFileContent relays a file; an output or error file of a tracked batch
// is also aggregated for settlement.
func (x *extendedExchange) serveFileContent() {
	if x.g.options.Batches != nil {
		ctx, cancel := executionContext(x.r.Context())
		alias, _, err := x.g.options.Batches.LoadBatch(ctx, x.owner, batchFileAliasKind, x.metadata.Resource.ID)
		cancel()
		if err == nil && alias.AliasOf != "" {
			x.serveResults(x.op.ID, alias.AliasOf, alias.AliasPart)
			return
		}
	}
	x.streamPassthrough()
}

// serveResults relays a JSONL results body line by line, holding back the
// last line. Only a body that ends cleanly and parses completely contributes
// a results part; settlement (when the record becomes complete) is
// acknowledged before the held line is released.
func (x *extendedExchange) serveResults(_ string, batchID, part string) {
	x.writeHeaders()
	input := bufio.NewReaderSize(x.response.Body, 64<<10)
	var line, held bytes.Buffer
	var lines []batchLine
	parseErr := error(nil)
	flushHeld := func() error {
		if held.Len() == 0 {
			return nil
		}
		_, err := flushWriter{x.w}.Write(held.Bytes())
		held.Reset()
		return err
	}
	consume := func() error {
		raw := append([]byte(nil), line.Bytes()...)
		line.Reset()
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && parseErr == nil {
			parsed, err := parseBatchLine(x.spec.provider, part, trimmed)
			if err != nil {
				parseErr = err
			} else {
				lines = append(lines, parsed)
			}
		}
		if err := flushHeld(); err != nil {
			return err
		}
		_, _ = held.Write(raw)
		return nil
	}
	for {
		chunk, readErr := input.ReadSlice('\n')
		if line.Len()+len(chunk) > maxBatchLineBytes {
			_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: fmt.Errorf("batch results line bound exceeded")})
			return
		}
		_, _ = line.Write(chunk)
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			if err := consume(); err != nil {
				_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: err})
				return
			}
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			if !errors.Is(readErr, io.EOF) || x.r.Context().Err() != nil {
				// Interrupted: nothing is aggregated, the held line is dropped.
				_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: errors.Join(readErr, io.ErrUnexpectedEOF)})
				return
			}
			if line.Len() > 0 {
				if err := consume(); err != nil {
					_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: err})
					return
				}
			}
			break
		}
	}
	var settleErr error
	if parseErr == nil {
		seen := map[string]bool{}
		for _, item := range lines {
			if seen[item.id] {
				parseErr = fmt.Errorf("duplicate batch item")
			}
			seen[item.id] = true
		}
	}
	if parseErr == nil {
		aggregate := aggregateBatchLines(lines)
		settleErr = x.g.advanceBatch(x.r.Context(), x.owner, x.spec.provider, batchID, func(record *BatchRecord) {
			if record.Parts == nil {
				record.Parts = map[string]BatchPart{}
			}
			record.Parts[part] = aggregate
		})
	}
	if settleErr != nil {
		_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true, Err: settleErr})
		return
	}
	if err := x.batchMarker(); err != nil {
		_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true, Err: err})
		return
	}
	if err := x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true}); err != nil {
		return
	}
	_ = flushHeld()
}

// advanceBatch applies mutate to the durable record and settles it exactly
// once when it becomes complete (see batch.go). A batch not created through
// this gateway has no record and is not settled here.
func (g *Gateway) advanceBatch(parent context.Context, owner Owner, provider, batchID string, mutate func(*BatchRecord)) error {
	if g.options.Batches == nil {
		return nil
	}
	ctx, cancel := executionContext(parent)
	defer cancel()
	for attempt := 0; attempt < batchSaveAttempts; attempt++ {
		record, revision, err := g.options.Batches.LoadBatch(ctx, owner, provider, batchID)
		if errors.Is(err, ErrBatchNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if record.Owner != owner || record.Provider != provider || record.BatchID != batchID {
			return fmt.Errorf("batch record identity mismatch")
		}
		mutate(&record)
		settlement, complete, err := batchSettlement(record)
		if err != nil {
			return err
		}
		if complete && record.CrossCheck != nil && !crossCheckAgrees(record.CrossCheck, settlement.Counters) {
			record.CrossCheckMismatch, complete = true, false
		}
		if complete && g.options.BatchSettler != nil {
			if err := g.options.BatchSettler.SettleBatch(ctx, owner, settlement); err != nil {
				if !errors.Is(err, ErrBatchUsageIncomplete) {
					return err
				}
				complete = false
			}
		}
		if complete {
			record.Settled, record.Usage = true, settlement.Counters
		}
		if _, err = g.options.Batches.SaveBatch(ctx, record, revision); errors.Is(err, ErrBatchConflict) {
			continue
		} else if err != nil {
			return err
		}
		if complete && g.options.BatchSettler != nil {
			g.options.BatchSettler.RecordBatch(ctx, owner, settlement)
		}
		return nil
	}
	return ErrBatchConflict
}
