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
)

// InputModalityCounters and OutputModalityCounters are the token subsets of
// input_tokens and output_tokens respectively.
var (
	InputModalityCounters  = []string{CounterInputTextTokens, CounterInputAudioTokens, CounterInputImageTokens, CounterInputVideoTokens}
	OutputModalityCounters = []string{CounterOutputTextTokens, CounterOutputAudioTokens, CounterOutputImageTokens}
	// ModalityCounters lists every modality split in wire order.
	ModalityCounters = append(append([]string{}, InputModalityCounters...), OutputModalityCounters...)
	// UnitCounters lists the non-token units in wire order.
	UnitCounters = []string{CounterAudioInputMilliseconds, CounterVoiceSessionMilliseconds, CounterVideoOutputMilliseconds, CounterGeneratedImages}
)

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
	for _, name := range append(append([]string{}, ModalityCounters...), UnitCounters...) {
		if *u.field(name) != nil {
			return true
		}
	}
	return false
}

func (u *UnitUsage) clone() UnitUsage {
	out := UnitUsage{}
	for _, name := range append(append([]string{}, ModalityCounters...), UnitCounters...) {
		if v := *u.field(name); v != nil {
			n := *v
			*out.field(name) = &n
		}
	}
	return out
}

func (u *UnitUsage) fromCounters(counters map[string]int64) {
	for _, name := range append(append([]string{}, ModalityCounters...), UnitCounters...) {
		if v, ok := counters[name]; ok && v >= 0 {
			*u.field(name) = Int64(v)
		}
	}
}

func (u *UnitUsage) validate() error {
	for _, name := range append(append([]string{}, ModalityCounters...), UnitCounters...) {
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
	for _, name := range ModalityCounters {
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
