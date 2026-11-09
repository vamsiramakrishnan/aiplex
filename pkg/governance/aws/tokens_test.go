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

type brokerFunc func(context.Context, TokenRequest) (TokenBinding, error)

func (f brokerFunc) Bind(ctx context.Context, r TokenRequest) (TokenBinding, error) { return f(ctx, r) }

func TestTrustedNativeCredentialExchanges(t *testing.T) {
	cfg := config()
	cfg.defaults()
	var calls []string
	var requests []map[string]any
	accountCalls := 0
	brokerCalls := 0
	d, _ := makeDriver(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("control plane called during token exchange")
		return nil, nil
	})
	d.account = AccountVerifierFunc(func(context.Context, string, string) error {
		accountCalls++
		if brokerCalls <= accountCalls-1 {
			t.Fatal("credential retrieval precedes broker authorization")
		}
		return nil
	})
	c, e := nativeapi.NewClient("https://bedrock-agentcore.us-east-1.amazonaws.com", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		var b map[string]any
		if json.Unmarshal(data, &b) != nil {
			t.Fatal("invalid native exchange JSON")
		}
		requests = append(requests, b)
		return result(`{"workloadAccessToken":"OPAQUE-WORKLOAD","accessToken":"RESOURCE-TOKEN","apiKey":"RESOURCE-API-KEY"}`), nil
	})}, nativeapi.AuthFunc(func(context.Context, *http.Request) error { return nil }))
	if e != nil {
		t.Fatal(e)
	}
	d.cfg.AllowUserIDExchange = true
	d, e = d.WithDataPlane(c, brokerFunc(func(ctx context.Context, r TokenRequest) (TokenBinding, error) {
		brokerCalls++
		b := TokenBinding{TenantID: "tenant-a", PrincipalID: "principal-a", WorkloadARN: r.WorkloadARN, ProviderName: r.ProviderName}
		switch r.Operation {
		case "workloadTokens.forJWT":
			b.Delegated = true
			b.UserJWT = "VERIFIED-IDP-JWT"
		case "workloadTokens.forUserID":
			b.Delegated = true
			b.UserID = "verified-subject"
		case "resourceCredentials.oauth2":
			b.WorkloadIdentityToken = "OPAQUE-USER-AGENT-TOKEN"
			b.Delegated = r.Flow != "M2M"
			if r.SessionHandle != "" {
				if r.SessionHandle != "owned-session" {
					return TokenBinding{}, errors.New("foreign session")
				}
				b.SessionURI = "urn:ietf:params:oauth:request_uri:owned-session"
			} else {
				b.CustomState = strings.Repeat("s", 32)
			}
		case "resourceCredentials.apiKey":
			b.WorkloadIdentityToken = "OPAQUE-USER-AGENT-TOKEN"
			b.Delegated = true
		}
		return b, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	arn := cfg.WorkloadARN("my-agent")
	for _, name := range []string{"workloadTokens.get", "workloadTokens.forJWT", "workloadTokens.forUserID"} {
		if _, e = d.Execute(context.Background(), nativeapi.Operation{Name: name, Resource: arn}); e != nil {
			t.Fatal(e)
		}
	}
	for _, flow := range []string{"M2M", "USER_FEDERATION", "ON_BEHALF_OF_TOKEN_EXCHANGE"} {
		b := resourceCredentialInput{ProviderName: "google-drive", Flow: flow, Scopes: []string{"drive.readonly"}}
		if flow == "USER_FEDERATION" {
			b.ReturnURL = cfg.AllowedReturnURLs[0]
			b.SessionHandle = "owned-session"
		}
		if _, e = d.Execute(context.Background(), nativeapi.Operation{Name: "resourceCredentials.oauth2", Resource: arn, Payload: raw(b)}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = d.Execute(context.Background(), nativeapi.Operation{Name: "resourceCredentials.apiKey", Resource: arn, Payload: raw(resourceCredentialInput{ProviderName: "google-drive"})}); e != nil {
		t.Fatal(e)
	}
	want := []string{"/identities/GetWorkloadAccessToken", "/identities/GetWorkloadAccessTokenForJWT", "/identities/GetWorkloadAccessTokenForUserId", "/identities/oauth2/token", "/identities/oauth2/token", "/identities/oauth2/token", "/identities/api-key"}
	if string(raw(calls)) != string(raw(want)) {
		t.Fatalf("wrong native routes: %v", calls)
	}
	if requests[0]["workloadName"] != "my-agent" || len(requests[0]) != 1 || requests[1]["userToken"] != "VERIFIED-IDP-JWT" || requests[2]["userId"] != "verified-subject" {
		t.Fatal("workload exchanges lost native identity binding")
	}
	if requests[4]["sessionUri"] != "urn:ietf:params:oauth:request_uri:owned-session" || requests[4]["workloadIdentityToken"] != "OPAQUE-USER-AGENT-TOKEN" {
		t.Fatal("resource exchange lost user/agent/session binding")
	}
	if _, ok := requests[4]["sessionHandle"]; ok {
		t.Fatal("host session handle reached upstream")
	}
	if brokerCalls != 7 || accountCalls != 7 {
		t.Fatal("each exchange requires broker and account attestation")
	}
}

func TestCredentialSubstitutionAndSessionOwnershipFailClosed(t *testing.T) {
	cfg := config()
	cfg.defaults()
	nativeCalls := 0
	d, accounts := makeDriver(t, func(*http.Request) (*http.Response, error) { nativeCalls++; return result(`{}`), nil })
	arn := cfg.WorkloadARN("my-agent")
	op := nativeapi.Operation{Name: "workloadTokens.forJWT", Resource: arn}
	if _, e := d.Execute(context.Background(), op); e == nil {
		t.Fatal("missing broker permitted exchange")
	}
	if *accounts != 0 {
		t.Fatal("missing broker triggered account credentials")
	}
	c, e := nativeapi.NewClient("https://bedrock-agentcore.us-east-1.amazonaws.com", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { nativeCalls++; return result(`{}`), nil })}, nativeapi.AuthFunc(func(context.Context, *http.Request) error { return nil }))
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []TokenBinding{{TenantID: "tenant", PrincipalID: "principal", WorkloadARN: cfg.WorkloadARN("other-agent"), Delegated: true, UserJWT: "jwt"}, {TenantID: "tenant", PrincipalID: "principal", WorkloadARN: arn, Delegated: false, UserJWT: "jwt"}, {TenantID: "tenant", PrincipalID: "principal", WorkloadARN: arn, Delegated: true, UserJWT: "jwt", UserID: "sub"}, {TenantID: "tenant", WorkloadARN: arn, Delegated: true, UserJWT: "jwt"}} {
		bound, e := d.WithDataPlane(c, brokerFunc(func(context.Context, TokenRequest) (TokenBinding, error) { return bad, nil }))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = bound.Execute(context.Background(), op); e == nil {
			t.Fatal("unbound identity proof accepted")
		}
	}
	bound, _ := d.WithDataPlane(c, brokerFunc(func(context.Context, TokenRequest) (TokenBinding, error) {
		return TokenBinding{}, errors.New("SECRET from broker")
	}))
	if _, e = bound.Execute(context.Background(), op); e == nil || strings.Contains(e.Error(), "SECRET") {
		t.Fatal("broker failure leaked or succeeded")
	}
	for _, payload := range []map[string]any{{"resourceCredentialProviderName": "google-drive", "workloadIdentityToken": "other-user-token"}, {"resourceCredentialProviderName": "google-drive", "oauth2Flow": "USER_FEDERATION", "scopes": []string{"drive.readonly"}, "resourceOauth2ReturnUrl": cfg.AllowedReturnURLs[0], "sessionUri": "foreign-session"}, {"resourceCredentialProviderName": "foreign-provider"}} {
		if _, e = bound.Execute(context.Background(), nativeapi.Operation{Name: "resourceCredentials.oauth2", Resource: arn, Payload: raw(payload)}); e == nil {
			t.Fatal("raw credential/session/provider substitution accepted")
		}
	}
	if nativeCalls != 0 || *accounts != 0 {
		t.Fatalf("unauthorized exchange reached credentials or network: %d/%d", *accounts, nativeCalls)
	}
}
