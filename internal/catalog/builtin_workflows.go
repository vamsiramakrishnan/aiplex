package catalog

import (
	"context"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// BuiltInWorkflows ships curated workflow templates so operators have working
// examples of agent-shaped declarative caps to deploy. The spec lives on
// Template.Config["spec"] and is evaluated by the workflow executor when the
// workflow capability is invoked.
type BuiltInWorkflows struct{}

func NewBuiltInWorkflows() *BuiltInWorkflows { return &BuiltInWorkflows{} }

func (b *BuiltInWorkflows) Name() string             { return "builtin-workflows" }
func (b *BuiltInWorkflows) Kind() capability.Kind    { return capability.KindWorkflow }

func (b *BuiltInWorkflows) Fetch(_ context.Context) ([]models.Template, error) {
	return []models.Template{
		{
			ID:          "grade-quiz",
			Source:      "builtin",
			Kind:        capability.KindWorkflow,
			Name:        "Grade a Quiz",
			Description: "Fetch a quiz, ask the model to grade it, store the grade in the student's memory namespace.",
			Version:     "v1",
			Category:    "workflow",
			Verified:    true,
			Capabilities: []capability.Capability{{
				URI:         "cap://workflow/grade-quiz@v1",
				Kind:        capability.KindWorkflow,
				Name:        "grade-quiz",
				Version:     "v1",
				Description: "Tutor-side grading workflow",
				Attrs: capability.Attrs{
					SideEffect: "write",
					DataClass:  "internal",
				},
			}},
			ConfigSchema: map[string]any{
				"properties": map[string]any{
					"spec": map[string]any{"type": "object"},
				},
			},
			// The executor reads inst.Config["spec"] — for built-in templates
			// we ship the spec inline so deploys work without operator input.
			Config: map[string]any{
				"spec": map[string]any{
					"inputs": map[string]any{
						"required": []string{"quiz_id", "student"},
					},
					"steps": []any{
						map[string]any{
							"id":  "fetch",
							"cap": "cap://tool/get_quiz@v1",
							"input": map[string]any{
								"id": "{{ inputs.quiz_id }}",
							},
						},
						map[string]any{
							"id":  "grade",
							"cap": "cap://model/gemini-2.5-flash@v1",
							"input": map[string]any{
								"prompt": "Grade this quiz: {{ steps.fetch.output.content }}",
							},
						},
						map[string]any{
							"id":  "store",
							"cap": "cap://memory/students/{{ inputs.student }}/grades@v1",
							"input": map[string]any{
								"key":   "quiz-{{ inputs.quiz_id }}",
								"value": map[string]any{"text": "{{ steps.grade.output.text }}"},
							},
						},
					},
					"outputs": map[string]any{
						"grade": "{{ steps.grade.output.text }}",
					},
				},
			},
		},
		{
			ID:          "research-and-summarise",
			Source:      "builtin",
			Kind:        capability.KindWorkflow,
			Name:        "Research → Summarise",
			Description: "Delegate to a research agent, then ask a model to write a 200-word brief on the result.",
			Version:     "v1",
			Category:    "workflow",
			Verified:    true,
			Capabilities: []capability.Capability{{
				URI:         "cap://workflow/research-and-summarise@v1",
				Kind:        capability.KindWorkflow,
				Name:        "research-and-summarise",
				Version:     "v1",
				Description: "Two-step research + write workflow",
				Attrs: capability.Attrs{
					SideEffect: "read",
					DataClass:  "internal",
				},
			}},
			Config: map[string]any{
				"spec": map[string]any{
					"inputs": map[string]any{
						"required": []string{"topic"},
					},
					"steps": []any{
						map[string]any{
							"id":  "research",
							"cap": "cap://task/research@v1",
							"input": map[string]any{
								"topic": "{{ inputs.topic }}",
							},
						},
						map[string]any{
							"id":  "summarise",
							"cap": "cap://model/gemini-2.5-flash@v1",
							"input": map[string]any{
								"prompt": "Write a 200-word brief on:\n{{ steps.research.output.content }}",
							},
						},
					},
					"outputs": map[string]any{
						"brief": "{{ steps.summarise.output.text }}",
					},
				},
			},
		},
	}, nil
}
