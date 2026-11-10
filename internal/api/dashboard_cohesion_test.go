package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

type unavailableDashboard struct {
	registry.Store
	source        string
	instanceReads int
}

func (s *unavailableDashboard) ListInstances(ctx context.Context, plane models.Plane) ([]models.Instance, error) {
	s.instanceReads++
	if s.source == "instances" {
		return nil, fmt.Errorf("backend down")
	}
	if plane != "" {
		return nil, fmt.Errorf("plane counts must share snapshot")
	}
	return s.Store.ListInstances(ctx, plane)
}
func (s *unavailableDashboard) ListAgents(ctx context.Context) ([]models.Agent, error) {
	if s.source == "agents" {
		return nil, fmt.Errorf("backend down")
	}
	return s.Store.ListAgents(ctx)
}
func (s *unavailableDashboard) GetUsageSummary(ctx context.Context, model, agent, period string) (*models.UsageSummary, error) {
	if s.source == "usage" {
		return nil, fmt.Errorf("backend down")
	}
	return s.Store.GetUsageSummary(ctx, model, agent, period)
}
func (s *unavailableDashboard) CountDelegations(ctx context.Context) (int64, error) {
	if s.source == "delegations" {
		return 0, fmt.Errorf("backend down")
	}
	return s.Store.CountDelegations(ctx)
}
func (s *unavailableDashboard) CountPolicyDenials(ctx context.Context) (int64, error) {
	if s.source == "denials" {
		return 0, fmt.Errorf("backend down")
	}
	return s.Store.CountPolicyDenials(ctx)
}
func (s *unavailableDashboard) CountPolicyDenialsSince(ctx context.Context, since time.Time) (int64, error) {
	if s.source == "denials" {
		return 0, fmt.Errorf("backend down")
	}
	return s.Store.CountPolicyDenialsSince(ctx, since)
}

func TestDashboardDoesNotPresentUnavailableDataAsZero(t *testing.T) {
	for _, source := range []string{"instances", "agents", "usage", "delegations", "denials"} {
		t.Run(source, func(t *testing.T) {
			store := &unavailableDashboard{Store: registry.NewMemoryStore(), source: source}
			w := httptest.NewRecorder()
			NewDashboardHandler(store).GetStats(w, httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil))
			if w.Code != 503 {
				t.Fatalf("missing %s reported as successful zero totals: %d %s", source, w.Code, w.Body.String())
			}
		})
	}
	store := &unavailableDashboard{Store: registry.NewMemoryStore()}
	_ = store.Store.PutInstance(context.Background(), &models.Instance{ID: "one", Plane: models.PlaneSkillsPlex, Status: models.StatusRunning})
	w := httptest.NewRecorder()
	NewDashboardHandler(store).GetStats(w, httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil))
	if w.Code != 200 || store.instanceReads != 1 {
		t.Fatalf("dashboard did not use a single inventory snapshot: %d reads%d", w.Code, store.instanceReads)
	}
}
