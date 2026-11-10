package usageledger

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAccessPointFieldsFilterAndGroup(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	plain := record("t", "plain", base)
	user := record("t", "user", base.Add(time.Second))
	user.AccessPointID, user.CallerKind = "ide", "user"
	client := record("t", "client", base.Add(2*time.Second))
	client.AccessPointID, client.CallerKind, client.ClientID = "ide", "client", "registered-ide"
	for _, r := range []Record{plain, user, client} {
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.List(ctx, "t", Query{Filter: Filter{AccessPointID: "ide", CallerKind: "client"}, Limit: 10})
	if err != nil || len(page.Records) != 1 || page.Records[0].ID != "client" {
		t.Fatalf("filter: %+v %v", page.Records, err)
	}
	groupBy, err := ParseGroupBy("access_point_id,caller_kind")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := Summarize(ctx, s, "t", SummaryQuery{GroupBy: groupBy})
	if err != nil || len(sum.Groups) != 3 {
		t.Fatalf("group_by: %+v %v", sum, err)
	}
	bad := user
	bad.CallerKind = "robot"
	if bad.Validate() == nil {
		t.Fatal("unknown caller kind accepted")
	}
	// Records outside access points keep their previous JSON shape.
	raw, _ := json.Marshal(plain)
	if strings.Contains(string(raw), "access_point_id") || strings.Contains(string(raw), "caller_kind") {
		t.Fatalf("empty access-point fields serialized: %s", raw)
	}
	var old Record
	if err := json.Unmarshal([]byte(`{"id":"x","tenant_id":"t","plane":"llmplex","status":"ok","source":"managed","cost":{"basis":"unknown"}}`), &old); err != nil || old.AccessPointID != "" {
		t.Fatal("previous records no longer decode", err)
	}
}
