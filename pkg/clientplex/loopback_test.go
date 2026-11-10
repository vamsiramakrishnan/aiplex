package clientplex

import "testing"

func TestDestinationEndpointAllowsOnlyLoopbackHTTP(t *testing.T) {
	for _, ok := range []string{"https://gw.example.com/ap/x", "http://127.0.0.1:8080/ap/local", "http://localhost:8080/ap/local", "http://[::1]:8080/ap/local"} {
		if _, err := destinationEndpoint(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://gw.example.com/ap/x", "http://10.0.0.5:8080/ap", "http://127.0.0.1:8080/ap?x=1", "http://user:pw@127.0.0.1/ap", "ftp://127.0.0.1/ap"} {
		if _, err := destinationEndpoint(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// Claude gateway servers keep requiring HTTPS, loopback or not.
	if _, err := secureEndpoint("http://127.0.0.1:8080"); err == nil {
		t.Error("secureEndpoint accepted http")
	}
}
