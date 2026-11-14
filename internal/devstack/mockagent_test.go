package devstack

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The mock keeps only the last few request bodies, however long it runs.
func TestMockAgentKeepsBoundedBodies(t *testing.T) {
	m := &MockAgent{Token: "t"}
	srv := httptest.NewServer(m)
	defer srv.Close()
	for i := 0; i < 50; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/v1/"+MockAgentResource+":query", strings.NewReader(`{"input":{"message":"`+strconv.Itoa(i)+`"}}`))
		req.Header.Set("Authorization", "Bearer t")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if m.Calls() != 50 || len(m.Bodies()) != maxMockAgentBodies || !strings.Contains(m.Bodies()[maxMockAgentBodies-1], `"49"`) {
		t.Fatalf("calls %d, kept %d", m.Calls(), len(m.Bodies()))
	}
	m.RotateToken("other")
	req, _ := http.NewRequest("POST", srv.URL+"/v1/"+MockAgentResource+":query", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("rotated token: %d", resp.StatusCode)
	}
}
