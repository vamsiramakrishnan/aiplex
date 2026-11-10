package modelapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

func mediaGateway(t *testing.T, ids []string, models []string, upstream http.HandlerFunc, mutate func(*Options)) (*Gateway, *collectingLease) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	s := providerSurfaceFor(Provider{ID: ProviderOpenAI}, ProtocolOpenAIAudio, models, ids...)(server.URL)
	opts := options(t, server.URL, newOwnership())
	opts.LiveDialer, opts.TokenSource = nil, nil
	opts.Credential = CredentialFunc(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer surface-key")
		return nil
	})
	lease := &collectingLease{profile: "request-count"}
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	if mutate != nil {
		mutate(&opts)
	}
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	return g, lease
}

func TestTranscriptionDurationIsObservedAsMilliseconds(t *testing.T) {
	var gotAuth string
	g, lease := mediaGateway(t, []string{"openai.v1.audio-transcriptions"}, []string{"fixture-asr"}, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"x","usage":{"type":"duration","seconds":2.0004}}`)
	}, nil)
	body, contentType := fixtureForm(map[string]string{"model": "fixture-asr"})
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Authorization", "Bearer forged-client-token")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || gotAuth != "Bearer surface-key" {
		t.Fatalf("%d auth=%q", w.Code, gotAuth)
	}
	terminal := lease.terminalObservations()
	if len(terminal) != 1 || terminal[0].Counters[CounterAudioInputMilliseconds] != 2001 || len(terminal[0].Counters) != 1 {
		t.Fatalf("duration observation: %+v", terminal)
	}
}

func TestTranscriptionModelAndUploadBoundsAreChecked(t *testing.T) {
	calls := 0
	g, _ := mediaGateway(t, []string{"openai.v1.audio-transcriptions"}, []string{"fixture-asr"}, func(w http.ResponseWriter, r *http.Request) { calls++ }, func(o *Options) { o.MaxUploadBytes = 1024 })
	for _, test := range []struct {
		name   string
		fields map[string]string
		pad    int
		want   int
	}{
		{"model not allowed", map[string]string{"model": "other"}, 0, 403},
		{"no model", map[string]string{}, 0, 403},
		{"too large", map[string]string{"model": "fixture-asr"}, 4096, 413},
	} {
		body, contentType := fixtureForm(test.fields)
		body = strings.Replace(body, "RIFF-synthetic-audio", strings.Repeat("A", test.pad+1), 1)
		r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%s: %d %s", test.name, w.Code, w.Body)
		}
	}
	if calls != 0 {
		t.Fatal("rejected upload reached the provider")
	}
}

// With n=2 the image count is observed but per-event token usage is not
// summed (per-image vs cumulative semantics are unconfirmed); the terminal
// frames are held until acknowledged.
func TestImageStreamCountsCompletedImages(t *testing.T) {
	stream := "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"A\"}\n\n" +
		"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"B\",\"size\":\"1024x1024\",\"quality\":\"low\",\"usage\":{\"input_tokens\":5,\"output_tokens\":10,\"total_tokens\":15}}\n\n" +
		"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"C\",\"size\":\"1024x1024\",\"quality\":\"low\",\"usage\":{\"input_tokens\":5,\"output_tokens\":10,\"total_tokens\":15}}\n\n"
	g, lease := mediaGateway(t, []string{"openai.v1.images-generations"}, []string{"fixture-image"}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	}, nil)
	w := serve(t, g, "POST", "/v1/images/generations", `{"model":"fixture-image","prompt":"x","n":2,"stream":true}`, "")
	if w.Code != 200 || w.Body.String() != stream {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	terminal := lease.terminalObservations()
	if len(terminal) != 1 || terminal[0].Counters[CounterGeneratedImages] != 2 || terminal[0].Dimensions[DimensionImageSize] != "1024x1024" {
		t.Fatalf("image stream observation: %+v", terminal)
	}
	if _, known := terminal[0].Counters["output_tokens"]; known {
		t.Fatal("per-event token usage was summed or picked")
	}
}

// Speech audio is relayed as it arrives, without buffering, and counted as
// one exchange.
func TestSpeechAudioIsStreamedThrough(t *testing.T) {
	audio := bytes.Repeat([]byte{1, 2, 3, 4}, 64<<10)
	g, lease := mediaGateway(t, []string{"openai.v1.audio-speech"}, []string{"fixture-tts"}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.Copy(w, bytes.NewReader(audio))
	}, func(o *Options) { o.MaxBodyBytes = 1024 })
	w := serve(t, g, "POST", "/v1/audio/speech", `{"model":"fixture-tts","input":"hi","voice":"alloy"}`, "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), audio) {
		t.Fatalf("%d len=%d", w.Code, w.Body.Len())
	}
	if len(lease.results) != 1 || !lease.results[0].ResponseComplete || len(lease.terminalObservations()) != 0 {
		t.Fatalf("speech exchange: %+v", lease.results)
	}
}
