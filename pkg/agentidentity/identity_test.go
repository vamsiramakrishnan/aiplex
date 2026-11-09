package agentidentity

import (
	"context"
	"net/http/httptest"
	"testing"
)

type verifierFunc func(context.Context, string) (map[string]any, error)

func (f verifierFunc) Verify(c context.Context, s string) (map[string]any, error) { return f(c, s) }
func config() Config {
	domain := "agents.global.org-123.system.id.goog"
	return Config{Issuer: "https://sts.googleapis.com/v1/organizations/123/locations/global/workloadIdentityPools/" + domain, Audience: "aiplex", TrustDomain: domain, AllowedSubjects: []string{"spiffe://" + domain + "/resources/aiplatform/projects/123/locations/us-central1/reasoningEngines/a"}, BindingMode: UnboundAllowed}
}
func TestNativeIdentityPinsPoolOwnerAndSubject(t *testing.T) {
	c := config()
	verify := verifierFunc(func(context.Context, string) (map[string]any, error) {
		return map[string]any{"iss": c.Issuer, "aud": c.Audience, "sub": c.AllowedSubjects[0]}, nil
	})
	v, e := New(c, verify)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.VerifyRequest(httptest.NewRequest("GET", "https://service", nil), "eyJ0eXAiOiJKV1QifQ.e30.signature"); e != nil {
		t.Fatal(e)
	}
	wrong := c
	wrong.Issuer = "https://sts.googleapis.com/v1/organizations/999/locations/global/workloadIdentityPools/" + c.TrustDomain
	if _, e := New(wrong, verify); e == nil {
		t.Fatal("trust-domain owner mismatch accepted")
	}
	c.AllowedSubjects[0] += "-changed"
	if _, e = v.Verify(context.Background(), "eyJ0eXAiOiJKV1QifQ.e30.signature"); e == nil {
		t.Fatal("unknown subject accepted")
	}
}
