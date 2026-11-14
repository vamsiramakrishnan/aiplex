package aiplex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GetUsageConfig must tell an absent field from an explicit empty value.
func TestGetUsageConfig_AbsentEmptyAndSet(t *testing.T) {
	bodies := map[string]string{
		"absent": `{}`,
		"empty":  `{"trace_url_template":"","feedback":false,"feedback_comments":false,"tag_keys_allowed":[]}`,
		"set":    `{"trace_url_template":"https://t.example/{id}","feedback":true,"feedback_comments":true,"tag_keys_allowed":["env"]}`,
	}
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/usage/config" {
			t.Errorf("path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	body = bodies["absent"]
	cfg, err := c.GetUsageConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TraceURLTemplate != nil || cfg.Feedback != nil || cfg.FeedbackComments != nil || cfg.TagKeysAllowed != nil {
		t.Fatalf("absent fields must stay nil: %+v", cfg)
	}

	body = bodies["empty"]
	cfg, err = c.GetUsageConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TraceURLTemplate == nil || *cfg.TraceURLTemplate != "" {
		t.Errorf("explicit empty string must be non-nil \"\": %+v", cfg.TraceURLTemplate)
	}
	if cfg.Feedback == nil || *cfg.Feedback || cfg.FeedbackComments == nil || *cfg.FeedbackComments {
		t.Errorf("explicit false must be non-nil false")
	}
	if cfg.TagKeysAllowed == nil || len(cfg.TagKeysAllowed) != 0 {
		t.Errorf("explicit empty list must be a non-nil empty slice: %#v", cfg.TagKeysAllowed)
	}

	body = bodies["set"]
	cfg, err = c.GetUsageConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TraceURLTemplate == nil || *cfg.TraceURLTemplate != "https://t.example/{id}" {
		t.Errorf("trace template: %+v", cfg.TraceURLTemplate)
	}
	if cfg.Feedback == nil || !*cfg.Feedback || cfg.FeedbackComments == nil || !*cfg.FeedbackComments {
		t.Errorf("feedback flags: %+v", cfg)
	}
	if len(cfg.TagKeysAllowed) != 1 || cfg.TagKeysAllowed[0] != "env" {
		t.Errorf("tag keys: %v", cfg.TagKeysAllowed)
	}
}
