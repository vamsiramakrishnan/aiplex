package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// BuiltInMemory ships a small set of curated memory namespace templates so
// the catalog has something useful to deploy out of the box. Operators can
// add their own via Firestore-backed local sources.
type BuiltInMemory struct{}

func NewBuiltInMemory() *BuiltInMemory { return &BuiltInMemory{} }

func (b *BuiltInMemory) Name() string             { return "builtin-memory" }
func (b *BuiltInMemory) Kind() capability.Kind    { return capability.KindMemory }

type memoryDef struct {
	ID, Name, Desc string
	URIName        string // path under cap://memory/
	Backend        string // local | firestore | alloydb | vertex
	DataClass      string // public | internal | pii | regulated
	Tenanted       bool   // if true, URI uses {tenant}/{user}/<URIName> sub-path
}

func (b *BuiltInMemory) Fetch(_ context.Context) ([]models.Template, error) {
	defs := []memoryDef{
		{
			ID: "scratch", Name: "Scratch Pad",
			Desc:    "Per-user ephemeral scratch space (TTL-bound). Default backend, no PII.",
			URIName: "agents/{agent}/scratch", Backend: "firestore", DataClass: "internal",
		},
		{
			ID: "user-profile", Name: "User Profile",
			Desc:    "Per-user long-term profile and preferences. Tenant- and user-scoped, PII redaction.",
			URIName: "users/{tenant}/{user}/profile", Backend: "firestore", DataClass: "pii", Tenanted: true,
		},
		{
			ID: "team-notes", Name: "Team Notes",
			Desc:    "Shared notes within a tenant; vector-searchable. Internal data class.",
			URIName: "tenants/{tenant}/notes", Backend: "alloydb", DataClass: "internal",
		},
		{
			ID: "catalog-cache", Name: "Catalog Cache",
			Desc:    "Public catalog metadata cache. No retention policy.",
			URIName: "shared/catalog", Backend: "local", DataClass: "public",
		},
	}

	out := make([]models.Template, 0, len(defs))
	for _, d := range defs {
		uri := capability.New(capability.KindMemory, d.URIName, "v1")
		out = append(out, models.Template{
			ID:          d.ID,
			Source:      "builtin",
			Kind:        capability.KindMemory,
			Name:        d.Name,
			Description: d.Desc,
			Version:     "v1",
			Category:    "memory",
			Verified:    true,
			Capabilities: []capability.Capability{
				{
					URI:         uri.String(),
					Kind:        capability.KindMemory,
					Name:        d.URIName,
					Version:     "v1",
					Description: d.Desc,
					Attrs: capability.Attrs{
						SideEffect: "write",
						DataClass:  d.DataClass,
						Backend:    d.Backend,
					},
				},
			},
		})
	}
	return out, nil
}
