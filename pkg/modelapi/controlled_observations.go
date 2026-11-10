package modelapi

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

func geminiGeneration(op Operation) bool {
	return strings.HasSuffix(op.ID, ".generateContent") || strings.HasSuffix(op.ID, ".streamGenerateContent")
}

// Gemini's documented total is prompt + candidates + thoughts; cached input is
// a subset of prompt. Normalize once for a complete final observation, never by
// summing overlapping totals or individually rounded stream chunks.
func normalizeGeminiFinal(value *UsageObservation) error {
	input, inputOK := value.Counters["input_tokens"]
	output, outputOK := value.Counters["output_tokens"]
	total, totalOK := value.Counters["total_tokens"]
	if !inputOK || !outputOK || !totalOK || input < 0 || output < 0 || total < input || total-input < output {
		return fmt.Errorf("complete consistent Gemini usage is required")
	}
	thought := total - input - output
	if explicit, exists := value.Counters["thought_tokens"]; exists && explicit != thought {
		return fmt.Errorf("Gemini thought usage conflicts with total")
	}
	// An omitted cachedContentTokenCount is 0. Google's REST pages state no
	// default, but both service definitions declare it as a plain proto3
	// scalar without field presence: `int32 cached_content_token_count = 5`
	// (google/cloud/aiplatform/v1/prediction_service.proto, UsageMetadata) and
	// `int32 cached_content_token_count = 4`
	// (google/ai/generativelanguage/v1beta/generative_service.proto), and the
	// proto3 JSON mapping omits fields holding their default value
	// (https://protobuf.dev/programming-guides/json/). Absence therefore
	// encodes 0, never "not reported".
	cached := value.Counters["cached_tokens"]
	if cached < 0 || cached > input || value.Counters["tool_use_tokens"] != 0 {
		return fmt.Errorf("unsupported Gemini usage composition")
	}
	value.Counters["thought_tokens"] = thought
	value.Counters["cached_tokens"] = cached
	value.Counters["uncached_input_tokens"] = input - cached
	value.Cumulative = true
	return nil
}

func observeVersionMetadata(run *executionRun, value jsonObservation) error {
	if value.modelVersion == "" {
		return nil
	}
	return run.observe(UsageObservation{Source: "upstream", ModelVersion: value.modelVersion, Counters: map[string]int64{}, Cumulative: true})
}

func (g *Gateway) inspectControlled(owner Owner, op Operation, data []byte, run *executionRun, metadataFirst bool) (jsonObservation, error) {
	if err := validateJSON(data); err != nil {
		return jsonObservation{}, err
	}
	ctx, cancel := executionContext(run.ctx)
	defer cancel()
	value, err := g.inspectJSON(ctx, owner, op, data, false, true)
	if metadataFirst {
		value.deferredVersion = err == nil && value.nativeTerminal && value.usage != nil
		if value.deferredVersion {
			value.deferredVersion = normalizeForRun(op, value.usage, run) == nil
		}
		if !value.deferredVersion {
			if observeErr := observeVersionMetadata(run, value); observeErr != nil {
				return value, observeErr
			}
		}
	}
	if err != nil {
		return value, err
	}
	if geminiGeneration(op) && (value.candidateCount > 1 || value.foreignCandidate) {
		return value, fmt.Errorf("controlled Gemini generation requires one candidate")
	}
	if err = run.resource(value.resource); err != nil {
		return value, err
	}
	return value, nil
}

func (g *Gateway) observeControlledJSON(owner Owner, op Operation, data []byte, run *executionRun) (bool, error) {
	value, err := g.inspectControlled(owner, op, data, run, false)
	if err != nil {
		if observeErr := observeVersionMetadata(run, value); observeErr != nil {
			return false, observeErr
		}
		return false, err
	}
	if value.usage == nil || !value.nativeTerminal {
		if observeErr := observeVersionMetadata(run, value); observeErr != nil {
			return false, observeErr
		}
	}
	if value.usage == nil {
		return false, nil
	}
	value.usage.Terminal = value.nativeTerminal
	run.modelIdentity(value.usage)
	if value.nativeTerminal {
		if err = normalizeForRun(op, value.usage, run); err != nil {
			if observeErr := observeVersionMetadata(run, value); observeErr != nil {
				return false, observeErr
			}
			return false, err
		}
	}
	if err = run.observe(*value.usage); err != nil {
		return false, err
	}
	g.emitLegacy(run.ctx, owner, value.usage)
	return value.usage.Terminal, nil
}

func sseFields(frame []byte) (data []byte, eventID string) {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if bytes.HasPrefix(line, []byte("id:")) {
			eventID = string(bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("id:")), []byte(" ")))
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			part := bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, part...)
		}
	}
	return
}

// copyControlledSSE forwards ordinary frames immediately. The bounded native
// final tail is held until clean protocol completion and acknowledged terminal
// usage; this preserves TTFT without delivering a false settled completion.
func (g *Gateway) copyControlledSSE(w http.ResponseWriter, r *http.Request, reader io.Reader, owner Owner, op Operation, run *executionRun, status int) {
	input := bufio.NewReaderSize(reader, 32<<10)
	anthropic := op.ID == "anthropic.messages" || (g.surface.Publisher == "anthropic" && anthropicOperation(op))
	validateCumulative := anthropic || !requestCountRun(run)
	var frame, tail bytes.Buffer
	var latest *UsageObservation
	seenFinal, seenDone := false, false
	anthropicStarted, anthropicStopped, anthropicFinalUsage := false, false, false
	modelVersion := ""
	var anthropicLatest *UsageObservation
	var cumulative execution.CumulativeUsage
	deferredVersion := ""
	finish := func(result execution.Result) {
		if deferredVersion != "" {
			if err := run.observe(UsageObservation{Source: "upstream", ModelVersion: deferredVersion, Counters: map[string]int64{}, Cumulative: true}); err != nil {
				result.Err = errors.Join(result.Err, err)
			}
			deferredVersion = ""
		}
		result.StatusCode = status
		_ = run.finish(result)
	}
	write := func(data []byte) error {
		if _, err := w.Write(data); err != nil {
			return err
		}
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		return nil
	}
	for {
		chunk, readErr := input.ReadSlice('\n')
		if frame.Len()+tail.Len()+len(chunk) > 1<<20 {
			finish(execution.Result{Err: fmt.Errorf("SSE observation bound exceeded")})
			return
		}
		_, _ = frame.Write(chunk)
		complete := bytes.Equal(chunk, []byte("\n")) || bytes.Equal(chunk, []byte("\r\n"))
		if complete {
			raw := append([]byte(nil), frame.Bytes()...)
			data, id := sseFields(raw)
			if (seenDone || anthropicStopped) && len(data) > 0 {
				finish(execution.Result{Err: fmt.Errorf("native data followed stream completion")})
				return
			}
			if bytes.Equal(data, []byte("[DONE]")) {
				seenDone = true
				seenFinal = true
			}
			if len(data) > 0 && !bytes.Equal(data, []byte("[DONE]")) {
				value, err := g.inspectControlled(owner, op, data, run, true)
				if value.deferredVersion && value.modelVersion != "" {
					deferredVersion = value.modelVersion
				} else if value.modelVersion != "" {
					deferredVersion = ""
				}
				if err != nil {
					finish(execution.Result{Err: err})
					return
				}
				if value.modelVersion != "" {
					if modelVersion != "" && modelVersion != value.modelVersion {
						finish(execution.Result{Err: fmt.Errorf("native model version changed within stream")})
						return
					}
					if !value.deferredVersion && deferredVersion == value.modelVersion {
						deferredVersion = ""
					}
					modelVersion = value.modelVersion
				}
				if seenFinal && geminiGeneration(op) && value.candidateCount > 0 && !value.nativeTerminal {
					finish(execution.Result{Err: fmt.Errorf("native generation continued after final marker")})
					return
				}
				if anthropic && value.eventType == "message_start" && value.resource.ID != "" && value.usage != nil {
					anthropicStarted = true
				}
				if anthropic && value.eventType == "message_stop" {
					anthropicStopped = true
				}
				if anthropic && value.eventType == "message_delta" && value.nativeTerminal && value.usage != nil {
					_, anthropicFinalUsage = value.usage.Counters["output_tokens"]
				}
				// Validate every cumulative update, including the withheld final
				// tail. Replacing latest must not erase higher observed usage.
				if value.usage != nil && value.usage.Cumulative && validateCumulative {
					if err := cumulative.Observe(value.usage.Counters); err != nil {
						finish(execution.Result{Err: err})
						return
					}
				}
				if anthropic && value.usage != nil {
					if anthropicLatest == nil {
						copy := *value.usage
						copy.Counters = map[string]int64{}
						anthropicLatest = &copy
					}
					for key, n := range value.usage.Counters {
						anthropicLatest.Counters[key] = n
					}
					if value.usage.ModelVersion != "" {
						anthropicLatest.ModelVersion = value.usage.ModelVersion
					}
					if value.usage.ResourceID != "" {
						anthropicLatest.ResourceID = value.usage.ResourceID
					}
					copy := *anthropicLatest
					copy.Counters = map[string]int64{}
					for key, n := range anthropicLatest.Counters {
						copy.Counters[key] = n
					}
					value.usage = &copy
				}
				if value.nativeTerminal {
					seenFinal = true
					if anthropic && value.usage == nil && anthropicLatest != nil {
						copy := *anthropicLatest
						value.usage = &copy
					}
				}
				if value.usage != nil {
					value.usage.EventID = id
					value.usage.Terminal = false
					if seenFinal {
						latest = value.usage
					} else {
						if err = run.observe(*value.usage); err != nil {
							finish(execution.Result{Err: err})
							return
						}
						g.emitLegacy(run.ctx, owner, value.usage)
					}
				}
			}
			if seenFinal {
				_, _ = tail.Write(raw)
			} else {
				if err := write(raw); err != nil {
					finish(execution.Result{Err: err})
					return
				}
			}
			frame.Reset()
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			if !errors.Is(readErr, io.EOF) {
				finish(execution.Result{Err: readErr})
				return
			}
			if frame.Len() != 0 {
				finish(execution.Result{Err: io.ErrUnexpectedEOF})
				return
			}
			if r.Context().Err() != nil {
				finish(execution.Result{Err: r.Context().Err()})
				return
			}
			markerComplete := seenFinal && (!chatCompletionsOperation(op) || seenDone) && (!anthropic || (anthropicStarted && anthropicStopped && anthropicFinalUsage))
			if markerComplete && latest == nil {
				// Counter-only policies can acknowledge a complete native response
				// without token usage. Strict metering leases reject this result.
				if err := run.finish(execution.Result{StatusCode: status, ResponseComplete: true}); err == nil {
					_ = write(tail.Bytes())
				}
				return
			}
			terminal := markerComplete && latest != nil
			if !terminal {
				if latest != nil {
					// Usage seen in a withheld tail whose stream never
					// completed (e.g. no [DONE]) is kept as non-terminal
					// telemetry; it settles nothing.
					latest.Terminal = false
					if run.observe(*latest) == nil {
						g.emitLegacy(run.ctx, owner, latest)
					}
				}
				finish(execution.Result{ResponseComplete: true, Err: fmt.Errorf("native terminal usage unavailable")})
				return
			}
			latest.Terminal = true
			if latest.ModelVersion == "" {
				latest.ModelVersion = modelVersion
			}
			{
				if err := normalizeForRun(op, latest, run); err != nil {
					finish(execution.Result{ResponseComplete: true, Err: err})
					return
				}
			}
			if latest.Cumulative && validateCumulative {
				if err := cumulative.Observe(latest.Counters); err != nil {
					finish(execution.Result{ResponseComplete: true, Err: err})
					return
				}
			}
			// A complete terminal observation carries the version and counters
			// together, so a rejecting host can retain all trusted final facts.
			deferredVersion = ""
			if err := run.observe(*latest); err != nil {
				finish(execution.Result{ResponseComplete: true, Err: err})
				return
			}
			if err := run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Terminal: true}); err != nil {
				return
			}
			g.emitLegacy(run.ctx, owner, latest)
			_ = write(tail.Bytes())
			return
		}
		if r.Context().Err() != nil {
			finish(execution.Result{Err: r.Context().Err()})
			return
		}
	}
}
