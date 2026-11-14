package modelapi

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// AWS event stream framing (application/vnd.amazon.eventstream):
//
//	total length (uint32 BE) | headers length (uint32 BE) | prelude CRC32
//	headers | payload | message CRC32 (over everything before it)
//
// Header: name length (uint8), name, value type (uint8), value. CRCs are
// IEEE CRC-32. Each message is forwarded byte-for-byte after both checksums
// verify; a corrupted or truncated frame ends the stream as incomplete.
type eventMessage struct {
	raw     []byte
	headers map[string]string
	payload []byte
}

const maxEventMessage = 16 << 20

var errEventStreamTruncated = errors.New("truncated event stream message")

func readEventMessage(r *bufio.Reader) (eventMessage, error) {
	prelude := make([]byte, 12)
	n, err := io.ReadFull(r, prelude)
	if err != nil {
		if errors.Is(err, io.EOF) && n == 0 {
			return eventMessage{}, io.EOF
		}
		return eventMessage{}, errEventStreamTruncated
	}
	total := binary.BigEndian.Uint32(prelude[0:4])
	headersLength := binary.BigEndian.Uint32(prelude[4:8])
	if crc32.ChecksumIEEE(prelude[0:8]) != binary.BigEndian.Uint32(prelude[8:12]) {
		return eventMessage{}, fmt.Errorf("event stream prelude checksum mismatch")
	}
	if total < 16 || total > maxEventMessage || uint64(headersLength) > uint64(total)-16 {
		return eventMessage{}, fmt.Errorf("invalid event stream message length")
	}
	raw := make([]byte, total)
	copy(raw, prelude)
	if _, err = io.ReadFull(r, raw[12:]); err != nil {
		return eventMessage{}, errEventStreamTruncated
	}
	if crc32.ChecksumIEEE(raw[:total-4]) != binary.BigEndian.Uint32(raw[total-4:]) {
		return eventMessage{}, fmt.Errorf("event stream message checksum mismatch")
	}
	headers, err := parseEventHeaders(raw[12 : 12+headersLength])
	if err != nil {
		return eventMessage{}, err
	}
	return eventMessage{raw: raw, headers: headers, payload: raw[12+headersLength : total-4]}, nil
}

func parseEventHeaders(b []byte) (map[string]string, error) {
	out := map[string]string{}
	for len(b) > 0 {
		nameLength := int(b[0])
		if nameLength == 0 || len(b) < 1+nameLength+1 {
			return nil, fmt.Errorf("invalid event stream header")
		}
		name := string(b[1 : 1+nameLength])
		kind := b[1+nameLength]
		b = b[2+nameLength:]
		fixed := map[byte]int{0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16}
		if size, ok := fixed[kind]; ok {
			if len(b) < size {
				return nil, fmt.Errorf("invalid event stream header value")
			}
			b = b[size:]
			continue
		}
		if kind != 6 && kind != 7 {
			return nil, fmt.Errorf("unknown event stream header type")
		}
		if len(b) < 2 {
			return nil, fmt.Errorf("invalid event stream header value")
		}
		size := int(binary.BigEndian.Uint16(b[:2]))
		if len(b) < 2+size {
			return nil, fmt.Errorf("invalid event stream header value")
		}
		if kind == 7 {
			if _, dup := out[name]; dup {
				return nil, fmt.Errorf("duplicate event stream header")
			}
			out[name] = string(b[2 : 2+size])
		}
		b = b[2+size:]
	}
	return out, nil
}

// encodeEventMessage frames string headers and a payload (test fixtures and
// documentation of the format; the gateway never re-encodes upstream frames).
func encodeEventMessage(headers [][2]string, payload []byte) []byte {
	var h bytes.Buffer
	for _, kv := range headers {
		h.WriteByte(byte(len(kv[0])))
		h.WriteString(kv[0])
		h.WriteByte(7)
		_ = binary.Write(&h, binary.BigEndian, uint16(len(kv[1])))
		h.WriteString(kv[1])
	}
	total := uint32(12 + h.Len() + len(payload) + 4)
	out := make([]byte, 12, total)
	binary.BigEndian.PutUint32(out[0:4], total)
	binary.BigEndian.PutUint32(out[4:8], uint32(h.Len()))
	binary.BigEndian.PutUint32(out[8:12], crc32.ChecksumIEEE(out[0:8]))
	out = append(out, h.Bytes()...)
	out = append(out, payload...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// copyEventStream forwards verified Bedrock event-stream messages. For
// ConverseStream, the messageStop/metadata tail is withheld under an execution
// lease until the terminal metadata usage is acknowledged. InvokeModel chunks
// carry model-native payloads ({"bytes": base64}); when the decoded payload is
// the Anthropic Messages event shape its cumulative usage is observed exactly
// as for Messages SSE (message_start input, cumulative message_delta,
// message_stop), and any other payload's usage stays unknown.
func (g *Gateway) copyEventStream(w http.ResponseWriter, r *http.Request, body io.Reader, owner Owner, op Operation, run *executionRun, status int) {
	reader := bufio.NewReaderSize(body, 64<<10)
	converse := bedrockConverse(op)
	var tail bytes.Buffer
	var latest *UsageObservation
	stopped, exception, events := false, false, 0
	// Anthropic-shaped InvokeModel stream state.
	var merged *UsageObservation
	invokeStarted, invokeStopped, invokeFinal := false, false, false
	observe := func(value UsageObservation) error {
		if run != nil {
			if err := run.observe(value); err != nil {
				return err
			}
			g.emitLegacy(run.ctx, owner, &value)
			return nil
		}
		g.emitLegacy(r.Context(), owner, &value)
		return nil
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
	fail := func(err error) {
		if run != nil {
			_ = run.finish(execution.Result{StatusCode: status, Err: err})
		}
	}
	for {
		message, err := readEventMessage(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fail(err)
			return
		}
		if r.Context().Err() != nil {
			fail(r.Context().Err())
			return
		}
		events++
		kind, event := message.headers[":message-type"], message.headers[":event-type"]
		if kind == "exception" || kind == "error" {
			exception = true
		}
		if converse && kind == "event" {
			if stopped && event != "metadata" {
				fail(fmt.Errorf("native data followed stream completion"))
				return
			}
			switch event {
			case "messageStop":
				stopped = true
			case "metadata":
				var payload map[string]json.RawMessage
				if json.Unmarshal(message.payload, &payload) != nil {
					fail(fmt.Errorf("invalid Bedrock metadata event"))
					return
				}
				if raw := payload["usage"]; raw != nil {
					counters, err := bedrockUsageCounters(raw)
					if err != nil {
						fail(err)
						return
					}
					latest = &UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, Counters: counters, Cumulative: true}
				}
				stopped = true
			}
		}
		if !converse && kind == "event" && event == "chunk" {
			if invokeStarted && invokeStopped {
				fail(fmt.Errorf("native data followed stream completion"))
				return
			}
			var wrapper struct {
				Bytes []byte `json:"bytes"`
			}
			// An id that names a vendor other than Anthropic never reaches
			// this parser (bedrock_invoke_families.go).
			if json.Unmarshal(message.payload, &wrapper) == nil && anthropicShaped(wrapper.Bytes) && invokeRouteOf(r.Context()).anthropic {
				value, err := g.inspectJSON(r.Context(), owner, op, wrapper.Bytes, false, true)
				if err != nil {
					fail(err)
					return
				}
				switch value.eventType {
				case "message_start":
					invokeStarted = value.usage != nil
				case "message_stop":
					invokeStopped = invokeStarted
				case "message_delta":
					if value.nativeTerminal && value.usage != nil {
						_, invokeFinal = value.usage.Counters["output_tokens"]
					}
				}
				if value.usage != nil {
					// Per-field last value wins: message_delta usage is cumulative
					// and replaces message_start counters; never summed.
					if merged == nil {
						merged = &UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, Counters: map[string]int64{}, Cumulative: true}
					}
					for key, n := range value.usage.Counters {
						if prior, ok := merged.Counters[key]; ok && n < prior {
							fail(execution.ErrUsageRegression)
							return
						}
						merged.Counters[key] = n
					}
					if value.usage.ModelVersion != "" {
						merged.ModelVersion = value.usage.ModelVersion
					}
					if value.usage.ResourceID != "" {
						merged.ResourceID = value.usage.ResourceID
					}
					snapshot := *merged
					snapshot.Counters = map[string]int64{}
					for key, n := range merged.Counters {
						snapshot.Counters[key] = n
					}
					if err = observe(snapshot); err != nil {
						fail(err)
						return
					}
				}
			}
		}
		if run != nil && ((converse && stopped) || invokeStopped) {
			if tail.Len()+len(message.raw) > maxEventMessage {
				fail(fmt.Errorf("event stream tail bound exceeded"))
				return
			}
			tail.Write(message.raw)
			continue
		}
		if err = write(message.raw); err != nil {
			fail(err)
			return
		}
	}
	if !converse && invokeStarted {
		// An Anthropic-shaped InvokeModel stream: final only with message_start,
		// a final cumulative message_delta usage report and message_stop.
		if invokeStopped && invokeFinal && merged != nil && !exception {
			final := *merged
			latest = &final
		} else if !exception {
			if run != nil {
				_ = run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Err: fmt.Errorf("native terminal usage unavailable")})
			}
			return
		}
	}
	if run == nil {
		if latest != nil && !exception {
			latest.Terminal = true
			g.emitLegacy(r.Context(), owner, latest)
		}
		return
	}
	if exception {
		_ = run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Err: fmt.Errorf("native stream exception")})
		_ = write(tail.Bytes())
		return
	}
	if converse && !stopped || events == 0 {
		_ = run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Err: fmt.Errorf("native terminal event unavailable")})
		return
	}
	if latest == nil || len(latest.Counters) == 0 {
		// Counter-only policies acknowledge a complete exchange without tokens.
		if err := run.finish(execution.Result{StatusCode: status, ResponseComplete: true}); err == nil {
			_ = write(tail.Bytes())
		}
		return
	}
	latest.Terminal = true
	run.modelIdentity(latest)
	if err := normalizeForRun(op, latest, run); err != nil {
		_ = run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Err: err})
		return
	}
	if err := run.observe(*latest); err != nil {
		_ = run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Err: err})
		return
	}
	if err := run.finish(execution.Result{StatusCode: status, ResponseComplete: true, Terminal: true}); err != nil {
		return
	}
	g.emitLegacy(run.ctx, owner, latest)
	_ = write(tail.Bytes())
}
