package modelapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Normalized non-token counters. The names equal the usage ledger fields and
// model-price meters (pkg/usageledger/units.go); they are never folded into
// a token counter.
const (
	CounterInputTextTokens          = "input_text_tokens"
	CounterInputAudioTokens         = "input_audio_tokens"
	CounterInputImageTokens         = "input_image_tokens"
	CounterInputVideoTokens         = "input_video_tokens"
	CounterOutputTextTokens         = "output_text_tokens"
	CounterOutputAudioTokens        = "output_audio_tokens"
	CounterOutputImageTokens        = "output_image_tokens"
	CounterAudioInputMilliseconds   = "audio_input_milliseconds"
	CounterVoiceSessionMilliseconds = "voice_session_milliseconds"
	CounterVideoOutputMilliseconds  = "video_output_milliseconds"
	CounterGeneratedImages          = "generated_images"
)

// Observation dimension keys.
const (
	DimensionBatch        = "batch"
	DimensionImageSize    = "image_size"
	DimensionImageQuality = "image_quality"
)

func cloneDimensions(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// secondsToMilliseconds converts a provider-reported (possibly fractional)
// number of seconds, rounding up so no billed fraction is lost.
func secondsToMilliseconds(raw json.RawMessage) (int64, error) {
	var seconds float64
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &seconds) != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 1e9 {
		return 0, fmt.Errorf("invalid provider duration")
	}
	return int64(math.Ceil(seconds*1000 - 1e-6)), nil
}

func nonNegativeCounter(raw json.RawMessage) (int64, bool, error) {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false, nil
	}
	var n int64
	if json.Unmarshal(raw, &n) != nil || n < 0 || n > 1<<53 {
		return 0, false, fmt.Errorf("invalid provider usage counter")
	}
	return n, true, nil
}

// modalityCounters reads per-modality token splits from a native usage object
// and adds them to counters. Splits are subsets of input/output tokens; a
// split that is not reported stays unknown. Supported shapes:
//
//   - OpenAI input_tokens_details / input_token_details / prompt_tokens_details
//     {text_tokens, audio_tokens, image_tokens} and output_tokens_details /
//     output_token_details / completion_tokens_details {text_tokens,
//     audio_tokens, image_tokens}
//     (https://developers.openai.com/api/reference/resources/images/methods/generate,
//     .../audio/subresources/transcriptions/methods/create,
//     .../chat/subresources/completions/methods/create)
//   - Gemini promptTokensDetails / candidatesTokensDetails /
//     responseTokensDetails [{modality, tokenCount}] (ModalityTokenCount,
//     https://aistudio.google.com/api-reference/generate-content and
//     https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/GenerateContentResponse)
//
// A conflicting duplicate fails the observation; counters already present
// with the same value are accepted.
func modalityCounters(usage map[string]json.RawMessage, counters map[string]int64) error {
	set := func(name string, n int64) error {
		if prior, ok := counters[name]; ok && prior != n {
			return fmt.Errorf("conflicting modality usage")
		}
		counters[name] = n
		return nil
	}
	for parent, prefix := range map[string]string{"input_tokens_details": "input", "input_token_details": "input", "prompt_tokens_details": "input", "output_tokens_details": "output", "output_token_details": "output", "completion_tokens_details": "output"} {
		raw := usage[parent]
		if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var detail map[string]json.RawMessage
		if json.Unmarshal(raw, &detail) != nil {
			return fmt.Errorf("invalid usage details")
		}
		for _, modality := range []string{"text", "audio", "image"} {
			n, known, err := nonNegativeCounter(detail[modality+"_tokens"])
			if err != nil {
				return err
			}
			if !known {
				continue
			}
			name := prefix + "_" + modality + "_tokens"
			if name == CounterOutputImageTokens || name == CounterInputImageTokens || name == CounterInputTextTokens || name == CounterInputAudioTokens || name == CounterOutputTextTokens || name == CounterOutputAudioTokens {
				if err := set(name, n); err != nil {
					return err
				}
			}
		}
	}
	for parent, prefix := range map[string]string{"promptTokensDetails": "input", "candidatesTokensDetails": "output", "responseTokensDetails": "output"} {
		raw := usage[parent]
		if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var entries []struct {
			Modality   string          `json:"modality"`
			TokenCount json.RawMessage `json:"tokenCount"`
		}
		if json.Unmarshal(raw, &entries) != nil {
			return fmt.Errorf("invalid modality token details")
		}
		sums := map[string]int64{}
		for _, entry := range entries {
			n, known, err := nonNegativeCounter(entry.TokenCount)
			if err != nil {
				return err
			}
			if !known {
				// proto3 omits a zero tokenCount; the entry still names the modality.
				n = 0
			}
			modality := strings.ToLower(entry.Modality)
			name := prefix + "_" + modality + "_tokens"
			switch name {
			case CounterInputTextTokens, CounterInputAudioTokens, CounterInputImageTokens, CounterInputVideoTokens, CounterOutputTextTokens, CounterOutputAudioTokens, CounterOutputImageTokens:
				sums[name] += n
			}
		}
		for name, n := range sums {
			if err := set(name, n); err != nil {
				return err
			}
		}
	}
	return nil
}
