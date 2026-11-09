package aws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

func TestAPIKeyProviderLifecycleNativeBodies(t *testing.T) {
	cfg := config()
	cfg.defaults()
	arn := cfg.arn("bedrock-agentcore", "token-vault/default/apikeycredentialprovider/google-drive")
	var paths []string
	d, _ := makeDriver(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(data, &b)
		if b["name"] != "google-drive" {
			t.Fatal("provider name not bound")
		}
		if strings.Contains(r.URL.Path, "Create") || strings.Contains(r.URL.Path, "Update") {
			if b["apiKey"] != "PRIVATE-KEY" || b["apiKeySecretSource"] != "MANAGED" {
				t.Fatalf("bad APIkey native body: fields=%d", len(b))
			}
		}
		if strings.Contains(r.URL.Path, "Delete") {
			return result(`{}`), nil
		}
		return result(string(raw(map[string]any{"name": "google-drive", "credentialProviderArn": arn, "apiKey": "MUST-STRIP", "apiKeySecretArn": map[string]string{"secretArn": "sensitive-reference"}}))), nil
	})
	ctx := context.Background()
	payload := raw(APIKeyProviderInput{APIKey: "PRIVATE-KEY"})
	for _, action := range []string{"create", "update", "delete"} {
		etag := ""
		if action != "create" {
			r, e := d.Execute(ctx, nativeapi.Operation{Name: "apiKeyCredentialProviders.get", Resource: arn})
			if e != nil {
				t.Fatal(e)
			}
			etag = r.ETag
			if strings.Contains(string(r.Body), "MUST-STRIP") || strings.Contains(string(r.Body), "apiKeySecret") {
				t.Fatal("API key metadata leaked credential")
			}
		}
		p := payload
		if action == "delete" {
			p = nil
		}
		r, e := d.Execute(ctx, nativeapi.Operation{Name: "apiKeyCredentialProviders." + action, Resource: arn, Payload: p, ETag: etag})
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(r.Body), "MUST-STRIP") {
			t.Fatal("provider mutation response leaked API key")
		}
	}
	if len(paths) != 7 || paths[0] != "/identities/CreateApiKeyCredentialProvider" || paths[3] != "/identities/UpdateApiKeyCredentialProvider" || paths[6] != "/identities/DeleteApiKeyCredentialProvider" {
		t.Fatalf("wrong APIkey lifecycle calls %v", paths)
	}
	for _, p := range []APIKeyProviderInput{{APIKey: "plaintext", APIKeySecretSource: "EXTERNAL", APIKeySecretConfig: &SecretReference{SecretID: cfg.arn("secretsmanager", "secret:key")}}, {APIKeySecretSource: "EXTERNAL", APIKeySecretConfig: &SecretReference{SecretID: "arn:aws:secretsmanager:us-east-1:000000000000:secret:other"}}} {
		if e := d.ValidateOperation(nativeapi.Operation{Name: "apiKeyCredentialProviders.create", Resource: arn, Payload: raw(p)}); e == nil {
			t.Fatal("secret substitution accepted")
		}
	}
}

func TestTokenVaultReadAndCMKNativeContracts(t *testing.T) {
	cfg := config()
	cfg.defaults()
	key := cfg.arn("kms", "key/11111111-2222-3333-4444-555555555555")
	state := KMSConfiguration{KeyType: "ServiceManagedKey"}
	var paths []string
	d, _ := makeDriver(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		var b map[string]json.RawMessage
		_ = json.Unmarshal(data, &b)
		if string(b["tokenVaultId"]) != `"default"` {
			t.Fatal("native vault ID not pinned")
		}
		switch r.URL.Path {
		case "/identities/set-token-vault-cmk":
			_ = json.Unmarshal(b["kmsConfiguration"], &state)
		case "/identities/get-token-vault":
			if len(b) != 1 {
				t.Fatal("invalid native vault get")
			}
		default:
			t.Fatal("unexpected vault API")
		}
		return result(string(raw(map[string]any{"tokenVaultId": "default", "kmsConfiguration": state, "lastModifiedDate": 10, "accessToken": "MUST-STRIP"}))), nil
	})
	d.cfg.AllowedKMSKeyARNs = []string{key}
	r, e := d.Execute(context.Background(), nativeapi.Operation{Name: "tokenVaults.get", Resource: cfg.VaultARN()})
	if e != nil || strings.Contains(string(r.Body), "MUST-STRIP") {
		t.Fatal("unsafe vault observation")
	}
	r, e = d.Execute(context.Background(), nativeapi.Operation{Name: "tokenVaults.setCMK", Resource: cfg.VaultARN(), ETag: r.ETag, Payload: raw(vaultInput{KMSConfiguration: KMSConfiguration{KeyType: "CustomerManagedKey", KMSKeyARN: key}})})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(r.Body), key) || state.KeyType != "CustomerManagedKey" || len(paths) != 3 || paths[1] != "/identities/get-token-vault" || paths[2] != "/identities/set-token-vault-cmk" {
		t.Fatalf("wrong KMS native contract %s %v", r.Body, paths)
	}
	if e := d.ValidateOperation(nativeapi.Operation{Name: "tokenVaults.setCMK", Resource: cfg.VaultARN(), ETag: r.ETag, Payload: raw(vaultInput{KMSConfiguration: KMSConfiguration{KeyType: "CustomerManagedKey", KMSKeyARN: strings.Replace(key, "123456789012", "000000000000", 1)}})}); e == nil {
		t.Fatal("foreign KMS key accepted")
	}
}

func TestOAuthProviderLifecycleUsesNativeUnionAndPaths(t *testing.T) {
	cfg := config()
	cfg.defaults()
	arn := cfg.arn("bedrock-agentcore", "token-vault/default/oauth2credentialprovider/google-drive")
	var paths []string
	d, _ := makeDriver(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		var b map[string]json.RawMessage
		_ = json.Unmarshal(data, &b)
		if string(b["name"]) != `"google-drive"` {
			t.Fatal("OAuth native name is not pinned")
		}
		if strings.HasSuffix(r.URL.Path, "CreateOauth2CredentialProvider") || strings.HasSuffix(r.URL.Path, "UpdateOauth2CredentialProvider") {
			if string(b["credentialProviderVendor"]) != `"GoogleOauth2"` || !strings.Contains(string(b["oauth2ProviderConfigInput"]), "googleOauth2ProviderConfig") {
				t.Fatal("OAuth native union not preserved")
			}
		}
		if strings.HasSuffix(r.URL.Path, "DeleteOauth2CredentialProvider") {
			return &http.Response{StatusCode: 204, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return result(string(raw(map[string]any{"name": "google-drive", "credentialProviderArn": arn, "credentialProviderVendor": "GoogleOauth2", "status": "READY"}))), nil
	})
	payload := raw(OAuth2ProviderInput{Vendor: "GoogleOauth2", Configuration: map[string]json.RawMessage{"googleOauth2ProviderConfig": raw(map[string]any{"clientId": "id", "clientSecret": "confidential"})}})
	for _, action := range []string{"create", "update", "delete"} {
		tag := ""
		p := payload
		if action != "create" {
			r, e := d.Execute(context.Background(), nativeapi.Operation{Name: "oauth2CredentialProviders.get", Resource: arn})
			if e != nil {
				t.Fatal(e)
			}
			tag = r.ETag
		}
		if action == "delete" {
			p = nil
		}
		if _, e := d.Execute(context.Background(), nativeapi.Operation{Name: "oauth2CredentialProviders." + action, Resource: arn, Payload: p, ETag: tag}); e != nil {
			t.Fatal(e)
		}
	}
	if len(paths) != 7 || paths[0] != "/identities/CreateOauth2CredentialProvider" || paths[3] != "/identities/UpdateOauth2CredentialProvider" || paths[6] != "/identities/DeleteOauth2CredentialProvider" {
		t.Fatalf("wrong OAuth lifecycle native calls %v", paths)
	}
}
