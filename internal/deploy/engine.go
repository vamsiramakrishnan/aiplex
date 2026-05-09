package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// Engine orchestrates deployments across all capability kinds.
type Engine struct {
	store       registry.Store
	k8s         K8sClient
	trustDomain string
	gatewayName string
}

// NewEngine creates a deploy engine.
func NewEngine(store registry.Store, trustDomain string) *Engine {
	return &Engine{
		store:       store,
		k8s:         NewNoOpK8sClient(),
		trustDomain: trustDomain,
		gatewayName: "aiplex-gateway",
	}
}

// NewEngineWithK8s creates a deploy engine with a real K8s client.
func NewEngineWithK8s(store registry.Store, k8s K8sClient, trustDomain, gatewayName string) *Engine {
	return &Engine{
		store:       store,
		k8s:         k8s,
		trustDomain: trustDomain,
		gatewayName: gatewayName,
	}
}

// Deploy provisions an instance for any capability kind.
func (e *Engine) Deploy(ctx context.Context, kind capability.Kind, templateID string, config map[string]any, owner, displayName string) (*models.Instance, error) {
	start := time.Now()
	logger := log.Ctx(ctx).With().Str("kind", string(kind)).Str("template", templateID).Logger()

	// 1. Resolve template
	tmpl, err := e.store.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("template %q not found: %w", templateID, err)
	}
	if kind == "" {
		kind = tmpl.Kind
	}

	// 2. Generate instance ID and namespace
	instanceID := generateID(templateID)
	namespace := kind.Namespace()

	logger.Info().Str("instance_id", instanceID).Msg("starting deploy")

	// 3. SPIFFE identity (skip for kinds with no workload)
	var spiffeID string
	if kind != capability.KindModel && kind != capability.KindMeta {
		spiffeID = fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", e.trustDomain, namespace, instanceID)
		logger.Info().Str("spiffe_id", spiffeID).Msg("identity provisioned")
	}

	// 4. Seed capabilities from the template; discovery may refine them later.
	caps := tmpl.CapSet()

	// 5. Persist instance (provisioning state)
	inst := &models.Instance{
		ID:           instanceID,
		Kind:         kind,
		TemplateID:   templateID,
		Owner:        owner,
		Namespace:    namespace,
		SpiffeID:     spiffeID,
		Capabilities: caps,
		Config:       config,
		Status:       models.StatusProvisioning,
		Replicas:     1,
		DisplayName:  displayName,
		DeployedAt:   time.Now(),
		UpdatedAt:    time.Now(),
		DeployedBy:   owner,
	}
	if err := e.store.PutInstance(ctx, inst); err != nil {
		return nil, fmt.Errorf("failed to persist instance: %w", err)
	}

	// 6. Apply K8s workload manifests (skipped for model/meta).
	manifests := GenerateManifests(inst, tmpl, e.trustDomain)
	for _, m := range manifests {
		if err := e.k8s.Apply(ctx, m); err != nil {
			inst.Status = models.StatusFailed
			e.store.PutInstance(ctx, inst)
			e.recordHistory(ctx, inst, "deploy", owner, config, start, false, err.Error())
			return nil, fmt.Errorf("failed to apply %s/%s: %w", m.Kind, m.Name, err)
		}
	}

	// 7. Discover real capabilities from the running workload (best-effort).
	discovered := e.discover(ctx, inst, logger)
	if len(discovered) > 0 {
		inst.Capabilities = discovered
	}

	// 8. Apply CapabilityRoute manifests for each capability the instance provides.
	routes := GenerateRoute(inst, tmpl, e.gatewayName)
	for _, m := range routes {
		if err := e.k8s.Apply(ctx, m); err != nil {
			inst.Status = models.StatusFailed
			e.store.PutInstance(ctx, inst)
			e.recordHistory(ctx, inst, "deploy", owner, config, start, false, err.Error())
			return nil, fmt.Errorf("failed to apply route %s/%s: %w", m.Kind, m.Name, err)
		}
	}

	// 9. Grant the owner the caps this instance just exposed.
	existing, _ := e.store.GetUserCaps(ctx, owner)
	if err := e.store.SetUserCaps(ctx, owner, existing.Union(inst.Capabilities)); err != nil {
		logger.Warn().Err(err).Msg("failed to grant owner caps")
	}

	// 10. Mark running and record history.
	e.recordHistory(ctx, inst, "deploy", owner, config, start, true, "")
	inst.Status = models.StatusRunning
	e.store.PutInstance(ctx, inst)

	logger.Info().Dur("duration", time.Since(start)).Msg("deploy complete")
	return inst, nil
}

// discover runs kind-specific discovery to refine the capability set with
// real values from the running workload. Failures fall back to template caps.
func (e *Engine) discover(ctx context.Context, inst *models.Instance, logger zerolog.Logger) capability.CapSet {
	switch inst.Kind {
	case capability.KindTool:
		return e.discoverTool(ctx, inst, logger)
	case capability.KindSkill:
		return e.discoverSkill(ctx, inst, logger)
	case capability.KindTask:
		return e.discoverTask(ctx, inst, logger)
	}
	return nil
}

func (e *Engine) discoverTool(ctx context.Context, inst *models.Instance, logger zerolog.Logger) capability.CapSet {
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/mcp", inst.ID, inst.Namespace)
	tools, err := DiscoverTools(ctx, url)
	if err != nil {
		logger.Warn().Err(err).Msg("MCP tool discovery failed — using template caps")
		return nil
	}
	if len(tools) == 0 {
		return nil
	}
	out := make(capability.CapSet, len(tools))
	for i, t := range tools {
		uri := capability.New(capability.KindTool, t.Name, "v1")
		out[i] = capability.Cap{URI: uri.String(), Actions: []string{"call"}}
	}
	logger.Info().Int("count", len(tools)).Msg("discovered MCP tools")
	return out
}

func (e *Engine) discoverSkill(ctx context.Context, inst *models.Instance, logger zerolog.Logger) capability.CapSet {
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/skills", inst.ID, inst.Namespace)
	skills, err := DiscoverSkills(ctx, url)
	if err != nil {
		logger.Warn().Err(err).Msg("skills/list discovery failed — using template caps")
		return nil
	}
	if len(skills) == 0 {
		return nil
	}
	out := make(capability.CapSet, len(skills))
	for i, s := range skills {
		uri := capability.New(capability.KindSkill, s, "v1")
		out[i] = capability.Cap{URI: uri.String(), Actions: []string{"invoke"}}
	}
	logger.Info().Int("count", len(skills)).Msg("discovered skills")
	return out
}

func (e *Engine) discoverTask(ctx context.Context, inst *models.Instance, logger zerolog.Logger) capability.CapSet {
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", inst.ID, inst.Namespace)
	card, err := DiscoverAgentCard(ctx, url)
	switch {
	case err == nil && len(card.TaskTypes) > 0:
		out := make(capability.CapSet, len(card.TaskTypes))
		for i, tt := range card.TaskTypes {
			uri := capability.New(capability.KindTask, tt.Type, "v1")
			out[i] = capability.Cap{URI: uri.String(), Actions: []string{"invoke"}}
		}
		logger.Info().Int("count", len(card.TaskTypes)).Msg("discovered A2A task types via Agent Card")
		return out
	case errors.Is(err, ErrAgentCardNotFound):
		tasks, ferr := DiscoverTasks(ctx, url)
		if ferr != nil {
			logger.Warn().Err(ferr).Msg("A2A Agent Card 404 and tasks/list also failed — using template caps")
			return nil
		}
		out := make(capability.CapSet, len(tasks))
		for i, t := range tasks {
			uri := capability.New(capability.KindTask, t, "v1")
			out[i] = capability.Cap{URI: uri.String(), Actions: []string{"invoke"}}
		}
		logger.Info().Int("count", len(tasks)).Msg("discovered A2A task types via tasks/list")
		return out
	default:
		logger.Warn().Err(err).Msg("A2A discovery failed — using template caps")
		return nil
	}
}

// Undeploy tears down an instance.
func (e *Engine) Undeploy(ctx context.Context, instanceID, performer string) error {
	start := time.Now()
	logger := log.Ctx(ctx).With().Str("instance_id", instanceID).Logger()

	inst, err := e.store.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	tmpl, _ := e.store.GetTemplate(ctx, inst.TemplateID)
	logger.Info().Msg("starting undeploy")

	if tmpl != nil {
		routes := GenerateRoute(inst, tmpl, e.gatewayName)
		for _, m := range routes {
			if err := e.k8s.Delete(ctx, m.APIVersion, m.Kind, m.Name, m.Namespace); err != nil {
				logger.Warn().Err(err).Str("kind", m.Kind).Str("name", m.Name).Msg("failed to delete route")
			}
		}
		manifests := GenerateManifests(inst, tmpl, e.trustDomain)
		for _, m := range manifests {
			if err := e.k8s.Delete(ctx, m.APIVersion, m.Kind, m.Name, m.Namespace); err != nil {
				logger.Warn().Err(err).Str("kind", m.Kind).Str("name", m.Name).Msg("failed to delete resource")
			}
		}
	}

	inst.Status = models.StatusTerminated
	inst.UpdatedAt = time.Now()
	if err := e.store.PutInstance(ctx, inst); err != nil {
		return fmt.Errorf("failed to update instance: %w", err)
	}

	e.recordHistory(ctx, inst, "undeploy", performer, nil, start, true, "")

	logger.Info().Dur("duration", time.Since(start)).Msg("undeploy complete")
	return nil
}

func (e *Engine) recordHistory(ctx context.Context, inst *models.Instance, action, performer string, config map[string]any, start time.Time, success bool, errMsg string) {
	history := &models.DeployHistory{
		ID:          generateID("hist"),
		InstanceID:  inst.ID,
		Action:      action,
		Kind:        inst.Kind,
		TemplateID:  inst.TemplateID,
		Owner:       inst.Owner,
		PerformedBy: performer,
		Config:      config,
		Timestamp:   time.Now(),
		DurationMs:  time.Since(start).Milliseconds(),
		Success:     success,
		Error:       errMsg,
	}
	if err := e.store.AppendHistory(ctx, history); err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("failed to record deploy history")
	}
}

func generateID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

