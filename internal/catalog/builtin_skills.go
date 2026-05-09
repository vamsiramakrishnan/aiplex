package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// BuiltInSkills returns a small set of curated skill bundles that ship with
// AIPlex out of the box.
type BuiltInSkills struct{}

func NewBuiltInSkills() *BuiltInSkills { return &BuiltInSkills{} }

func (b *BuiltInSkills) Name() string             { return "builtin-skills" }
func (b *BuiltInSkills) Kind() capability.Kind    { return capability.KindSkill }

type skillDef struct {
	Bundle, Name, Description string
	Triggers                  []string
}

func (b *BuiltInSkills) Fetch(_ context.Context) ([]models.Template, error) {
	bundles := []struct {
		ID, Name, Desc, Bundle string
		Skills                 []skillDef
	}{
		{
			"code-review", "Code Review",
			"Review pull requests for correctness, style, and security", "code-review",
			[]skillDef{
				{"code-review", "review_pr", "Review a pull request diff", []string{"review", "pr"}},
				{"code-review", "suggest_tests", "Suggest unit tests for a diff", []string{"tests", "coverage"}},
			},
		},
		{
			"research", "Research",
			"Source-cited research and synthesis", "research",
			[]skillDef{
				{"research", "search", "Run web search and rank results", []string{"search"}},
				{"research", "synthesize", "Synthesize a brief from sources", []string{"summary", "synthesize"}},
			},
		},
		{
			"writing", "Writing",
			"Drafting, editing, and polishing prose", "writing",
			[]skillDef{
				{"writing", "draft", "Draft a document from an outline", []string{"draft"}},
				{"writing", "edit", "Edit prose for clarity and tone", []string{"edit", "polish"}},
			},
		},
	}

	out := make([]models.Template, 0, len(bundles))
	for _, b := range bundles {
		caps := make([]capability.Capability, 0, len(b.Skills))
		for _, s := range b.Skills {
			uri := capability.New(capability.KindSkill, b.Bundle+"/"+s.Name, "v1")
			caps = append(caps, capability.Capability{
				URI:         uri.String(),
				Kind:        capability.KindSkill,
				Name:        b.Bundle + "/" + s.Name,
				Version:     "v1",
				Description: s.Description,
				Tags:        s.Triggers,
			})
		}
		out = append(out, models.Template{
			ID:           b.ID,
			Source:       "builtin",
			Kind:         capability.KindSkill,
			Name:         b.Name,
			Description:  b.Desc,
			Image:        "ghcr.io/aiplex/skills-server:latest",
			Version:      "v1",
			Category:     "skill",
			Verified:     true,
			SkillBundle:  b.Bundle,
			Capabilities: caps,
		})
	}
	return out, nil
}
