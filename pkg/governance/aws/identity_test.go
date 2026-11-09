package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func result(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
func config() Config {
	return Config{AccountID: "123456789012", Region: "us-east-1", AllowedWorkloadNames: []string{"my-agent", "other-agent"}, AllowedProviderNames: []string{"google-drive"}, AllowedReturnURLs: []string{"https://console.example/callback"}, AllowedOAuthFlows: []string{"USER_FEDERATION", "M2M", "ON_BEHALF_OF_TOKEN_EXCHANGE"}, AllowedScopes: []string{"drive.readonly"}, AllowNonAtomicMutations: true}
}
func makeDriver(t *testing.T, handler roundTripFunc) (*Driver, *int) {
	t.Helper()
	cfg := config()
	c, e := nativeapi.NewClient("https://bedrock-agentcore-control.us-east-1.amazonaws.com", &http.Client{Transport: handler}, nativeapi.AuthFunc(func(ctx context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "test-sigv4")
		return nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	d, e := New(cfg, c)
	if e != nil {
		t.Fatal(e)
	}
	calls := new(int)
	d, e = d.WithAccountVerifier(AccountVerifierFunc(func(ctx context.Context, account, partition string) error {
		*calls++
		if account != cfg.AccountID || partition != "aws" {
			return errors.New("wrong scope")
		}
		return nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	return d, calls
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func workloadBody(cfg Config, urls []string) string {
	return string(raw(map[string]any{"name": "my-agent", "workloadIdentityArn": cfg.WorkloadARN("my-agent"), "allowedResourceOauth2ReturnUrls": urls, "createdTime": 1, "lastUpdatedTime": 2, "accessToken": "MUST-NOT-LEAK"}))
}

func TestWorkloadLifecycleNativeContracts(t *testing.T) {
	cfg := config()
	cfg.defaults()
	state := workloadBody(cfg, cfg.AllowedReturnURLs)
	var paths []string
	d, accountCalls := makeDriver(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("Authorization") == "" {
			t.Errorf("bad native request: %s %s", r.Method, r.URL.Path)
		}
		paths = append(paths, r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		var b map[string]any
		if json.Unmarshal(data, &b) != nil {
			t.Fatal("not JSON")
		}
		if strings.HasSuffix(r.URL.Path, "ListWorkloadIdentities") {
			if b["maxResults"] != float64(5) || b["nextToken"] != "page-2" {
				t.Errorf("wrong list request %s", data)
			}
			return result(`{"nextToken":"page-3","workloadIdentities":[` + state + `,{"name":"my-agent","workloadIdentityArn":"arn:aws:bedrock-agentcore:us-east-1:000000000000:workload-identity-directory/default/workload-identity/my-agent"},{"name":"foreign-agent","workloadIdentityArn":"foreign"}]}`), nil
		}
		if b["name"] != "my-agent" {
			t.Errorf("native identity body does not pin configured name: %s", data)
		}
		switch r.URL.Path {
		case "/identities/CreateWorkloadIdentity":
			if _, ok := b["tags"]; !ok {
				t.Error("create lost tags")
			}
			return result(state), nil
		case "/identities/GetWorkloadIdentity":
			if len(b) != 1 {
				t.Error("get accepts only name")
			}
			return result(state), nil
		case "/identities/UpdateWorkloadIdentity":
			if _, ok := b["tags"]; ok {
				t.Error("update has illegal tags")
			}
			urls, ok := b["allowedResourceOauth2ReturnUrls"].([]any)
			if !ok || len(urls) != 0 {
				t.Errorf("update must clear allowlist explicitly: %s", data)
			}
			state = workloadBody(cfg, []string{})
			return result(state), nil
		case "/identities/DeleteWorkloadIdentity":
			return &http.Response{StatusCode: 204, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
		return nil, nil
	})
	ctx := context.Background()
	arn := cfg.WorkloadARN("my-agent")
	r, e := d.Execute(ctx, nativeapi.Operation{Name: "workloadIdentities.list", Resource: cfg.DirectoryARN(), Payload: raw(map[string]any{"maxResults": 5, "nextToken": "page-2"})})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(r.Body), "MUST-NOT-LEAK") || strings.Count(string(r.Body), `"name"`) != 1 || !strings.Contains(string(r.Body), "page-3") {
		t.Fatalf("unsafe inventory %s", r.Body)
	}
	_, e = d.Execute(ctx, nativeapi.Operation{Name: "workloadIdentities.create", Resource: arn, Payload: raw(map[string]any{"allowedResourceOauth2ReturnUrls": cfg.AllowedReturnURLs, "tags": map[string]string{"owner": "platform"}})})
	if e != nil {
		t.Fatal(e)
	}
	r, e = d.Execute(ctx, nativeapi.Operation{Name: "workloadIdentities.get", Resource: arn})
	if e != nil || !validFingerprint(r.ETag) || strings.Contains(string(r.Body), "MUST-NOT-LEAK") {
		t.Fatalf("invalid observed workload: %s %v", r.Body, e)
	}
	_, e = d.Execute(ctx, nativeapi.Operation{Name: "workloadIdentities.update", Resource: arn, ETag: r.ETag, Payload: raw(map[string]any{"allowedResourceOauth2ReturnUrls": []string{}})})
	if e != nil {
		t.Fatal(e)
	}
	r, e = d.Execute(ctx, nativeapi.Operation{Name: "workloadIdentities.get", Resource: arn})
	if e != nil {
		t.Fatal(e)
	}
	_, e = d.Execute(ctx, nativeapi.Operation{Name: "workloadIdentities.delete", Resource: arn, ETag: r.ETag})
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"/identities/ListWorkloadIdentities", "/identities/CreateWorkloadIdentity", "/identities/GetWorkloadIdentity", "/identities/GetWorkloadIdentity", "/identities/UpdateWorkloadIdentity", "/identities/GetWorkloadIdentity", "/identities/GetWorkloadIdentity", "/identities/DeleteWorkloadIdentity"}
	if string(raw(paths)) != string(raw(want)) {
		t.Fatalf("wrong API calls %v", paths)
	}
	if *accountCalls != 6 {
		t.Fatalf("every execute must verify account: %d", *accountCalls)
	}
}

func TestScopedValidationBeforeCredentialsOrNetwork(t *testing.T) {
	calls := 0
	d, verified := makeDriver(t, func(r *http.Request) (*http.Response, error) { calls++; return result(`{}`), nil })
	cfg := config()
	cfg.defaults()
	good := cfg.WorkloadARN("my-agent")
	for _, op := range []nativeapi.Operation{{Name: "workloadIdentities.get", Resource: strings.Replace(good, "123456789012", "000000000000", 1)}, {Name: "workloadIdentities.get", Resource: cfg.arn("bedrock-agentcore", "runtime/my-agent")}, {Name: "workloadIdentities.get", Resource: cfg.WorkloadARN("not-allowed")}, {Name: "workloadIdentities.create", Resource: good, Payload: raw(map[string]any{"name": "other-agent"})}, {Name: "workloadIdentities.create", Resource: good, Payload: raw(map[string]any{"userToken": "substitution"})}, {Name: "workloadIdentities.update", Resource: good, ETag: "sha256:x", Payload: raw(map[string]any{"allowedResourceOauth2ReturnUrls": []string{}})}, {Name: "workloadIdentities.update", Resource: good, ETag: "sha256:" + strings.Repeat("a", 64), Payload: raw(map[string]any{})}, {Name: "workloadIdentities.list", Resource: cfg.DirectoryARN(), Payload: raw(map[string]any{"maxResults": 21})}, {Name: "workloadTokens.forJWT", Resource: good, Payload: raw(map[string]any{"userToken": "substitution"})}, {Name: "workloadTokens.forUserID", Resource: good}, {Name: "resourceCredentials.oauth2", Resource: good, Payload: raw(map[string]any{"resourceCredentialProviderName": "google-drive", "oauth2Flow": "M2M", "scopes": []string{"admin"}})}} {
		if d.ValidateOperation(op) == nil {
			t.Errorf("unexpected valid op %+v", op)
		}
		if _, e := d.Execute(context.Background(), op); e == nil {
			t.Errorf("unexpected successful op %+v", op)
		}
	}
	if calls != 0 || *verified != 0 {
		t.Fatalf("invalid operations reached credentials/network: %d/%d", *verified, calls)
	}
	d.account = AccountVerifierFunc(func(context.Context, string, string) error { return errors.New("wrong account SECRET") })
	if _, e := d.Execute(context.Background(), nativeapi.Operation{Name: "workloadIdentities.get", Resource: good}); e == nil || strings.Contains(e.Error(), "SECRET") {
		t.Fatal("account error should fail closed and redact")
	}
	if calls != 0 {
		t.Fatal("wrong-account credentials reached native service")
	}
	d.account = nil
	if _, e := d.Execute(context.Background(), nativeapi.Operation{Name: "workloadIdentities.list", Resource: cfg.DirectoryARN()}); e == nil {
		t.Fatal("missing account verifier allowed inventory")
	}
}

func TestProviderMetadataRedactionAndConfigurationDrift(t *testing.T) {
	cfg := config()
	cfg.defaults()
	providerARN := cfg.arn("acps", "token-vault/default/oauth2credentialprovider/google-drive")
	secret := "HIDDEN-CLIENT-SECRET"
	native := func() string {
		return string(raw(map[string]any{"name": "google-drive", "credentialProviderArn": providerARN, "credentialProviderVendor": "GoogleOauth2", "oauth2ProviderConfigOutput": map[string]any{"googleOauth2ProviderConfig": map[string]any{"clientId": "id", "clientSecret": secret}}, "clientSecretArn": map[string]string{"secretArn": "arn:secret"}, "callbackUrl": "https://callback.example", "status": "READY"}))
	}
	updates := 0
	d, _ := makeDriver(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/identities/GetOauth2CredentialProvider":
			return result(native()), nil
		case "/identities/UpdateOauth2CredentialProvider":
			updates++
			return result(native()), nil
		case "/identities/ListOauth2CredentialProviders":
			return result(`{"credentialProviders":[` + native() + `,{"name":"google-drive","credentialProviderArn":"arn:aws:bedrock-agentcore:us-east-1:000000000000:token-vault/default/oauth2credentialprovider/google-drive"}]}`), nil
		}
		t.Fatalf("unexpected %s", r.URL.Path)
		return nil, nil
	})
	arn := cfg.arn("bedrock-agentcore", "token-vault/default/oauth2credentialprovider/google-drive")
	r, e := d.Execute(context.Background(), nativeapi.Operation{Name: "oauth2CredentialProviders.get", Resource: arn})
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{secret, "clientSecret", "oauth2ProviderConfigOutput", "callbackUrl"} {
		if strings.Contains(string(r.Body), key) {
			t.Fatalf("provider output leaked %s", key)
		}
	}
	l, e := d.Execute(context.Background(), nativeapi.Operation{Name: "oauth2CredentialProviders.list", Resource: cfg.VaultARN()})
	if e != nil || strings.Count(string(l.Body), `"name"`) != 1 || strings.Contains(string(l.Body), secret) {
		t.Fatalf("unsafe provider list %s %v", l.Body, e)
	}
	secret = "ROTATED-SECRET"
	op := nativeapi.Operation{Name: "oauth2CredentialProviders.update", Resource: arn, ETag: r.ETag, Payload: raw(OAuth2ProviderInput{Vendor: "GoogleOauth2", Configuration: map[string]json.RawMessage{"googleOauth2ProviderConfig": raw(map[string]any{"clientId": "id", "clientSecret": "new-secret"})}})}
	if _, e = d.Execute(context.Background(), op); e == nil || updates != 0 {
		t.Fatal("native configuration drift bypassed metadata fingerprint")
	}
	providerARN = strings.Replace(providerARN, "123456789012", "000000000000", 1)
	if _, e = d.Execute(context.Background(), nativeapi.Operation{Name: "oauth2CredentialProviders.get", Resource: arn}); e == nil {
		t.Fatal("foreign provider ARN returned through configured name")
	}
	for _, op := range d.Operations() {
		if strings.Contains(op.Name, "CredentialProviders.create") || strings.Contains(op.Name, "CredentialProviders.update") || strings.HasPrefix(op.Name, "workloadTokens.") || strings.HasPrefix(op.Name, "resourceCredentials.") {
			if !op.Credential {
				t.Errorf("secret-bearing operation %s is public", op.Name)
			}
		}
	}
}
