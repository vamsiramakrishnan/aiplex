package usageledger

import "math"

// Non-token units and per-modality token splits.
//
// Token counters (TokenCounters) keep their meaning. The fields below are held
// beside them and are never added into a token counter:
//
//   - Modality token splits are SUBSETS of input_tokens / output_tokens as the
//     provider reports them (OpenAI input_tokens_details / prompt_tokens_details
//     audio/text/image tokens, Gemini promptTokensDetails / candidatesTokensDetails
//     modality entries). They exist because providers price audio, image and
//     text tokens differently. A split the provider did not report is unknown.
//   - Unit counters are other units: milliseconds of input audio billed by
//     duration, milliseconds of a voice session, milliseconds of generated
//     video, and a count of generated images.
//
// Every name is both the normalized execution counter name an adapter emits,
// the ledger usage JSON field and the model-price meter name.
const (
	CounterInputTextTokens   = "input_text_tokens"
	CounterInputAudioTokens  = "input_audio_tokens"
	CounterInputImageTokens  = "input_image_tokens"
	CounterInputVideoTokens  = "input_video_tokens"
	CounterOutputTextTokens  = "output_text_tokens"
	CounterOutputAudioTokens = "output_audio_tokens"
	CounterOutputImageTokens = "output_image_tokens"

	CounterAudioInputMilliseconds   = "audio_input_milliseconds"
	CounterVoiceSessionMilliseconds = "voice_session_milliseconds"
	CounterVideoOutputMilliseconds  = "video_output_milliseconds"
	CounterGeneratedImages          = "generated_images"

	// The per-TTL cache-write counters are SUBSETS of cache_write_tokens;
	// within one provider they do not overlap and sum to it.
	//
	// CounterCacheWrite5m and CounterCacheWrite1h are the five-minute and
	// one-hour writes of Anthropic (usage.cache_creation.ephemeral_5m/1h_input_tokens)
	// and Bedrock (cacheDetails ttl 5m/1h). CounterCacheWrite30m and
	// CounterCacheWrite12h are the OpenAI Responses writes
	// (input_tokens_details.cache_write_tokens_details.ttl_30m/12h_tokens).
	CounterCacheWrite5m  = "cache_write_5m_tokens"
	CounterCacheWrite1h  = "cache_write_1h_tokens"
	CounterCacheWrite30m = "cache_write_30m_tokens"
	CounterCacheWrite12h = "cache_write_12h_tokens"
	// CounterToolUsePrompt is the Gemini toolUsePromptTokenCount: tokens
	// retrieved by built-in tools, reported apart from promptTokenCount. It is
	// NOT part of input_tokens.
	CounterToolUsePrompt = "tool_use_prompt_tokens"
)

// InputModalityCounters and OutputModalityCounters are the token subsets of
// input_tokens and output_tokens respectively.
var (
	InputModalityCounters  = []string{CounterInputTextTokens, CounterInputAudioTokens, CounterInputImageTokens, CounterInputVideoTokens}
	OutputModalityCounters = []string{CounterOutputTextTokens, CounterOutputAudioTokens, CounterOutputImageTokens}
	// ModalityCounters lists every modality split in wire order.
	ModalityCounters = append(append([]string{}, InputModalityCounters...), OutputModalityCounters...)
	// UnitCounters lists the non-token units in wire order.
	UnitCounters = []string{CounterAudioInputMilliseconds, CounterVoiceSessionMilliseconds, CounterVideoOutputMilliseconds, CounterGeneratedImages, CounterToolUsePrompt}
	// CacheSubsetCounters are the per-TTL token subsets of cache_write_tokens.
	CacheSubsetCounters = []string{CounterCacheWrite5m, CounterCacheWrite1h, CounterCacheWrite30m, CounterCacheWrite12h}
	// CacheWriteLongTTLCounters are the subsets the cache_write_tokens rate
	// does not cover: the provider prices them differently from its default
	// TTL (five minutes for Anthropic and Bedrock, 30 minutes for OpenAI), so
	// a positive count needs its own rate (see EstimateWith).
	CacheWriteLongTTLCounters = []string{CounterCacheWrite1h, CounterCacheWrite12h}
)

// heldCounters lists every modality, cache-subset and unit field in wire order.
func heldCounters() []string {
	out := append([]string{}, ModalityCounters...)
	out = append(out, CacheSubsetCounters...)
	return append(out, UnitCounters...)
}

// counterAliases maps normalized execution counter names to ledger names where
// they differ.
var counterAliases = map[string]string{"tool_use_tokens": CounterToolUsePrompt}

// UnitUsage is embedded in Usage. Every field is omitted when unknown, so
// records without these units keep their previous JSON shape.
type UnitUsage struct {
	InputTextTokens   *int64 `json:"input_text_tokens,omitempty"`
	InputAudioTokens  *int64 `json:"input_audio_tokens,omitempty"`
	InputImageTokens  *int64 `json:"input_image_tokens,omitempty"`
	InputVideoTokens  *int64 `json:"input_video_tokens,omitempty"`
	OutputTextTokens  *int64 `json:"output_text_tokens,omitempty"`
	OutputAudioTokens *int64 `json:"output_audio_tokens,omitempty"`
	OutputImageTokens *int64 `json:"output_image_tokens,omitempty"`

	AudioInputMilliseconds   *int64 `json:"audio_input_milliseconds,omitempty"`
	VoiceSessionMilliseconds *int64 `json:"voice_session_milliseconds,omitempty"`
	VideoOutputMilliseconds  *int64 `json:"video_output_milliseconds,omitempty"`
	GeneratedImages          *int64 `json:"generated_images,omitempty"`
	// The CacheWrite*Tokens fields are subsets of cache_write_tokens;
	// ToolUsePromptTokens is a separate quantity (see the counter constants).
	CacheWrite5mTokens  *int64 `json:"cache_write_5m_tokens,omitempty"`
	CacheWrite1hTokens  *int64 `json:"cache_write_1h_tokens,omitempty"`
	CacheWrite30mTokens *int64 `json:"cache_write_30m_tokens,omitempty"`
	CacheWrite12hTokens *int64 `json:"cache_write_12h_tokens,omitempty"`
	ToolUsePromptTokens *int64 `json:"tool_use_prompt_tokens,omitempty"`
}

func (u *UnitUsage) field(name string) **int64 {
	switch name {
	case CounterInputTextTokens:
		return &u.InputTextTokens
	case CounterInputAudioTokens:
		return &u.InputAudioTokens
	case CounterInputImageTokens:
		return &u.InputImageTokens
	case CounterInputVideoTokens:
		return &u.InputVideoTokens
	case CounterOutputTextTokens:
		return &u.OutputTextTokens
	case CounterOutputAudioTokens:
		return &u.OutputAudioTokens
	case CounterOutputImageTokens:
		return &u.OutputImageTokens
	case CounterAudioInputMilliseconds:
		return &u.AudioInputMilliseconds
	case CounterVoiceSessionMilliseconds:
		return &u.VoiceSessionMilliseconds
	case CounterVideoOutputMilliseconds:
		return &u.VideoOutputMilliseconds
	case CounterGeneratedImages:
		return &u.GeneratedImages
	case CounterCacheWrite5m:
		return &u.CacheWrite5mTokens
	case CounterCacheWrite1h:
		return &u.CacheWrite1hTokens
	case CounterCacheWrite30m:
		return &u.CacheWrite30mTokens
	case CounterCacheWrite12h:
		return &u.CacheWrite12hTokens
	case CounterToolUsePrompt:
		return &u.ToolUsePromptTokens
	}
	return nil
}

// UnitCounter returns a modality split or unit counter by name; nil is unknown.
func (u *Usage) UnitCounter(name string) *int64 {
	if u == nil {
		return nil
	}
	if f := u.UnitUsage.field(name); f != nil {
		return *f
	}
	return nil
}

// HeldUnit reports whether the ledger holds this normalized counter name as a
// modality split or unit field.
func HeldUnit(name string) bool {
	var probe UnitUsage
	return probe.field(name) != nil
}

func (u *UnitUsage) known() bool {
	for _, name := range heldCounters() {
		if *u.field(name) != nil {
			return true
		}
	}
	return false
}

func (u *UnitUsage) clone() UnitUsage {
	out := UnitUsage{}
	for _, name := range heldCounters() {
		if v := *u.field(name); v != nil {
			n := *v
			*out.field(name) = &n
		}
	}
	return out
}

func (u *UnitUsage) fromCounters(counters map[string]int64) {
	for _, name := range heldCounters() {
		if v, ok := counters[name]; ok && v >= 0 {
			*u.field(name) = Int64(v)
		}
	}
	for native, name := range counterAliases {
		if v, ok := counters[native]; ok && v >= 0 {
			*u.field(name) = Int64(v)
		}
	}
}

func (u *UnitUsage) validate() error {
	for _, name := range heldCounters() {
		if v := *u.field(name); v != nil && *v < 0 {
			return invalid("usage counter %s is negative", name)
		}
	}
	return nil
}

// addUnits sums one record's known modality and unit counters into a summary
// group. Units never enter the token totals.
func (g *groupAccumulator) addUnits(r Record) error {
	s := &g.group
	for _, name := range append(append([]string{}, ModalityCounters...), CacheSubsetCounters...) {
		v := r.Usage.UnitCounter(name)
		if v == nil {
			continue
		}
		if s.ModalityTokens == nil {
			s.ModalityTokens = map[string]int64{}
		}
		if s.ModalityTokens[name] > math.MaxInt64-*v {
			return invalid("modality token total overflows")
		}
		s.ModalityTokens[name] += *v
	}
	for _, name := range UnitCounters {
		v := r.Usage.UnitCounter(name)
		if v == nil {
			continue
		}
		if s.Units == nil {
			s.Units = map[string]int64{}
			s.UnitRecords = map[string]int64{}
		}
		if s.Units[name] > math.MaxInt64-*v {
			return invalid("unit total overflows")
		}
		s.Units[name] += *v
		s.UnitRecords[name]++
	}
	if r.Batch {
		s.BatchRecords++
	}
	return nil
}
