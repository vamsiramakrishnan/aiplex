package devstack

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
)

// Names of the issuers `aiplex dev token --issuer` can mint for. The local
// issuer is the one the API trusts statically (the developer and the callers
// `aiplex dev token --user` mints). The federation issuers stand in for
// external identity providers: the API does not trust them until you configure
// them through the identity APIs, exactly as with a real provider.
const (
	IssuerLocal        = "local"
	IssuerIDP          = "idp"
	IssuerWorkload     = "workload"
	IssuerUnconfigured = "unconfigured"
	IssuerBranchyard   = "branchyard"
)

// IssuerNames lists the values of `aiplex dev token --issuer`.
var IssuerNames = []string{IssuerLocal, IssuerIDP, IssuerWorkload, IssuerUnconfigured, IssuerBranchyard}

// IdentitySummary is the Identity row `aiplex dev` and `aiplex dev status`
// print: every stand-in issuer of IssuerNames (all but local).
func IdentitySummary() string {
	return "federation on; stand-in providers on loopback: aiplex dev token --issuer " + strings.Join(IssuerNames[1:], "|")
}

// FederatedIssuer is one loopback identity provider of the local stack. Its
// issuer name (the iss claim) is an https name that is only compared, never
// resolved (identity presets accept https issuers only); its signing keys are
// served from the loopback listener at JWKSURL.
type FederatedIssuer struct {
	Name     string
	Issuer   *Issuer
	JWKSURL  string
	Audience string
}

// IssuerURL is the iss claim of the tokens this issuer signs.
func (f *FederatedIssuer) IssuerURL() string { return f.Issuer.IssuerURL() }

// Federation is the set of loopback identity providers the stack runs next to
// the local issuer. All three are served by one loopback listener.
//
//   - IDP stands in for a workforce identity provider: configure it with
//     `aiplex identity user-providers apply`, then mint users with
//     `aiplex dev token --issuer idp`.
//   - Workload stands in for a workload identity provider: bind an agent to it
//     with `aiplex agents identity bind`, then mint with
//     `aiplex dev token --issuer workload`.
//   - Unconfigured is never configured by the stack. A token it signs is for
//     showing that an issuer nobody configured is refused.
//   - Branchyard stands in for a Branchyard server (contract section 7): it
//     has its own loopback listener, because its issuer is that listener's
//     URL and its keys are at issuer + /.well-known/jwks.json. Bind an agent
//     to it with `aiplex agents identity bind ... --preset branchyard`, then
//     mint with `aiplex dev token --issuer branchyard --tenant NAME`.
type Federation struct {
	URL          string
	IDP          *FederatedIssuer
	Workload     *FederatedIssuer
	Unconfigured *FederatedIssuer
	Branchyard   *BranchyardIssuer
}

// BranchyardIssuer is a loopback stand-in for a Branchyard server's model
// gateway: it signs turn tokens with an Ed25519 key (alg EdDSA) and publishes
// the public key as an OKP JWKS, shaped as contract section 7 says. The API
// does not trust it until an agent identity binding with the branchyard preset
// names it.
type BranchyardIssuer struct {
	// URL is the issuer (the iss claim) and the server input of the
	// branchyard preset: http://127.0.0.1:{port}, with no trailing slash.
	URL      string
	JWKSURL  string
	Audience string
	key      ed25519.PrivateKey
	kid      string
}

func newBranchyardIssuer(url, audience string) (*BranchyardIssuer, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pub)
	return &BranchyardIssuer{URL: url, JWKSURL: url + "/.well-known/jwks.json", Audience: audience, key: key, kid: "by-" + hex.EncodeToString(sum[:6])}, nil
}

// JWKS is the public key set: one OKP Ed25519 key, never its private part.
func (b *BranchyardIssuer) JWKS() []byte {
	out, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig", "kid": b.kid,
		"x": base64.RawURLEncoding.EncodeToString(b.key.Public().(ed25519.PublicKey)),
	}}})
	return out
}

// MintTurn signs a turn token for tenant: iss, aud, sub branchyard:<tenant>,
// iat, exp (ttl, at most an hour), a fresh jti, by_purpose aiplex and the
// turn's by_tenant, by_branch and by_turn.
func (b *BranchyardIssuer) MintTurn(tenant, branch, turn string, ttl time.Duration) (string, error) {
	if !agentfederation.ValidBranchyardTenant(tenant) {
		return "", fmt.Errorf("tenant must be 1 to 128 of A-Z a-z 0-9 . _ @ + -")
	}
	if ttl <= 0 || ttl > time.Hour {
		return "", fmt.Errorf("a Branchyard token lasts at most an hour")
	}
	now := time.Now()
	return b.Sign(map[string]any{"iss": b.URL, "aud": b.Audience, "sub": "branchyard:" + tenant, "iat": now.Unix(), "exp": now.Add(ttl).Unix(), "jti": randomID(), "by_purpose": "aiplex", "by_tenant": tenant, "by_branch": branch, "by_turn": turn})
}

// Sign signs claims exactly as given, so tests can build tokens that are
// wrong in one way.
func (b *BranchyardIssuer) Sign(claims map[string]any) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": b.kid})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(b.key, []byte(input))), nil
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Get returns the named federation issuer, or nil for local and unknown names.
func (f *Federation) Get(name string) *FederatedIssuer {
	if f == nil {
		return nil
	}
	switch name {
	case IssuerIDP:
		return f.IDP
	case IssuerWorkload:
		return f.Workload
	case IssuerUnconfigured:
		return f.Unconfigured
	}
	return nil
}

// federationUserAudience is shared by the idp and unconfigured issuers so a
// refusal of the unconfigured one is about its issuer, not its audience.
const (
	federationUserAudience  = "aiplex-dev-users"
	federationAgentAudience = "aiplex-dev-agents"
)

// startFederation runs the loopback listener that serves the JWKS of the three
// federation issuers, and the Branchyard stand-in's own loopback listener.
// Both listen on 127.0.0.1 only (Listen) and serve nothing but public keys;
// tokens are minted through the local issuer's /dev/token, which requires the
// developer token.
func (s *Stack) startFederation() error {
	mux := http.NewServeMux()
	f := &Federation{}
	srv, url, err := Listen(mux)
	if err != nil {
		return err
	}
	s.servers = append(s.servers, srv)
	f.URL = url
	for _, spec := range []struct {
		name, iss, aud string
		dst            **FederatedIssuer
	}{
		{IssuerIDP, "https://idp.aiplex-dev.invalid", federationUserAudience, &f.IDP},
		{IssuerWorkload, "https://workload.aiplex-dev.invalid", federationAgentAudience, &f.Workload},
		{IssuerUnconfigured, "https://unconfigured.aiplex-dev.invalid", federationUserAudience, &f.Unconfigured},
	} {
		issuer, err := NewIssuer(spec.iss, spec.aud)
		if err != nil {
			return err
		}
		prefix := "/" + spec.name
		mux.HandleFunc(prefix+"/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(issuer.JWKS())
		})
		*spec.dst = &FederatedIssuer{Name: spec.name, Issuer: issuer, JWKSURL: url + prefix + "/.well-known/jwks.json", Audience: spec.aud}
	}
	// The Branchyard stand-in's issuer is its listener's URL, so it needs a
	// listener of its own; its key set is at the path the preset derives.
	byMux := http.NewServeMux()
	bySrv, byURL, err := Listen(byMux)
	if err != nil {
		return err
	}
	s.servers = append(s.servers, bySrv)
	if f.Branchyard, err = newBranchyardIssuer(byURL, federationAgentAudience); err != nil {
		return err
	}
	byMux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.Branchyard.JWKS())
	})
	s.Federation = f
	return nil
}

// federatedPrincipalID is the principal ID the API gives a caller one of the
// federation issuers signed for, in the stack's tenant: a user of the idp
// issuer, or a workload of the workload issuer once an agent binding names it.
func federatedPrincipalID(f *FederatedIssuer, subject string) string {
	pool := auth.UserFederationPoolID(DevTenant)
	if f.Name == IssuerWorkload {
		pool = auth.FederationPoolID(DevTenant)
	}
	return auth.CanonicalPrincipalID(f.IssuerURL(), pool, subject)
}
