package agentfederation

import "testing"

// A loopback http JWKS URL passes the shape check; the outbound endpoint
// policy decides reachability (loopback only in development mode). Issuers
// and non-loopback http URLs stay refused.
func TestJWKSURLShapeAllowsOnlyLoopbackHTTP(t *testing.T) {
	for _, ok := range []string{"https://idp.example/keys", "http://127.0.0.1:8443/jwks", "http://[::1]:9/jwks", "http://localhost:9/jwks"} {
		if err := validHTTPSURL(ok, "JWKS URL", false); err != nil {
			t.Fatalf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://idp.example/keys", "http://127.0.0.2.example/keys", "http://10.0.0.1/keys", "ftp://127.0.0.1/keys"} {
		if err := validHTTPSURL(bad, "JWKS URL", false); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	if err := validHTTPSURL("http://127.0.0.1:9", "issuer", true); err == nil {
		t.Fatal("http loopback issuer accepted")
	}
}
