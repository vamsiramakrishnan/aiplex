package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// IAP manages the policy actually evaluated for registered destination egress.
// Its client must target https://iap.googleapis.com, independently of Registry.
type IAP struct {
	cfg    Config
	client *nativeapi.Client
}

func NewIAP(cfg Config, client *nativeapi.Client) (*IAP, error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	if client == nil {
		return nil, errors.New("IAP client is required")
	}
	for _, c := range cfg.Project {
		if c < '0' || c > '9' {
			return nil, errors.New("IAP requires numeric project number")
		}
	}
	cfg.AllowedAgentPrincipals = slices.Clone(cfg.AllowedAgentPrincipals)
	cfg.AllowedRoles = slices.Clone(cfg.AllowedRoles)
	return &IAP{cfg, client}, nil
}
func (d *IAP) Operations() []nativeapi.OperationInfo {
	return []nativeapi.OperationInfo{{Name: "egress.getIamPolicy", Description: "Read IAP registered-destination egress policy"}, {Name: "egress.setIamPolicy", Mutating: true, Description: "Set IAP egress policy with provider policy etag"}}
}
func (d *IAP) Execute(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	if e := d.ValidateOperation(op); e != nil {
		return nativeapi.Result{}, e
	}
	if op.Name != "egress.getIamPolicy" && op.Name != "egress.setIamPolicy" {
		return nativeapi.Result{}, errors.New("unknown IAP governance operation")
	}
	if _, e := d.cfg.resource(op.Resource, "agents", "mcpServers", "endpoints"); e != nil {
		return nativeapi.Result{}, e
	}
	return policyExecute(ctx, d.client, d.cfg, op, "v1/", true, "agents", "mcpServers", "endpoints")
}
func policyExecute(ctx context.Context, client *nativeapi.Client, cfg Config, op nativeapi.Operation, version string, iap bool, kinds ...string) (nativeapi.Result, error) {
	if _, e := cfg.resource(op.Resource, kinds...); e != nil {
		return nativeapi.Result{}, e
	}
	_, action, _ := strings.Cut(op.Name, ".")
	if action != "getIamPolicy" && action != "setIamPolicy" {
		return nativeapi.Result{}, errors.New("unknown IAM policy operation")
	}
	path := op.Resource
	if iap {
		path = strings.Replace(path, "/locations/"+cfg.Location+"/", "/locations/"+cfg.Location+"/iap_web/agentRegistry/", 1)
	}
	path = version + path + ":" + action
	if action == "getIamPolicy" {
		if e := empty(op.Payload); e != nil {
			return nativeapi.Result{}, e
		}
		method := http.MethodGet
		var body any
		var q url.Values
		if iap {
			method = http.MethodPost
			body = map[string]any{"options": map[string]int{"requestedPolicyVersion": 3}}
		} else {
			q = url.Values{"options.requestedPolicyVersion": []string{"3"}}
		}
		r, e := client.Do(ctx, method, path, q, body)
		if e == nil {
			var p struct {
				ETag string `json:"etag"`
			}
			if json.Unmarshal(r.Body, &p) != nil {
				return nativeapi.Result{}, errors.New("invalid IAM policy response")
			}
			r.ETag = p.ETag
		}
		return r, e
	}
	var request struct {
		Policy     json.RawMessage `json:"policy"`
		UpdateMask string          `json:"updateMask,omitempty"`
	}
	if e := strict(op.Payload, &request); e != nil {
		return nativeapi.Result{}, e
	}
	var p struct {
		ETag     string `json:"etag"`
		Version  int    `json:"version"`
		Bindings []struct {
			Condition json.RawMessage `json:"condition"`
		} `json:"bindings"`
	}
	if json.Unmarshal(request.Policy, &p) != nil || p.ETag == "" || op.ETag == "" || p.ETag != op.ETag {
		return nativeapi.Result{}, errors.New("IAM policy etag must match reviewed operation etag")
	}
	if request.UpdateMask != "" && request.UpdateMask != "bindings,etag" && request.UpdateMask != "bindings,etag,version" {
		return nativeapi.Result{}, errors.New("IAM policy updateMask must preserve etag")
	}
	for _, b := range p.Bindings {
		if len(b.Condition) > 0 && string(b.Condition) != "null" && p.Version != 3 {
			return nativeapi.Result{}, errors.New("conditional IAM bindings require policy version 3")
		}
	}
	prefix, _, _ := strings.Cut(op.Name, ".")
	current, e := policyExecute(ctx, client, cfg, nativeapi.Operation{Name: prefix + ".getIamPolicy", Resource: op.Resource}, version, iap, kinds...)
	if e != nil {
		return nativeapi.Result{}, e
	}
	if current.ETag != op.ETag {
		return nativeapi.Result{}, errors.New("IAM policy changed; review a new plan")
	}
	if e = validatePolicyDiff(cfg, current.Body, request.Policy); e != nil {
		return nativeapi.Result{}, e
	}
	r, e := client.Do(ctx, http.MethodPost, path, nil, request)
	if e == nil {
		var p struct {
			ETag string `json:"etag"`
		}
		if json.Unmarshal(r.Body, &p) != nil {
			return nativeapi.Result{}, errors.New("invalid IAM policy response")
		}
		r.ETag = p.ETag
	}
	return r, e
}

// GovernanceLink preserves identifiers with different meanings. Registration
// and topology alone never assert that Gateway routing or IAP enforcement is on.
type GovernanceLink struct {
	RegistryResource   string `json:"registry_resource"`
	RegistryIdentifier string `json:"registry_identifier"`
	AgentPrincipal     string `json:"agent_principal"`
	GatewayResource    string `json:"gateway_resource,omitempty"`
}

func (l GovernanceLink) Validate(cfg Config) error {
	if e := cfg.validate(); e != nil {
		return e
	}
	if _, e := cfg.resource(l.RegistryResource, "agents", "mcpServers", "endpoints"); e != nil {
		return e
	}
	if !urn.MatchString(l.RegistryIdentifier) {
		return errors.New("invalid registry identifier")
	}
	if !validPrincipal(l.AgentPrincipal) {
		return errors.New("invalid native agent principal")
	}
	if l.GatewayResource != "" {
		if _, e := cfg.resource(l.GatewayResource, "agentGateways"); e != nil {
			return e
		}
	}
	return nil
}
func validPrincipal(p string) bool {
	if !agentPrincipal.MatchString(p) || strings.ContainsAny(p, "%?#\\\r\n*") {
		return false
	}
	u, e := url.Parse(p)
	return e == nil && u.Scheme == "principal" && u.User == nil && strings.HasSuffix(u.Host, ".system.id.goog") && strings.HasPrefix(u.Path, "/resources/") && !strings.Contains(u.Path, "..")
}
