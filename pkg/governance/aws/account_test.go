package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
)

func TestSTSAccountAttestationSignsActualPinnedRequest(t *testing.T) {
	provider := sdkaws.CredentialsProviderFunc(func(context.Context) (sdkaws.Credentials, error) {
		return sdkaws.Credentials{AccessKeyID: "TESTACCESSKEY", SecretAccessKey: "test-secret", SessionToken: "session-token"}, nil
	})
	count := 0
	account := "123456789012"
	arn := "arn:aws:sts::123456789012:assumed-role/AgentGovernance/session"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count++
		if r.URL.String() != "https://sts.us-east-1.amazonaws.com" || r.Method != "POST" {
			t.Fatalf("STS target unpinned %s %s", r.Method, r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "Action=GetCallerIdentity&Version=2011-06-15" {
			t.Fatalf("wrong STS contract %s", body)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=TESTACCESSKEY/") || !strings.Contains(auth, "/us-east-1/sts/aws4_request") || !strings.Contains(auth, "Signature=") || r.Header.Get("X-Amz-Security-Token") != "session-token" {
			t.Fatalf("STS request was not SigV4 signed")
		}
		return result(`<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>` + arn + `</Arn><UserId>id</UserId><Account>` + account + `</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`), nil
	})}
	v, e := NewSTSAccountVerifier(client, provider, "us-east-1")
	if e != nil {
		t.Fatal(e)
	}
	if e = v.VerifyAccount(context.Background(), "123456789012", "aws"); e != nil {
		t.Fatal(e)
	}
	account = "000000000000"
	if e = v.VerifyAccount(context.Background(), "123456789012", "aws"); e == nil {
		t.Fatal("wrong AWS account passed attestation")
	}
	account = "123456789012"
	arn = "arn:aws-us-gov:sts::123456789012:assumed-role/AgentGovernance/session"
	if e = v.VerifyAccount(context.Background(), "123456789012", "aws"); e == nil {
		t.Fatal("wrong AWS partition passed attestation")
	}
	if e = v.VerifyAccount(context.Background(), "123456789012", "aws-cn"); e == nil || count != 3 {
		t.Fatal("partition mismatch should reject before STS")
	}
}
func TestSensitiveBindingsRedactLoggingAndJSON(t *testing.T) {
	b := TokenBinding{UserJWT: "sensitive-jwt", WorkloadIdentityToken: "sensitive-token", UserID: "private-subject"}
	data, e := json.Marshal(b)
	if e != nil || string(data) != "{}" || strings.Contains(b.String(), "sensitive") {
		t.Fatal("token proof was printable")
	}
	r := &CredentialResult{accessToken: "secret-access", apiKey: "secret-key", workloadToken: "secret-workload", authorizationURL: "secret-url", sessionURI: "secret-session"}
	data, e = json.Marshal(r)
	printed := fmt.Sprintf("%v %#v %v %#v %v %#v", r, r, *r, *r, b, b)
	if e != nil || string(data) != `{"redacted":true}` || strings.Contains(printed, "secret-") || strings.Contains(printed, "sensitive-") {
		t.Fatal("credential result leaked during JSON or formatting")
	}
	if r.AccessToken() != "secret-access" || r.APIKey() != "secret-key" || r.WorkloadAccessToken() != "secret-workload" {
		t.Fatal("explicit transport injection lost credential")
	}
}
