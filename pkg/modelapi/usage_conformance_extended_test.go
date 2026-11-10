package modelapi

import (
	"bytes"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Conformance routes for media and batch operations (provider_extended.go).

func fixtureForm(fields map[string]string) (string, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.SetBoundary("aiplexfixtureboundary")
	part, _ := mw.CreateFormFile("file", "audio.wav")
	_, _ = part.Write([]byte("RIFF-synthetic-audio"))
	for _, key := range []string{"model", "stream", "response_format"} {
		if value, ok := fields[key]; ok {
			_ = mw.WriteField(key, value)
		}
	}
	_ = mw.Close()
	return buf.String(), mw.FormDataContentType()
}

var (
	openAIMediaProvider = Provider{ID: ProviderOpenAI}
	callTranscription   = func() *fixtureCall {
		body, contentType := fixtureForm(map[string]string{"model": "fixture-asr"})
		return &fixtureCall{surface: providerSurfaceFor(openAIMediaProvider, ProtocolOpenAIAudio, []string{"fixture-asr"}, "openai.v1.audio-transcriptions"), path: "/v1/audio/transcriptions", body: body, contentType: contentType}
	}()
	callTranscriptionS = func() *fixtureCall {
		body, contentType := fixtureForm(map[string]string{"model": "fixture-asr", "stream": "true"})
		return &fixtureCall{surface: providerSurfaceFor(openAIMediaProvider, ProtocolOpenAIAudio, []string{"fixture-asr"}, "openai.v1.audio-transcriptions"), path: "/v1/audio/transcriptions", body: body, contentType: contentType}
	}()
	callImages  = &fixtureCall{surface: providerSurfaceFor(openAIMediaProvider, ProtocolOpenAIImages, []string{"fixture-image"}, "openai.v1.images-generations"), path: "/v1/images/generations", body: `{"model":"fixture-image","prompt":"x"}`}
	callImagesS = &fixtureCall{surface: providerSurfaceFor(openAIMediaProvider, ProtocolOpenAIImages, []string{"fixture-image"}, "openai.v1.images-generations"), path: "/v1/images/generations", body: `{"model":"fixture-image","prompt":"x","stream":true,"partial_images":1}`}
)

func readFixture(t *testing.T, provider, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage-formats", provider, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// batchFileCheck aggregates a results file with the gateway's own batch
// parser and checks both the aggregate and whether the batch may settle.
func batchFileCheck(provider, part string, native nativeSemantics, allow map[string]string, settle func(t *testing.T, aggregate BatchPart) (BatchRecord, bool)) func(*testing.T, []byte, canonical) {
	return func(t *testing.T, data []byte, want canonical) {
		lines, err := parseBatchLines(provider, part, data)
		if err != nil {
			t.Fatal(err)
		}
		aggregate := aggregateBatchLines(lines)
		got := observedCanonical(native, aggregate.Usage)
		if aggregate.Succeeded == 0 {
			got = canonical{}
		}
		if diffs := compareCanonical(want, got, allow); len(diffs) > 0 {
			t.Errorf("batch aggregate mismatch: %s\n want %s\n got  %s", strings.Join(diffs, "; "), describe(want), describe(got))
		}
		record, wantComplete := settle(t, aggregate)
		settlement, complete, err := batchSettlement(record)
		if err != nil || complete != wantComplete {
			t.Fatalf("settlement complete=%v want %v err=%v", complete, wantComplete, err)
		}
		if complete && record.CrossCheck != nil && !crossCheckAgrees(record.CrossCheck, settlement.Counters) {
			t.Fatal("per-line sum disagrees with the batch object")
		}
	}
}

func extendedUsageRoutes() []fixtureRoute {
	const unitNote = "unit-aware: a media-units binding settles generated_images / audio_input_milliseconds; token counters are observed exactly (ledger telemetry, no token profile)"
	const batchNote = "batch-usage binding: admitted at create, settled once from complete per-line results through the gateway (succeeded lines only; batch-object usage is a cross-check, never added)"
	media := func(file, surface, transport string, call *fixtureCall, final bool, note string) fixtureRoute {
		return fixtureRoute{provider: "openai", file: file, surface: surface, transport: transport, status: statusExact, reason: note, call: call, conv: convOpenAI, native: natOpenAI, final: final, strict: strictNotApplicable}
	}
	anthropicResults := func(t *testing.T, aggregate BatchPart) BatchRecord {
		return BatchRecord{Provider: ProviderAnthropic, BatchID: "b", Expected: 5, Parts: map[string]BatchPart{"results": aggregate}}
	}
	openAIRecord := func(t *testing.T, output, errorsFile BatchPart) BatchRecord {
		object := readFixture(t, "openai", "batch.object.json")
		_, fields, err := objectID(object)
		if err != nil {
			t.Fatal(err)
		}
		record := BatchRecord{Provider: ProviderOpenAI, BatchID: "batch_fix1", Expected: -1}
		applyBatchObject(ProviderOpenAI, fields, &record)
		record.Expected, record.ErrorFileID = 3, "file-err1"
		record.Parts = map[string]BatchPart{"output": output, "error": errorsFile}
		return record
	}
	parse := func(t *testing.T, name, part string) BatchPart {
		lines, err := parseBatchLines(ProviderOpenAI, part, readFixture(t, "openai", name))
		if err != nil {
			t.Fatal(err)
		}
		return aggregateBatchLines(lines)
	}
	const liveNote = "WebSocket session: one request-count admission and one settlement per session (token-metered leases refused before the dial); voice seconds are the primary meter, observed as voice_session_milliseconds from cumulative session.usage.updated snapshots (never summed) and final only with session.closed; nested Responses/decision token usage is advisory, counted once per response"
	callLive := &fixtureCall{surface: providerSurfaceFor(openAIMediaProvider, ProtocolOpenAILive, []string{"fixture-model-live"}, openAILiveOperation), path: "/v1/live/sessions?model=fixture-model-live", body: `{"type":"session.start","session":{"model":"fixture-model-live"}}`, ws: "openai-live"}
	return []fixtureRoute{
		{provider: "openai", file: "live.ws.jsonl", surface: "live-voice-sessions", transport: "websocket", status: statusAdvisory, reason: liveNote, call: callLive, conv: convOpenAI, native: natOpenAI, final: true},
		{provider: "openai", file: "live.interrupted.ws.jsonl", surface: "live-voice-sessions", transport: "websocket", status: statusAdvisory, reason: liveNote, call: callLive, conv: convOpenAI, native: natOpenAI, final: false},
		media("transcription.json", "audio-transcriptions", "json", callTranscription, true, unitNote),
		media("transcription.duration.json", "audio-transcriptions", "json", callTranscription, true, unitNote+"; duration-billed responses report seconds, observed as audio_input_milliseconds and never as tokens"),
		media("transcription.sse", "audio-transcriptions", "sse", callTranscriptionS, true, unitNote+"; usage only on transcript.text.done, whose frame is held until acknowledged"),
		media("transcription.interrupted.sse", "audio-transcriptions", "sse", callTranscriptionS, false, unitNote),
		media("images.json", "images", "json", callImages, true, unitNote+"; images without a usage object (non-GPT image models) still report generated_images"),
		media("images.sse", "images", "sse", callImagesS, true, unitNote+"; partial_image events carry no usage; the n-th completed event is terminal"),
		media("images.interrupted.sse", "images", "sse", callImagesS, false, unitNote),
		{provider: "openai", file: "batch.output.jsonl", surface: "batch", transport: "jsonl-file", status: statusExact, reason: batchNote + "; read through GET /v1/files/{id}/content of a tracked batch's output_file_id", conv: convOpenAI, native: natOpenAI, check: batchFileCheck(ProviderOpenAI, "output", natOpenAI, nil, func(t *testing.T, aggregate BatchPart) (BatchRecord, bool) {
			return openAIRecord(t, aggregate, parse(t, "batch.errors.jsonl", "error")), true
		})},
		{provider: "openai", file: "batch.errors.jsonl", surface: "batch", transport: "jsonl-file", status: statusExact, reason: "error lines are provider evidence of failed/expired items (released, never charged)", conv: convOpenAI, native: natOpenAI, check: batchFileCheck(ProviderOpenAI, "error", natOpenAI, nil, func(t *testing.T, aggregate BatchPart) (BatchRecord, bool) {
			record := openAIRecord(t, parse(t, "batch.output.jsonl", "output"), aggregate)
			delete(record.Parts, "output")
			return record, false // the output file is still needed
		})},
		{provider: "openai", file: "batch.object.json", surface: "batch", transport: "json", status: statusExact, reason: "batch object (create/retrieve/cancel): status, request_counts and results file ids drive settlement; its usage is a cross-check only", conv: convOpenAI, native: natOpenAI, check: func(t *testing.T, data []byte, want canonical) {
			_, fields, err := objectID(data)
			if err != nil {
				t.Fatal(err)
			}
			got := observedCanonical(natOpenAI, openAIBatchObjectUsage(fields["usage"]))
			if diffs := compareCanonical(want, got, nil); len(diffs) > 0 {
				t.Errorf("cross-check usage: %v", diffs)
			}
		}},
		{provider: "anthropic", file: "batch-results.jsonl", surface: "message-batches", transport: "jsonl-file", status: statusExact, reason: batchNote + "; GET /v1/messages/batches/{id}/results; a cache counter absent on any succeeded line leaves that sum unknown", conv: convAnthropic, native: natAnthropic, check: batchFileCheck(ProviderAnthropic, "results", natAnthropic, map[string]string{"cache_write_tokens": "absent on one succeeded line: sum unknown, not zero"}, func(t *testing.T, aggregate BatchPart) (BatchRecord, bool) {
			return anthropicResults(t, aggregate), true
		})},
		{provider: "anthropic", file: "batch-results-partial.jsonl", surface: "message-batches", transport: "jsonl-file", status: statusExact, reason: batchNote, conv: convAnthropic, native: natAnthropic, check: batchFileCheck(ProviderAnthropic, "results", natAnthropic, nil, func(t *testing.T, aggregate BatchPart) (BatchRecord, bool) {
			return anthropicResults(t, aggregate), false // one of five items: nothing settles
		})},
	}
}
