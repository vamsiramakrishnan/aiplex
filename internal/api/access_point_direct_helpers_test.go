package api

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// directSurface is one provider-direct model surface exposed by an access
// point under a client-facing name.
type directSurface struct {
	id       string
	name     string // client-facing model name
	native   string // provider model or deployment
	protocol string
	provider modelapi.Provider
	ops      []string
	auth     *toolcredential.Auth
}

// newDirectAPFixture saves the surfaces and an access point exposing them to
// the signed-in user, over the shared upstream fixture.
func newDirectAPFixture(t *testing.T, apID string, rows []directSurface) *apFixture {
	t.Helper()
	f := newAPFixture(t)
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{
		{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "COHESION_HOST_CREDENTIAL", AllowedOrigins: []string{f.upstream.URL}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.Runtime = runtime
	installProviderCredentials(t, f.h)
	spec := AccessPoint{ID: apID, DisplayName: apID, CallerKinds: []string{CallerUser}}
	scopes := append([]string(nil), apAllScopes...)
	for _, r := range rows {
		provider := r.provider
		ops, err := modelapi.SelectProviderOperations(provider.ID, r.ops...)
		if err != nil {
			t.Fatal(err)
		}
		surface := modelapi.Surface{ID: r.id, Protocol: r.protocol, Origin: f.upstream.URL, Models: []string{r.native}, Operations: ops, Provider: &provider}
		f.put("model-surface", r.id, ModelConnection{Surface: surface, IdentityRef: "host", Auth: r.auth})
		spec.Models = append(spec.Models, AccessPointModel{Name: r.name, SurfaceRef: r.id, NativeModel: r.native})
		scopes = append(scopes, "llm:model:"+r.native)
		for _, op := range ops {
			scopes = append(scopes, "llm:surface:"+r.id+":"+op.ID)
		}
	}
	f.put(AccessPointKind, apID, spec)
	f.as(apUser("alice", "platform-eng"), scopes...)
	return f
}

// lastUpstream is the most recent request the upstream fixture received.
func (f *apFixture) lastUpstream() upstreamCall {
	calls := f.upstreamCalls()
	if len(calls) == 0 {
		f.t.Fatal("no upstream call was made")
	}
	return calls[len(calls)-1]
}
