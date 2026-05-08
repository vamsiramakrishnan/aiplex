package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// BuiltInSkills returns a small set of curated skill bundles that ship with
// AIPlex out of the box. Mirrors BuiltInProviders for the SkillsPlex plane.
type BuiltInSkills struct{}

func NewBuiltInSkills() *BuiltInSkills { return &BuiltInSkills{} }

func (b *BuiltInSkills) Name() string        { return "builtin-skills" }
func (b *BuiltInSkills) Plane() models.Plane { return models.PlaneSkillsPlex }

func (b *BuiltInSkills) Fetch(_ context.Context) ([]models.Template, error) {
	return []models.Template{
		{
			ID:          "code-review",
			Source:      "builtin",
			Plane:       models.PlaneSkillsPlex,
			Name:        "Code Review",
			Description: "Review pull requests for correctness, style, and security",
			Image:       "ghcr.io/aiplex/skills-server:latest",
			Version:     "1.0.0",
			Category:    "skill",
			Verified:    true,
			SkillBundle: "code-review",
			Skills: []models.SkillInfo{
				{Name: "review_pr", Description: "Review a pull request diff", Triggers: []string{"review", "pr"}},
				{Name: "suggest_tests", Description: "Suggest unit tests for a diff", Triggers: []string{"tests", "coverage"}},
			},
		},
		{
			ID:          "research",
			Source:      "builtin",
			Plane:       models.PlaneSkillsPlex,
			Name:        "Research",
			Description: "Source-cited research and synthesis",
			Image:       "ghcr.io/aiplex/skills-server:latest",
			Version:     "1.0.0",
			Category:    "skill",
			Verified:    true,
			SkillBundle: "research",
			Skills: []models.SkillInfo{
				{Name: "search", Description: "Run web search and rank results", Triggers: []string{"search"}},
				{Name: "synthesize", Description: "Synthesize a brief from sources", Triggers: []string{"summary", "synthesize"}},
			},
		},
		{
			ID:          "writing",
			Source:      "builtin",
			Plane:       models.PlaneSkillsPlex,
			Name:        "Writing",
			Description: "Drafting, editing, and polishing prose",
			Image:       "ghcr.io/aiplex/skills-server:latest",
			Version:     "1.0.0",
			Category:    "skill",
			Verified:    true,
			SkillBundle: "writing",
			Skills: []models.SkillInfo{
				{Name: "draft", Description: "Draft a document from an outline", Triggers: []string{"draft"}},
				{Name: "edit", Description: "Edit prose for clarity and tone", Triggers: []string{"edit", "polish"}},
			},
		},
	}, nil
}
