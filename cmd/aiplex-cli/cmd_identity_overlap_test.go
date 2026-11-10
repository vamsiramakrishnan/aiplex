package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestIdentityBindAcknowledgeOverlapSendsFlagAndPrintsWarnings(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/agents/identity-presets":     `{"presets":[{"id":"aws-sts-web-identity","display_name":"AWS","cloud":"aws","inputs":[{"name":"issuer","label":"Issuer","required":true},{"name":"role_arn","label":"Role","required":true},{"name":"region","label":"Region"},{"name":"audience","label":"Audience","required":true,"default":"https://aiplex.example.com"}]}],"live_authentication":true}`,
		"PUT /api/v1/agents/agent/identities/aws": `201:{"id":"aws","client_id":"agent","preset":"aws-sts-web-identity","issuer":"https://i","subject":"arn:aws:iam::123456789012:role/r","audience":"https://aiplex.example.com","revision":1,"warnings":["acknowledged overlap: trusts the same issuer as user provider okta"]}`,
	})
	args := []string{"bind", "agent", "aws", "--preset", "aws-sts-web-identity", "--input", "issuer=https://abc.tokens.sts.global.api.aws", "--input", "role_arn=arn:aws:iam::123456789012:role/r", "--input", "region=us-east-1"}
	out, err := runIdentity(t, "", append(args, "--acknowledge-overlap")...)
	if err != nil || !strings.Contains(out, "Warning: acknowledged overlap") {
		t.Fatal(out, err)
	}
	var put spCall
	for _, c := range calls {
		if c.method == http.MethodPut {
			put = c
		}
	}
	var body map[string]any
	if json.Unmarshal([]byte(put.body), &body) != nil || body["acknowledge_overlap"] != true {
		t.Fatalf("request = %s", put.body)
	}
	// Without the flag the field is not sent.
	calls = nil
	if _, err = runIdentity(t, "", args...); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c.method == http.MethodPut && strings.Contains(c.body, "acknowledge_overlap") {
			t.Fatalf("flag sent without --acknowledge-overlap: %s", c.body)
		}
	}
}

func TestIdentityBindHelpExampleUsesRealAWSInputs(t *testing.T) {
	long := agentIdentityBindCmd().Long
	for _, want := range []string{"--input issuer=", "--input role_arn=", "--acknowledge-overlap"} {
		if !strings.Contains(long, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	for _, stale := range []string{"account_id=", "role_name="} {
		if strings.Contains(long, stale) {
			t.Errorf("help still shows %q, which the preset does not take", stale)
		}
	}
}

func TestUserProvidersApplyPassesAcknowledgeOverlapAndPrintsWarnings(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"PUT /api/v1/identity/user-providers/okta": `201:{"id":"okta","preset":"okta","issuer":"https://i","audience":"a","subject_claim":"sub","revision":1,"principal_pool":"p","warnings":["acknowledged overlap: agent binding gh trusts the same issuer"]}`,
	})
	spec := "id: okta\npreset: okta\ninputs: {domain: acme.okta.com}\nacknowledge_overlap: true\n"
	root := func() *cobra.Command { return identityUserCmd() }
	out, err := runSP(t, spec, root(), "user-providers", "apply", "-f", "-")
	if err != nil || !strings.Contains(out, "Warning: acknowledged overlap") {
		t.Fatal(out, err)
	}
	var put spCall
	for _, c := range calls {
		if c.method == http.MethodPut {
			put = c
		}
	}
	var body map[string]any
	if json.Unmarshal([]byte(put.body), &body) != nil || body["acknowledge_overlap"] != true {
		t.Fatalf("request = %s", put.body)
	}
}
