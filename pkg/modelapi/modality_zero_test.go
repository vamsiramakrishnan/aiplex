package modelapi

import "testing"

// Gemini lists only the modalities that carried tokens (live, 2026-11-10: a
// text-only prompt lists TEXT alone). When the list adds up to the whole
// count, the unlisted standard modalities are reported zeros; when it does not,
// they stay unknown.
func TestGeminiUnlistedModalitiesAreZeroOnlyWhenTheListAddsUp(t *testing.T) {
	s := geminiDASurface("gemini.models.generateContent")("http://127.0.0.1:1")
	whole := inspectFor(t, s, "gemini.models.generateContent", `{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6,"promptTokensDetails":[{"modality":"TEXT","tokenCount":4}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":2}]}}`)
	c := whole.usage.Counters
	for _, name := range []string{"input_audio_tokens", "input_image_tokens", "input_video_tokens", "output_audio_tokens", "output_image_tokens"} {
		if n, ok := c[name]; !ok || n != 0 {
			t.Errorf("%s = %d (reported %v), want a reported zero", name, n, ok)
		}
	}
	if c["input_text_tokens"] != 4 || c["output_text_tokens"] != 2 {
		t.Fatalf("%+v", c)
	}
	partial := inspectFor(t, s, "gemini.models.generateContent", `{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12,"promptTokensDetails":[{"modality":"TEXT","tokenCount":4}]}}`)
	if _, ok := partial.usage.Counters["input_audio_tokens"]; ok {
		t.Fatalf("a list that does not add up to promptTokenCount must leave the rest unknown: %+v", partial.usage.Counters)
	}
}
