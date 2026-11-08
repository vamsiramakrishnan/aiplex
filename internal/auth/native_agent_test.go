package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentidentity"
)

type nativeRoundTrip func(*http.Request) (*http.Response, error)

func (f nativeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNativeGoogleAgentIdentityActualSignatureAndCertificateBinding(t *testing.T) {
	now := time.Now()
	domain := "agents.global.org-123.system.id.goog"
	subject := "spiffe://" + domain + "/resources/container/projects/p/locations/r/clusters/c/ns/n/sa/a"
	issuer := "https://sts.googleapis.com/v1/organizations/123/locations/global/workloadIdentityPools/" + domain
	signing, _ := rsa.GenerateKey(rand.Reader, 2048)
	verifier, e := NewNativeAgentVerifier(agentidentity.Config{Issuer: issuer, Audience: "aiplex", TrustDomain: domain, AllowedSubjects: []string{subject}, BindingMode: agentidentity.RequiredBinding}, JWKSConfig{HTTPClient: &http.Client{Transport: nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != issuer+"/openid/jwks" {
			t.Error(r.URL)
		}
		body := fmt.Sprintf(`{"keys":[{"kid":"test","kty":"RSA","n":"%s","e":"AQAB"}]}`, base64.RawURLEncoding.EncodeToString(signing.N.Bytes()))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	if e != nil {
		t.Fatal(e)
	}
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "agent-test-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	clientKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	uri, _ := url.Parse(subject)
	clientTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "agent"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), URIs: []*url.URL{uri}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	clientDER, _ := x509.CreateCertificate(rand.Reader, clientTemplate, ca, &clientKey.PublicKey, caKey)
	fingerprint := sha256.Sum256(clientDER)
	claims := map[string]any{"iss": issuer, "aud": "aiplex", "sub": subject, "exp": now.Add(time.Minute).Unix(), "cnf": map[string]any{"x5t#S256": base64.RawURLEncoding.EncodeToString(fingerprint[:])}}
	token := signNativeJWT(t, signing, claims)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := verifier.VerifyRequest(r, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if err != nil {
			http.Error(w, err.Error(), 401)
			return
		}
		w.WriteHeader(204)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	server.StartTLS()
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{clientDER, caDER}, PrivateKey: clientKey}}
	client := &http.Client{Transport: transport}
	req, _ := http.NewRequest("GET", server.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	claims["cnf"] = map[string]any{"x5t#S256": "forged"}
	req, _ = http.NewRequest("GET", server.URL, nil)
	req.Header.Set("Authorization", "Bearer "+signNativeJWT(t, signing, claims))
	response, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("mismatched certificate accepted")
	}
	if _, e = verifier.Verify(context.Background(), token); e == nil {
		t.Fatal("bound identity accepted without TLS request")
	}
	payload, _ := json.Marshal(claims)
	if len(payload) == 0 {
		t.Fatal("empty fixture")
	}
}

func signNativeJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	payload, e := json.Marshal(claims)
	if e != nil {
		t.Fatal(e)
	}
	input := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(input))
	signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if e != nil {
		t.Fatal(e)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}
