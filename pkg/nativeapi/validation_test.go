package nativeapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

func TestPublicPayloadRejectsNestedCredentialMaterial(t *testing.T) {
	for _, key := range []string{"Authorization", "client_secret", "accessToken", "refresh-token", "id.token", "registrationAccessToken", "API_KEY", "password", "privateKey", "user_assertion", "workloadIdentityToken", "bearerToken", "subjectToken", "clientAssertion"} {
		raw, _ := json.Marshal(map[string]any{"nested": []any{map[string]any{key: "PRIVATE-CREDENTIAL"}}})
		err := nativeapi.ValidatePublicPayload(raw)
		if err == nil || strings.Contains(err.Error(), "PRIVATE-CREDENTIAL") {
			t.Fatalf("unsafe field %q: %v", key, err)
		}
	}
}

func TestPublicPayloadRejectsAmbiguousAndComplexJSON(t *testing.T) {
	for _, raw := range []string{`{"name":"a","name":"b"}`, `{"displayName":"a","display_name":"b"}`, `{} {}`, `{"name":`, strings.Repeat("[", 34) + `0` + strings.Repeat("]", 34), strings.Repeat(" ", nativeapi.MaxBodyBytes+1)} {
		if err := nativeapi.ValidatePublicPayload(json.RawMessage(raw)); err == nil {
			t.Fatal("unsafe payload accepted")
		}
	}
	for _, raw := range []string{`{"credential_ref":"secrets/managed","api_key_ref":"key-ref","nested":{"displayName":"safe"}}`, `{"enabled":true,"count":1,"items":[null,"safe"]}`, `null`} {
		if err := nativeapi.ValidatePublicPayload(json.RawMessage(raw)); err != nil {
			t.Fatalf("reference-safe payload rejected: %v", err)
		}
	}
	if err := nativeapi.ValidatePublicPayload(nil); err != nil {
		t.Fatal(err)
	}
}
