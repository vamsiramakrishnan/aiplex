package aiplex

import (
	"context"
	"net/http"
	"net/url"

	"github.com/vamsiramakrishnan/aiplex/pkg/deployment"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func deploymentPlanPath(id string) (string, error) {
	if err := validateResourceID(id); err != nil {
		return "", err
	}
	return "/api/v1/network/plans/" + url.PathEscape(id), nil
}
func (c *Client) ApproveDeployment(ctx context.Context, id, digest, reason string) (*resources.Record, error) {
	path, err := deploymentPlanPath(id)
	if err != nil {
		return nil, err
	}
	var out resources.Record
	err = c.do(ctx, http.MethodPost, path+"/approve", map[string]string{"digest": digest, "reason": reason}, &out)
	return &out, err
}
func (c *Client) ExecuteDeployment(ctx context.Context, id, digest, approvalID string) (*deployment.Receipt, error) {
	path, err := deploymentPlanPath(id)
	if err != nil {
		return nil, err
	}
	var out deployment.Receipt
	err = c.do(ctx, http.MethodPost, path+"/execute", map[string]string{"digest": digest, "approval_id": approvalID}, &out)
	return &out, err
}
func (c *Client) GetDeploymentReceipt(ctx context.Context, id string) (*deployment.Receipt, error) {
	path, err := deploymentPlanPath(id)
	if err != nil {
		return nil, err
	}
	var out deployment.Receipt
	err = c.do(ctx, http.MethodGet, path+"/receipt", nil, &out)
	return &out, err
}
func (c *Client) ObserveDeployment(ctx context.Context, id, key string) (*deployment.Observation, error) {
	path, err := deploymentPlanPath(id)
	if err != nil {
		return nil, err
	}
	var out deployment.Observation
	err = c.doWithHeaders(ctx, http.MethodPost, path+"/observe", map[string]any{}, &out, map[string]string{"Idempotency-Key": key})
	return &out, err
}

func (c *Client) GetDeploymentObservation(ctx context.Context, id, key string) (*deployment.Observation, error) {
	path, err := deploymentPlanPath(id)
	if err != nil {
		return nil, err
	}
	var out deployment.Observation
	err = c.doWithHeaders(ctx, http.MethodGet, path+"/observation", nil, &out, map[string]string{"Idempotency-Key": key})
	return &out, err
}
