package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/governance"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

type NativeProviderInfo struct {
	ID           string                    `json:"id"`
	Type         string                    `json:"type"`
	Operations   []nativeapi.OperationInfo `json:"operations"`
	ApplyEnabled bool                      `json:"apply_enabled"`
	Status       string                    `json:"status"`
}
type NativePlan = governance.Plan
type NativeReceipt = governance.Receipt
type NativeObservation = governance.Observation

type NativeReadRequest struct {
	Operation       nativeapi.Operation `json:"operation"`
	BindingRevision uint64              `json:"binding_revision"`
}
type NativePlanRequest struct {
	DesiredID       string `json:"desired_id"`
	BindingRevision uint64 `json:"binding_revision"`
	TTLSeconds      int64  `json:"ttl_seconds"`
}
type NativeApplyRequest struct {
	PlanRevision   uint64 `json:"plan_revision"`
	ApprovalDigest string `json:"approval_digest"`
}

var nativeObjectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func nativePath(collection, id string) (string, error) {
	if !nativeObjectID.MatchString(id) {
		return "", fmt.Errorf("invalid native governance resource ID")
	}
	return "/api/v1/iam/native/" + collection + "/" + id, nil
}
func (c *Client) ListNativeProviders(ctx context.Context) ([]NativeProviderInfo, error) {
	var out []NativeProviderInfo
	err := c.do(ctx, http.MethodGet, "/api/v1/iam/native/providers", nil, &out)
	return out, err
}
func (c *Client) ReadNativeBinding(ctx context.Context, id string, req NativeReadRequest) (NativeObservation, error) {
	path, err := nativePath("bindings", id)
	if err != nil {
		return NativeObservation{}, err
	}
	if req.BindingRevision == 0 || strings.TrimSpace(req.Operation.Name) == "" || strings.TrimSpace(req.Operation.Resource) == "" {
		return NativeObservation{}, fmt.Errorf("operation, resource, and positive binding revision required")
	}
	if err = nativeapi.ValidatePublicPayload(req.Operation.Payload); err != nil {
		return NativeObservation{}, err
	}
	var out NativeObservation
	err = c.do(ctx, http.MethodPost, path+"/read", req, &out)
	return out, err
}
func (c *Client) PlanNativeBinding(ctx context.Context, id string, req NativePlanRequest) (NativePlan, error) {
	path, err := nativePath("bindings", id)
	if err != nil {
		return NativePlan{}, err
	}
	if req.BindingRevision == 0 || !nativeObjectID.MatchString(req.DesiredID) || req.TTLSeconds <= 0 {
		return NativePlan{}, fmt.Errorf("desired ID, positive binding revision, and positive TTL required")
	}
	var out NativePlan
	err = c.do(ctx, http.MethodPost, path+"/plan", req, &out)
	return out, err
}
func (c *Client) ApplyNativePlan(ctx context.Context, id string, req NativeApplyRequest) (NativeReceipt, error) {
	path, err := nativePath("plans", id)
	if err != nil {
		return NativeReceipt{}, err
	}
	if req.PlanRevision == 0 || strings.TrimSpace(req.ApprovalDigest) == "" || len(req.ApprovalDigest) > 256 || strings.ContainsAny(req.ApprovalDigest, "\r\n") {
		return NativeReceipt{}, fmt.Errorf("positive plan revision and reviewed approval digest required")
	}
	var out NativeReceipt
	err = c.do(ctx, http.MethodPost, path+"/apply", req, &out)
	return out, err
}
func (c *Client) GetNativePlan(ctx context.Context, id string) (NativePlan, error) {
	path, err := nativePath("plans", id)
	if err != nil {
		return NativePlan{}, err
	}
	var out NativePlan
	err = c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
func (c *Client) GetNativeReceipt(ctx context.Context, id string) (NativeReceipt, error) {
	path, err := nativePath("receipts", id)
	if err != nil {
		return NativeReceipt{}, err
	}
	var out NativeReceipt
	err = c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
