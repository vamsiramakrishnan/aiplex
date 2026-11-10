package usageledger

import (
	"context"
	"testing"
	"time"
)

func TestFilterBatchErrorClassAndUsageBound(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	plain := record("t", "plain", base)
	batch := record("t", "batch", base.Add(time.Second))
	batch.Batch = true
	failed := record("t", "failed", base.Add(2*time.Second))
	failed.Status, failed.ErrorClass = StatusError, "rate_limited"
	bounded := record("t", "bounded", base.Add(3*time.Second))
	bounded.UsageBound = UsageLowerBound
	for _, r := range []Record{plain, batch, failed, bounded} {
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	yes, no := true, false
	ids := func(f Filter) string {
		page, err := s.List(ctx, "t", Query{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		out := ""
		for _, r := range page.Records {
			out += r.ID + ","
		}
		return out
	}
	for name, c := range map[string]struct {
		f    Filter
		want string
	}{
		"any":         {Filter{}, "bounded,failed,batch,plain,"},
		"batch":       {Filter{Batch: &yes}, "batch,"},
		"not batch":   {Filter{Batch: &no}, "bounded,failed,plain,"},
		"error class": {Filter{ErrorClass: "rate_limited"}, "failed,"},
		"bound":       {Filter{UsageBound: UsageLowerBound}, "bounded,"},
		"bound none":  {Filter{UsageBound: UsageBoundNone}, "failed,batch,plain,"},
		"other bound": {Filter{UsageBound: UsagePartial}, ""},
	} {
		if got := ids(c.f); got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}
}

func TestEveryFilterDimensionIsGroupable(t *testing.T) {
	groupable := map[string]bool{}
	for _, f := range GroupByFields {
		groupable[f] = true
	}
	for _, f := range []string{"plane", "provider", "model_id", "tool_name", "actor_id", "principal_id", "client_id", "access_point_id", "caller_kind", "status", "source", "error_class", "usage_bound", "batch"} {
		if !groupable[f] {
			t.Errorf("filterable dimension %s is not in GroupByFields", f)
		}
	}
	ctx := context.Background()
	s := NewMemoryStore()
	a := record("t", "a", base)
	b := record("t", "b", base.Add(time.Second))
	b.Batch, b.Source, b.ErrorClass, b.UsageBound = true, SourceReported, "timeout", UsagePartial
	for _, r := range []Record{a, b} {
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	by, err := ParseGroupBy("source,error_class,usage_bound,batch")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := Summarize(ctx, s, "t", SummaryQuery{GroupBy: by})
	if err != nil || len(sum.Groups) != 2 {
		t.Fatalf("%+v %v", sum, err)
	}
	// Groups sort by key values: managed (a) before reported (b).
	if g := sum.Groups[0]; g.Key["batch"] != "false" || g.Key["usage_bound"] != "" || g.Key["error_class"] != "" {
		t.Fatalf("absent dimensions group as empty/false: %+v", g.Key)
	}
	if g := sum.Groups[1]; g.Key["batch"] != "true" || g.Key["source"] != SourceReported || g.Key["error_class"] != "timeout" {
		t.Fatalf("%+v", g.Key)
	}
}
