package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// Engine orchestrates deployments across all three planes.
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

// Deploy provisions an instance for any plane.
func (e *Engine) Deploy(ctx context.Context, plane models.Plane, templateID string, config map[string]any, owner, displayName string) (*models.Instance, error) {
	start := time.Now()
	logger := log.Ctx(ctx).With().Str("plane", string(plane)).Str("template", templateID).Logger()

	// 1. Resolve template
	template, err := e.store.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("template %q not found: %w", templateID, err)
	}

	// 2. Generate instance ID
	instanceID := generateID(templateID)
	namespace := string(plane)

	logger.Info().Str("instance_id", instanceID).Msg("starting deploy")

	// 3. Build SPIFFE identity (not for LLMPlex — Envoy handles models directly)
	var spiffeID string
	if plane != models.PlaneLLMPlex {
		spiffeID = fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", e.trustDomain, namespace, instanceID)
		logger.Info().Str("spiffe_id", spiffeID).Msg("identity provisioned")
	}

	// 4. Determine scopes based on plane
	var scopes []string
	switch plane {
	case models.PlaneMCPlex:
		for _, tool := range template.Tools {
			scopes = append(scopes, "mcp:tools:"+tool.Name)
		}
	case models.PlaneA2APlex:
		for _, taskType := range template.TaskTypes {
			scopes = append(scopes, "a2a:task:"+taskType)
		}
	case models.PlaneLLMPlex:
		scopes = []string{"llm:model:" + template.ModelID}
		for _, cap := range template.Capabilities {
			scopes = append(scopes, "llm:capability:"+cap)
		}
	}

	// 5. Build and persist instance (provisioning state)
	inst := &models.Instance{
		ID:          instanceID,
		Plane:       plane,
		TemplateID:  templateID,
		Owner:       owner,
		Namespace:   namespace,
		SpiffeID:    spiffeID,
		Scopes:      scopes,
		Config:      config,
		Status:      models.StatusProvisioning,
		Replicas:    1,
		DisplayName: displayName,
		DeployedAt:  time.Now(),
		UpdatedAt:   time.Now(),
		DeployedBy:  owner,
	}
	if err := e.store.PutInstance(ctx, inst); err != nil {
		return nil, fmt.Errorf("failed to persist instance: %w", err)
	}

	// 6. Apply K8s workload manifests (SA, Deployment, Service, NetworkPolicy)
	manifests := GenerateManifests(inst, template, e.trustDomain)
	for _, m := range manifests {
		if err := e.k8s.Apply(ctx, m); err != nil {
			inst.Status = models.StatusFailed
			e.store.PutInstance(ctx, inst)
			e.recordHistory(ctx, inst, "deploy", owner, config, start, false, err.Error())
			return nil, fmt.Errorf("failed to apply %s/%s: %w", m.Kind, m.Name, err)
		}
	}

	// Discover actual capabilities from running server (MCPlex/A2APlex only).
	// MCP servers expose tools/list via JSON-RPC; A2A agents expose an Agent Card
	// at /.well-known/agent.json. Failures are non-fatal — fall back to template
	// scopes so a slow-starting workload doesn't block the deploy.
	switch plane {
	case models.PlaneMCPlex:
		mcpURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/mcp", instanceID, namespace)
		tools, err := DiscoverTools(ctx, mcpURL)
		if err != nil {
			logger.Warn().Err(err).Str("instance", instanceID).Msg("MCP tool discovery failed — using template scopes")
		} else if len(tools) > 0 {
			discovered := make([]string, len(tools))
			for i, t := range tools {
				discovered[i] = "mcp:tools:" + t.Name
			}
			inst.Scopes = discovered
			logger.Info().Int("count", len(tools)).Msg("discovered MCP tools")
		}
	case models.PlaneA2APlex:
		agentURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", instanceID, namespace)
		card, err := DiscoverAgentCard(ctx, agentURL)
		switch {
		case err == nil && len(card.TaskTypes) > 0:
			discovered := make([]string, len(card.TaskTypes))
			for i, tt := range card.TaskTypes {
				discovered[i] = "a2a:task:" + tt.Type
			}
			inst.Scopes = discovered
			logger.Info().Int("count", len(card.TaskTypes)).Msg("discovered A2A task types via Agent Card")
		case errors.Is(err, ErrAgentCardNotFound):
			// Agent Card unavailable — try JSON-RPC tasks/list fallback
			tasks, ferr := DiscoverTasks(ctx, agentURL)
			if ferr != nil {
				logger.Warn().Err(ferr).Str("instance", instanceID).
					Msg("A2A Agent Card 404 and tasks/list also failed — using template scopes")
			} else if len(tasks) > 0 {
				discovered := make([]string, len(tasks))
				for i, t := range tasks {
					discovered[i] = "a2a:task:" + t
				}
				inst.Scopes = discovered
				logger.Info().Int("count", len(tasks)).Msg("discovered A2A task types via tasks/list")
			}
		default:
			logger.Warn().Err(err).Str("instance", instanceID).Msg("A2A discovery failed — using template scopes")
		}
	}

	// 7. Apply route CRD (MCPRoute / HTTPRoute / LLMRoute)
	routes := GenerateRoute(inst, template, e.gatewayName)
	for _, m := range routes {
		if err := e.k8s.Apply(ctx, m); err != nil {
			inst.Status = models.StatusFailed
			e.store.PutInstance(ctx, inst)
			e.recordHistory(ctx, inst, "deploy", owner, config, start, false, err.Error())
			return nil, fmt.Errorf("failed to apply route %s/%s: %w", m.Kind, m.Name, err)
		}
	}

	// 8. Grant owner access (Dimension B — user ceiling)
	if err := e.store.SetUserScopes(ctx, owner, append(
		mustGetUserScopes(ctx, e.store, owner), scopes...,
	)); err != nil {
		logger.Warn().Err(err).Msg("failed to grant owner scopes")
	}

	// 9. Record success history
	e.recordHistory(ctx, inst, "deploy", owner, config, start, true, "")

	// Mark as running (in production, this transitions after health check passes)
	inst.Status = models.StatusRunning
	e.store.PutInstance(ctx, inst)

	logger.Info().Dur("duration", time.Since(start)).Msg("deploy complete")
	return inst, nil
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

	// Delete route CRDs
	if tmpl != nil {
		routes := GenerateRoute(inst, tmpl, e.gatewayName)
		for _, m := range routes {
			if err := e.k8s.Delete(ctx, m.APIVersion, m.Kind, m.Name, m.Namespace); err != nil {
				logger.Warn().Err(err).Str("kind", m.Kind).Str("name", m.Name).Msg("failed to delete route")
			}
		}
	}

	// Delete K8s workload manifests
	if tmpl != nil {
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
		Plane:       inst.Plane,
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

func mustGetUserScopes(ctx context.Context, store registry.Store, userID string) []string {
	scopes, _ := store.GetUserScopes(ctx, userID)
	return scopes
}

func generateID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}
