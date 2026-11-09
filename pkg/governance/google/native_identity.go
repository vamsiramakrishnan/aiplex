package google

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentidentity"
)

// NativeTokenProvider obtains a real certificate-bound Google ID token using
// the runtime's supported credential mechanism. Implementations must not
// substitute an ordinary ADC token or an unbound assertion.
type NativeTokenProvider interface {
	Token(context.Context, string, *tls.Certificate) (string, error)
}
type NativeTokenFunc func(context.Context, string, *tls.Certificate) (string, error)

func (f NativeTokenFunc) Token(ctx context.Context, aud string, c *tls.Certificate) (string, error) {
	if f == nil {
		return "", errors.New("native token provider unavailable")
	}
	return f(ctx, aud, c)
}

// CertificateProvider is called for each request, allowing a runtime to rotate
// credentials without restarting the client. Its returned value is snapshotted.
type CertificateProvider interface {
	Certificate(context.Context) (*tls.Certificate, error)
}
type CertificateFunc func(context.Context) (*tls.Certificate, error)

func (f CertificateFunc) Certificate(ctx context.Context) (*tls.Certificate, error) {
	if f == nil {
		return nil, errors.New("native certificate provider unavailable")
	}
	return f(ctx)
}

type FileCertificateProvider struct{ CertificateFile, PrivateKeyFile string }

func (f FileCertificateProvider) Certificate(context.Context) (*tls.Certificate, error) {
	c, e := tls.LoadX509KeyPair(f.CertificateFile, f.PrivateKeyFile)
	if e != nil {
		return nil, errors.New("native client certificate could not be loaded")
	}
	return &c, nil
}

type NativeConfig struct {
	Origin string
	// Identity must require binding and allow exactly the intended agent subject.
	Identity    agentidentity.Config
	ClientRoots *x509.CertPool
	// Transport can carry the host's pinned DNS/network dialer. TLS verification
	// remains enabled; a clone is made for each certificate generation.
	Transport *http.Transport
	Timeout   time.Duration
}

// NewNativeClient composes real injected credential acquisition, cryptographic
// claim validation and mTLS. It implements no undocumented STS exchange. Pools
// are isolated by certificate fingerprint so a rotated token cannot reuse a TLS
// connection authenticated with the preceding certificate.
func NewNativeClient(cfg NativeConfig, tokens NativeTokenProvider, certificates CertificateProvider, claims agentidentity.ClaimsVerifier) (*http.Client, error) {
	u, e := url.Parse(cfg.Origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("native agent transport requires an exact HTTPS origin")
	}
	if cfg.Identity.BindingMode != agentidentity.RequiredBinding || len(cfg.Identity.AllowedSubjects) != 1 || cfg.ClientRoots == nil || tokens == nil || certificates == nil {
		return nil, errors.New("native transport requires a bound agent subject, roots and credential providers")
	}
	v, e := agentidentity.New(cfg.Identity, claims)
	if e != nil {
		return nil, e
	}
	base := cfg.Transport
	if base == nil {
		return nil, errors.New("native agent transport requires an explicit constrained HTTP transport")
	}
	base = base.Clone()
	base.Proxy = nil
	if base.DialTLS != nil || base.DialTLSContext != nil {
		return nil, errors.New("native transport requires the standard verified TLS handshake")
	}
	if base.TLSClientConfig == nil {
		base.TLSClientConfig = &tls.Config{}
	} else {
		base.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	if base.TLSClientConfig.InsecureSkipVerify {
		return nil, errors.New("native TLS verification cannot be disabled")
	}
	base.TLSClientConfig.MinVersion = tls.VersionTLS13
	base.TLSClientConfig.Certificates = nil
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if timeout < 0 {
		return nil, errors.New("native transport timeout must be positive")
	}
	rt := &nativeTransport{origin: u.Scheme + "://" + u.Host, audience: cfg.Identity.Audience, roots: cfg.ClientRoots.Clone(), base: base, tokens: tokens, certificates: certificates, verifier: v, pools: map[string]*http.Transport{}}
	return &http.Client{Transport: rt, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type nativeTransport struct {
	origin, audience string
	roots            *x509.CertPool
	base             *http.Transport
	tokens           NativeTokenProvider
	certificates     CertificateProvider
	verifier         *agentidentity.Verifier
	mu               sync.Mutex
	pools            map[string]*http.Transport
	current          string
}

func (t *nativeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme+"://"+req.URL.Host != t.origin || req.URL.User != nil || (req.Host != "" && req.Host != req.URL.Host) {
		return nil, errors.New("native agent destination is outside configured origin")
	}
	cert, e := t.certificates.Certificate(req.Context())
	if e != nil || cert == nil || len(cert.Certificate) == 0 {
		return nil, errors.New("native client certificate unavailable")
	}
	snapshot := *cert
	snapshot.Certificate = make([][]byte, len(cert.Certificate))
	for i, der := range cert.Certificate {
		snapshot.Certificate[i] = append([]byte(nil), der...)
	}
	cert = &snapshot
	leaf, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil {
		return nil, errors.New("invalid native client certificate")
	}
	cert.Leaf = leaf
	intermediates := x509.NewCertPool()
	for _, der := range cert.Certificate[1:] {
		c, e := x509.ParseCertificate(der)
		if e != nil {
			return nil, errors.New("invalid native certificate chain")
		}
		intermediates.AddCert(c)
	}
	chains, e := leaf.Verify(x509.VerifyOptions{Roots: t.roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if e != nil {
		return nil, errors.New("native certificate trust validation failed")
	}
	token, e := t.tokens.Token(req.Context(), t.audience, cert)
	if e != nil || token == "" {
		return nil, errors.New("native certificate-bound token unavailable")
	}
	bound := req.Clone(req.Context())
	bound.TLS = &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: chains}
	verified, verifyErr := t.verifier.VerifyRequest(bound, token)
	if verifyErr != nil {
		return nil, errors.New("native outbound token subject/audience/certificate validation failed")
	}
	expires := int64(0)
	switch exp := verified["exp"].(type) {
	case float64:
		expires = int64(exp)
	case json.Number:
		expires, _ = exp.Int64()
	case int64:
		expires = exp
	case int:
		expires = int64(exp)
	}
	if expires <= time.Now().Unix() {
		return nil, errors.New("native outbound token is expired or lacks expiry")
	}
	sum := sha256.Sum256(leaf.Raw)
	key := hex.EncodeToString(sum[:])
	t.mu.Lock()
	pool := t.pools[key]
	if pool == nil {
		pool = t.base.Clone()
		pool.TLSClientConfig = pool.TLSClientConfig.Clone()
		pool.TLSClientConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return cert, nil }
		pool.TLSClientConfig.Certificates = nil
		t.pools[key] = pool
	}
	if t.current != key {
		for old, p := range t.pools {
			if old != key {
				p.CloseIdleConnections()
				delete(t.pools, old)
			}
		}
		t.current = key
	}
	t.mu.Unlock()
	out := req.Clone(req.Context())
	out.Header = req.Header.Clone()
	out.Header.Set("Authorization", "Bearer "+token)
	return pool.RoundTrip(out)
}
func (t *nativeTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.pools {
		p.CloseIdleConnections()
	}
}
