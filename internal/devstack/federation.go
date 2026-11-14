package devstack

import (
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
)

// Names of the issuers `aiplex dev token --issuer` can mint for. The local
// issuer is the one the API trusts statically (the developer and the callers
// `aiplex dev token --user` mints). The three federation issuers stand in for
// external identity providers: the API does not trust them until you configure
// them through the identity APIs, exactly as with a real provider.
const (
	IssuerLocal        = "local"
	IssuerIDP          = "idp"
	IssuerWorkload     = "workload"
	IssuerUnconfigured = "unconfigured"
)

// IssuerNames lists the values of `aiplex dev token --issuer`.
var IssuerNames = []string{IssuerLocal, IssuerIDP, IssuerWorkload, IssuerUnconfigured}

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
type Federation struct {
	URL          string
	IDP          *FederatedIssuer
	Workload     *FederatedIssuer
	Unconfigured *FederatedIssuer
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
// federation issuers. It listens on 127.0.0.1 only (Listen) and serves nothing
// but public keys; tokens are minted through the local issuer's /dev/token,
// which requires the developer token.
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
