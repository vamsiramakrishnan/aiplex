package toolauth

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

// A development API with an outbound policy that opts into loopback and an
// http://127.0.0.1 public URL used to refuse to start. Loopback HTTP must be
// accepted only with that opt-in.
func TestMetadataHandlerLoopbackOriginRequiresOptIn(t *testing.T) {
	loopback, err := NewService(Config{Network: endpointpolicy.Config{AllowedOrigins: []string{"http://127.0.0.1:9"}, AllowLoopback: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = loopback.MetadataHandler("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("loopback opt-in refused loopback public origin: %v", err)
	}
	strict, err := NewService(Config{Network: endpointpolicy.Config{AllowedOrigins: []string{"https://tools.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://127.0.0.1:8080", "http://example.com"} {
		if _, err = strict.MetadataHandler(origin); err == nil {
			t.Fatalf("HTTP public origin %s accepted without loopback opt-in", origin)
		}
	}
	if _, err = loopback.MetadataHandler("http://example.com"); err == nil {
		t.Fatal("non-loopback HTTP public origin accepted")
	}
}
