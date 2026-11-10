package usageledger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func routedRecord() Record {
	now := time.Now().UTC()
	return Record{ID: "ul_1", TenantID: "t", Plane: PlaneLLM, Status: StatusOK, Source: SourceManaged, StartedAt: now, FinishedAt: now, Cost: UnknownCost(),
		Attempts: []Attempt{{SurfaceRef: "openai-east", NativeModel: "gpt-a", Outcome: "unavailable", UpstreamStatus: 503}, {SurfaceRef: "azure-west", NativeModel: "gpt-b", Outcome: "ok"}},
		Backend:  &BackendRef{SurfaceRef: "azure-west", NativeModel: "gpt-b"}, RoutingStrategy: "priority"}
}

func TestRoutingFieldsValidateAndRedact(t *testing.T) {
	r := routedRecord()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := routedRecord()
	bad.Cache = &CacheInfo{Status: "maybe"}
	if bad.Validate() == nil {
		t.Fatal("unknown cache status")
	}
	clone := r.Clone()
	clone.Attempts[0].Outcome = "changed"
	if r.Attempts[0].Outcome == "changed" {
		t.Fatal("clone shares attempts")
	}
	r.RedactRouting()
	raw, _ := json.Marshal(r)
	for _, name := range []string{"openai-east", "azure-west", "gpt-a", "gpt-b"} {
		if strings.Contains(string(raw), name) {
			t.Fatalf("redacted record names %s: %s", name, raw)
		}
	}
	if r.Attempts[0].SurfaceRef != "backend-1" || r.Backend.SurfaceRef != "backend-2" || r.Attempts[1].Outcome != "ok" {
		t.Fatalf("redaction keeps order and outcomes: %+v %+v", r.Attempts, r.Backend)
	}
}

func TestCachedCost(t *testing.T) {
	c := CachedCost()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if (Cost{Basis: BasisCached, Amount: "1"}).Validate() == nil || (Cost{Basis: BasisCached, Amount: "0", Currency: "USD"}).Validate() == nil {
		t.Fatal("a cached cost is exactly 0 with no currency")
	}
	raw, _ := json.Marshal(c)
	if string(raw) != `{"basis":"cached","amount":"0","scale":0,"amount_decimal":"0"}` {
		t.Fatalf("cached cost JSON: %s", raw)
	}
}
