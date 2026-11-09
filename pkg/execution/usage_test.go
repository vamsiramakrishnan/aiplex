package execution

import (
	"errors"
	"fmt"
	"maps"
	"testing"
)

func TestCumulativeUsagePreservesUnknownAndRepeatedCounters(t *testing.T) {
	var usage CumulativeUsage
	input := map[string]int64{"input": 7, "output": 2}
	if err := usage.Observe(input); err != nil {
		t.Fatal(err)
	}
	input["input"] = 99
	for range 2 {
		if err := usage.Observe(map[string]int64{"output": 3}); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]int64{"input": 7, "output": 3}
	snapshot := usage.Snapshot()
	if !maps.Equal(snapshot, want) {
		t.Fatalf("missing or repeated counters changed usage: %v", snapshot)
	}
	snapshot["input"] = 0
	if !maps.Equal(usage.Snapshot(), want) {
		t.Fatal("snapshot mutated producer state")
	}
}

func TestCumulativeUsageRejectsWholeContradictoryUpdate(t *testing.T) {
	for _, update := range []map[string]int64{
		{"input": 8, "output": 1},
		{"input": 8, "output": -1},
		{"input": 8, "": 1},
	} {
		var usage CumulativeUsage
		initial := map[string]int64{"input": 7, "output": 2}
		if err := usage.Observe(initial); err != nil {
			t.Fatal(err)
		}
		err := usage.Observe(update)
		if err == nil || !maps.Equal(usage.Snapshot(), initial) {
			t.Fatalf("contradictory update changed known usage: %v %v", usage.Snapshot(), err)
		}
		if update["output"] == 1 && !errors.Is(err, ErrUsageRegression) {
			t.Fatal("regression has no stable error", err)
		}
	}
}

func TestCumulativeUsageBoundsStateAcrossFrames(t *testing.T) {
	var usage CumulativeUsage
	for i := range 64 {
		if err := usage.Observe(map[string]int64{fmt.Sprint(i): 0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := usage.Observe(map[string]int64{"new": 0}); err == nil || len(usage.Snapshot()) != 64 {
		t.Fatal("stream grew unbounded usage state", err)
	}
}
