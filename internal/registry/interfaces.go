package registry

import (
	"context"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// These capability interfaces let a module use its own persistence adapter.
// Store remains the compatibility composition implemented by MemoryStore and
// FirestoreStore; module handlers never require unrelated module tables.

type TemplateReader interface {
	GetTemplate(context.Context, string) (*models.Template, error)
}

type TemplateLister interface {
	ListTemplates(context.Context, models.Plane, int, int) ([]models.Template, int, error)
}

type UserScopeStore interface {
	GetUserScopes(context.Context, string) ([]string, error)
	SetUserScopes(context.Context, string, []string) error
}

type InstanceReader interface {
	GetInstance(context.Context, string) (*models.Instance, error)
	ListInstances(context.Context, models.Plane) ([]models.Instance, error)
}

// DeploymentStore is the orchestration boundary. Deployment does not require
// model usage, agent delegation, or identity-provider persistence.
type DeploymentStore interface {
	TemplateReader
	InstanceReader
	UserScopeStore
	PutInstance(context.Context, *models.Instance) error
	AppendHistory(context.Context, *models.DeployHistory) error
}

// CatalogStore is the catalog API's read boundary.
type CatalogStore interface {
	TemplateReader
}

// AgentStore owns agent registrations and their template-based scope catalog.
type AgentStore interface {
	TemplateLister
	GetAgent(context.Context, string) (*models.Agent, error)
	ListAgents(context.Context) ([]models.Agent, error)
	PutAgent(context.Context, *models.Agent) error
	DeleteAgent(context.Context, string) error
}

// LLMPlexStore contains model configuration and usage reporting, not quota
// reservations. A future quota module owns its transactional ledger separately.
type LLMPlexStore interface {
	TemplateReader
	GetRouteConfig(context.Context, string) (*models.LLMRouteConfig, error)
	ListRouteConfigs(context.Context) ([]models.LLMRouteConfig, error)
	PutRouteConfig(context.Context, *models.LLMRouteConfig) error
	DeleteRouteConfig(context.Context, string) error
	ListProviderConfigs(context.Context) ([]models.ProviderConfig, error)
	PutProviderConfig(context.Context, *models.ProviderConfig) error
	AppendUsage(context.Context, *models.UsageRecord) error
	GetUsageSummary(context.Context, string, string, string) (*models.UsageSummary, error)
	ListUsageRecords(context.Context, string, string, time.Time, int) ([]models.UsageRecord, error)
}

type A2APlexStore interface {
	TemplateReader
	InstanceReader
	AppendDelegation(context.Context, *models.Delegation) error
	GetDelegation(context.Context, string) (*models.Delegation, error)
	ListDelegations(context.Context, string, int) ([]models.Delegation, error)
	UpdateDelegation(context.Context, *models.Delegation) error
}

type SkillsPlexStore interface {
	TemplateReader
	InstanceReader
	AppendPolicyDenial(context.Context, *models.PolicyDenial) error
	AppendSkillInvocation(context.Context, *models.SkillInvocation) error
	ListSkillInvocations(context.Context, string, string, int) ([]models.SkillInvocation, error)
}

type RoleBindingStore interface {
	GetRoleBinding(context.Context, string) (*models.RoleBinding, error)
	ListRoleBindings(context.Context) ([]models.RoleBinding, error)
	ListRoleBindingsByGroup(context.Context, string) ([]models.RoleBinding, error)
	PutRoleBinding(context.Context, *models.RoleBinding) error
	DeleteRoleBinding(context.Context, string) error
}

// Keep the bundled persistence implementations checked against every module's
// capability boundary as the modules evolve.
var (
	_ DeploymentStore  = (*MemoryStore)(nil)
	_ DeploymentStore  = (*FirestoreStore)(nil)
	_ CatalogStore     = (*MemoryStore)(nil)
	_ AgentStore       = (*MemoryStore)(nil)
	_ LLMPlexStore     = (*MemoryStore)(nil)
	_ A2APlexStore     = (*MemoryStore)(nil)
	_ SkillsPlexStore  = (*MemoryStore)(nil)
	_ RoleBindingStore = (*MemoryStore)(nil)
	_ CatalogStore     = (*FirestoreStore)(nil)
	_ AgentStore       = (*FirestoreStore)(nil)
	_ LLMPlexStore     = (*FirestoreStore)(nil)
	_ A2APlexStore     = (*FirestoreStore)(nil)
	_ SkillsPlexStore  = (*FirestoreStore)(nil)
	_ RoleBindingStore = (*FirestoreStore)(nil)
)
