package google

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
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentidentity"
)

type signedClaims struct{ key *rsa.PublicKey }

func (s signedClaims) Verify(_ context.Context, token string) (map[string]any, error) {
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return nil, errors.New("invalid token")
	}
	signature, e := base64.RawURLEncoding.DecodeString(p[2])
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256([]byte(p[0] + "." + p[1]))
	if e = rsa.VerifyPKCS1v15(s.key, crypto.SHA256, sum[:], signature); e != nil {
		return nil, e
	}
	body, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return nil, e
	}
	var claims map[string]any
	if e = json.Unmarshal(body, &claims); e != nil {
		return nil, e
	}
	exp, _ := claims["exp"].(float64)
	if exp <= float64(time.Now().Unix()) {
		return nil, errors.New("expired")
	}
	return claims, nil
}
func mint(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body, _ := json.Marshal(claims)
	payload := head + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(payload))
	sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if e != nil {
		t.Fatal(e)
	}
	return payload + "." + base64.RawURLEncoding.EncodeToString(sig)
}
func clientCertificates(t *testing.T, subject string) (*x509.CertPool, *tls.Certificate, *tls.Certificate) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "native-test-root"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	uri, _ := url.Parse(subject)
	issue := func(serial int64) *tls.Certificate {
		leafKey, e := rsa.GenerateKey(rand.Reader, 2048)
		if e != nil {
			t.Fatal(e)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "native-agent"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), URIs: []*url.URL{uri}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
		b, e := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		return &tls.Certificate{Certificate: [][]byte{b, der}, PrivateKey: leafKey}
	}
	return roots, issue(2), issue(3)
}
func TestNativeMTLSBoundJWTAndRotation(t *testing.T) {
	const domain = "agents.global.org-123.system.id.goog"
	const subject = "spiffe://" + domain + "/resources/aiplatform/projects/456/locations/us-central1/reasoningEngines/one"
	issuer := "https://sts.googleapis.com/v1/organizations/123/locations/global/workloadIdentityPools/" + domain
	roots, first, second := clientCertificates(t, subject)
	signer, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var seenMu sync.Mutex
	var serials []int64
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("missing verified native client certificate")
		}
		serial := r.TLS.PeerCertificates[0].SerialNumber.Int64()
		seenMu.Lock()
		serials = append(serials, serial)
		seenMu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing native JWT")
		}
		io.WriteString(w, `{}`)
	}))
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	s.StartTLS()
	defer s.Close()
	identity := agentidentity.Config{Issuer: issuer, Audience: "https://receiver.example", TrustDomain: domain, AllowedSubjects: []string{subject}, BindingMode: agentidentity.RequiredBinding}
	var current atomic.Pointer[tls.Certificate]
	current.Store(first)
	var acquisitions atomic.Int32
	wrongAudience := atomic.Bool{}
	provider := NativeTokenFunc(func(_ context.Context, audience string, cert *tls.Certificate) (string, error) {
		acquisitions.Add(1)
		if audience != identity.Audience {
			t.Error("token acquisition audience mismatch")
		}
		sum := sha256.Sum256(cert.Certificate[0])
		if wrongAudience.Load() {
			audience = "https://foreign.example"
		}
		return mint(t, signer, map[string]any{"iss": issuer, "sub": subject, "aud": audience, "exp": time.Now().Add(time.Minute).Unix(), "cnf": map[string]any{"x5t#S256": base64.RawURLEncoding.EncodeToString(sum[:])}}), nil
	})
	transport := s.Client().Transport.(*http.Transport).Clone()
	client, e := NewNativeClient(NativeConfig{Origin: s.URL, Identity: identity, ClientRoots: roots, Transport: transport}, provider, CertificateFunc(func(context.Context) (*tls.Certificate, error) { return current.Load(), nil }), signedClaims{&signer.PublicKey})
	if e != nil {
		t.Fatal(e)
	}
	defer client.CloseIdleConnections()
	for _, cert := range []*tls.Certificate{first, second} {
		current.Store(cert)
		r, e := client.Get(s.URL + "/invoke")
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
	}
	seenMu.Lock()
	if len(serials) != 2 || serials[0] != 2 || serials[1] != 3 {
		t.Fatalf("certificate rotation reused wrong TLS credential: %v", serials)
	}
	seenMu.Unlock()
	wrongAudience.Store(true)
	if _, e = client.Get(s.URL + "/invoke"); e == nil {
		t.Fatal("foreign audience token reached network")
	}
	seenMu.Lock()
	n := len(serials)
	seenMu.Unlock()
	if n != 2 {
		t.Fatal("invalid bound token reached receiver")
	}
	before := acquisitions.Load()
	if _, e = client.Get("https://foreign.example/invoke"); e == nil || acquisitions.Load() != before {
		t.Fatal("foreign destination acquired credentials")
	}
}
func TestNativeTransportRequiresExplicitBoundConfiguration(t *testing.T) {
	if _, e := NewNativeClient(NativeConfig{Origin: "https://example.com"}, nil, nil, nil); e == nil {
		t.Fatal("native client allowed missing credential providers")
	}
}
