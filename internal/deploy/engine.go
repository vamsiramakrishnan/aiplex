package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// Engine orchestrates deployments across all three planes.
type Engine struct {
	store       registry.Store
	trustDomain string
}

// NewEngine creates a deploy engine.
func NewEngine(store registry.Store, trustDomain string) *Engine {
	return &Engine{store: store, trustDomain: trustDomain}
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
		// TODO: create GKE managed workload identity
		// TODO: create K8s ServiceAccount, Deployment, Service, NetworkPolicy
		logger.Info().Str("spiffe_id", spiffeID).Msg("identity provisioned")
	}

	// 4. Determine scopes based on plane
	var scopes []string
	switch plane {
	case models.PlaneMCPlex:
		// TODO: call tools/list on the deployed server to discover tools
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

	// TODO: register scopes in Hydra
	// TODO: apply route CRD (MCPRoute / HTTPRoute / LLMRoute)
	// TODO: grant owner access (Dimension B)

	// 5. Persist instance
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

	// 6. Record history
	duration := time.Since(start)
	history := &models.DeployHistory{
		ID:          generateID("hist"),
		InstanceID:  instanceID,
		Action:      "deploy",
		Plane:       plane,
		TemplateID:  templateID,
		Owner:       owner,
		PerformedBy: owner,
		Config:      config,
		Timestamp:   time.Now(),
		DurationMs:  duration.Milliseconds(),
		Success:     true,
	}
	if err := e.store.AppendHistory(ctx, history); err != nil {
		logger.Warn().Err(err).Msg("failed to record deploy history")
	}

	// Mark as running (in production, this happens after health check passes)
	inst.Status = models.StatusRunning
	e.store.PutInstance(ctx, inst)

	logger.Info().Dur("duration", duration).Msg("deploy complete")
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

	logger.Info().Msg("starting undeploy")

	// TODO: delete K8s resources (Deployment, Service, SA, NetworkPolicy)
	// TODO: delete route CRD
	// TODO: remove scopes from Hydra

	inst.Status = models.StatusTerminated
	inst.UpdatedAt = time.Now()
	if err := e.store.PutInstance(ctx, inst); err != nil {
		return fmt.Errorf("failed to update instance: %w", err)
	}

	duration := time.Since(start)
	history := &models.DeployHistory{
		ID:          generateID("hist"),
		InstanceID:  instanceID,
		Action:      "undeploy",
		Plane:       inst.Plane,
		Owner:       inst.Owner,
		PerformedBy: performer,
		Timestamp:   time.Now(),
		DurationMs:  duration.Milliseconds(),
		Success:     true,
	}
	e.store.AppendHistory(ctx, history)

	logger.Info().Dur("duration", duration).Msg("undeploy complete")
	return nil
}

func generateID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}
