// Package networking renders executable, reviewable network desired state.
// It never applies cloud changes. Cloud-native endpoint and DNS references are
// explicit inputs, separate from governance policy or claims of interception.
package networking

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type Kind string

const (
	GKEAgentIdentity     Kind = "gke-agent-identity"
	AWSPrivateLink       Kind = "aws-agentcore-privatelink"
	AzurePrivateEndpoint Kind = "azure-private-endpoint"
	KubernetesEgress     Kind = "kubernetes-egress"
)

type Profile struct {
	ID                string   `json:"id"`
	TenantID          string   `json:"tenant_id"`
	Kind              Kind     `json:"kind"`
	Region            string   `json:"region,omitempty"`
	Namespace         string   `json:"namespace,omitempty"`
	WorkloadName      string   `json:"workload_name,omitempty"`
	ServiceAccount    string   `json:"service_account,omitempty"`
	TrustDomain       string   `json:"trust_domain,omitempty"`
	AgentCardPath     string   `json:"agent_card_path,omitempty"`
	VPCID             string   `json:"vpc_id,omitempty"`
	SubnetIDs         []string `json:"subnet_ids,omitempty"`
	SecurityGroupIDs  []string `json:"security_group_ids,omitempty"`
	AWSPrincipals     []string `json:"aws_principals,omitempty"`
	AWSActions        []string `json:"aws_actions,omitempty"`
	AWSResourceARNs   []string `json:"aws_resource_arns,omitempty"`
	AWSAuthMode       string   `json:"aws_auth_mode,omitempty"`
	ResourceGroup     string   `json:"resource_group,omitempty"`
	TargetResourceID  string   `json:"target_resource_id,omitempty"`
	PrivateDNSZoneIDs []string `json:"private_dns_zone_ids,omitempty"`
	DestinationCIDRs  []string `json:"destination_cidrs,omitempty"`
	DNSCIDRs          []string `json:"dns_cidrs,omitempty"`
}
type Artifact struct {
	Path    string `json:"path"`
	Format  string `json:"format"`
	Content string `json:"content"`
}
type DeploymentPlan struct {
	Version       string     `json:"version"`
	ProfileID     string     `json:"profile_id"`
	TenantID      string     `json:"tenant_id"`
	Artifacts     []Artifact `json:"artifacts"`
	ApplyCommands [][]string `json:"apply_commands"`
	Scope         string     `json:"scope"`
}

var name = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$|^[a-z]$`)
var awsID = regexp.MustCompile(`^[a-z]+-[0-9a-f]+$`)
var awsPrincipal = regexp.MustCompile(`^arn:(aws|aws-us-gov|aws-cn):iam::[0-9]{12}:(role|user)/[A-Za-z0-9+=,.@_/-]+$`)
var googleTrust = regexp.MustCompile(`^agents\.global\.(org|proj)-[0-9]+\.system\.id\.goog$`)

func (p Profile) Validate() error {
	if !name.MatchString(p.ID) || p.TenantID == "" {
		return fmt.Errorf("network profile name and tenant required")
	}
	switch p.Kind {
	case GKEAgentIdentity:
		if !name.MatchString(p.Namespace) || !name.MatchString(p.WorkloadName) || !name.MatchString(p.ServiceAccount) || !googleTrust.MatchString(p.TrustDomain) {
			return fmt.Errorf("native GKE agent identity requires namespace, workload, service account and Google agent trust domain")
		}
		path := p.AgentCardPath
		if path == "" {
			path = "/.well-known/agent-card.json"
		}
		u, e := url.Parse(path)
		if e != nil || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || !regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`).MatchString(path) || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n\\") || strings.Contains(path, "..") {
			return fmt.Errorf("invalid relative agent card path")
		}
	case AWSPrivateLink:
		if !name.MatchString(p.Region) || !strings.HasPrefix(p.VPCID, "vpc-") || !awsID.MatchString(p.VPCID) || len(p.SubnetIDs) == 0 || len(p.SecurityGroupIDs) == 0 {
			return fmt.Errorf("AWS private connectivity requires region, VPC, subnets and security groups")
		}
		for _, s := range p.SubnetIDs {
			if !strings.HasPrefix(s, "subnet-") || !awsID.MatchString(s) {
				return fmt.Errorf("invalid AWS subnet")
			}
		}
		for _, s := range p.SecurityGroupIDs {
			if !strings.HasPrefix(s, "sg-") || !awsID.MatchString(s) {
				return fmt.Errorf("invalid AWS security group")
			}
		}
		if len(p.AWSActions) == 0 || len(p.AWSResourceARNs) == 0 {
			return fmt.Errorf("AWS endpoint policies require explicit actions and resource ARNs")
		}
		for _, action := range p.AWSActions {
			if !regexp.MustCompile(`^bedrock-agentcore:[A-Za-z]+$`).MatchString(action) {
				return fmt.Errorf("invalid or wildcard AWS endpoint action")
			}
		}
		for _, resource := range p.AWSResourceARNs {
			if !regexp.MustCompile(`^arn:(aws|aws-us-gov|aws-cn):bedrock-agentcore:[a-z0-9-]+:[0-9]{12}:(runtime|gateway)/[A-Za-z0-9_-]+$`).MatchString(resource) {
				return fmt.Errorf("invalid or wildcard AWS endpoint resource")
			}
		}
		if p.AWSAuthMode != "sigv4" && p.AWSAuthMode != "oauth" {
			return fmt.Errorf("explicit AWS endpoint authentication mode required")
		}
		if p.AWSAuthMode == "sigv4" && len(p.AWSPrincipals) == 0 {
			return fmt.Errorf("SigV4 endpoints require explicit IAM principals")
		}
		if p.AWSAuthMode == "oauth" && len(p.AWSPrincipals) > 0 {
			return fmt.Errorf("OAuth endpoint policies cannot filter IAM callers; use gateway OAuth policy")
		}
		for _, a := range p.AWSPrincipals {
			if !awsPrincipal.MatchString(a) {
				return fmt.Errorf("invalid AWS principal ARN")
			}
		}
	case AzurePrivateEndpoint:
		if !name.MatchString(p.Region) || !regexp.MustCompile(`^[A-Za-z0-9._()-]+$`).MatchString(p.ResourceGroup) || len(p.SubnetIDs) != 1 || !azureResource(p.SubnetIDs[0], "/subnets/") || !azureResource(p.TargetResourceID, "/providers/Microsoft.CognitiveServices/accounts/") || len(p.PrivateDNSZoneIDs) == 0 {
			return fmt.Errorf("Azure private endpoint requires region, group, one subnet, Foundry resource and private DNS zones")
		}
		for _, id := range p.PrivateDNSZoneIDs {
			if !azureResource(id, "/privateDnsZones/") {
				return fmt.Errorf("invalid Azure private DNS zone resource")
			}
		}
	case KubernetesEgress:
		if !name.MatchString(p.Namespace) || !name.MatchString(p.WorkloadName) || len(p.DestinationCIDRs) == 0 || len(p.DNSCIDRs) == 0 {
			return fmt.Errorf("egress policy requires namespace, workload, destination CIDRs and DNS CIDRs")
		}
		for _, raw := range append(append([]string(nil), p.DestinationCIDRs...), p.DNSCIDRs...) {
			prefix, e := netip.ParsePrefix(raw)
			if e != nil || prefix.Bits() == 0 || prefix != prefix.Masked() {
				return fmt.Errorf("invalid or unrestricted egress CIDR %q", raw)
			}
		}
	default:
		return fmt.Errorf("unsupported network profile kind %q", p.Kind)
	}
	return nil
}
func azureResource(raw, part string) bool {
	return strings.HasPrefix(raw, "/subscriptions/") && strings.Contains(raw, "/resourceGroups/") && strings.Contains(raw, part) && !strings.ContainsAny(raw, "\r\n\"\\?#${}") && !strings.Contains(raw, "..")
}
func encode(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) + "\n" }
func sorted(values []string) []string {
	v := append([]string(nil), values...)
	sort.Strings(v)
	return v
}

// Plan emits Terraform JSON or Kubernetes JSON (valid YAML) with array-form
// commands. Callers can display/save these files and explicitly choose to apply.
func Plan(p Profile) (DeploymentPlan, error) {
	if e := p.Validate(); e != nil {
		return DeploymentPlan{}, e
	}
	plan := DeploymentPlan{Version: "v1", ProfileID: p.ID, TenantID: p.TenantID, Artifacts: []Artifact{}, ApplyCommands: [][]string{}}
	switch p.Kind {
	case GKEAgentIdentity:
		card := p.AgentCardPath
		if card == "" {
			card = "/.well-known/agent-card.json"
		}
		patch := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": p.WorkloadName, "namespace": p.Namespace, "labels": map[string]string{"registry.gke.io/functional-type": "AGENT"}, "annotations": map[string]string{"a2a-protocol.org/agent-card": fmt.Sprintf("card:\n  endpoint: %s\n  protocol: HTTP\n  port: 8080\n", card)}}, "spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{"iam.gke.io/identity": "spiffe://" + p.TrustDomain + "/*", "iam.gke.io/inject-podcertificates": "true", "iam.gke.io/spiffe-identity-type": "agent-identity"}}, "spec": map[string]any{"serviceAccountName": p.ServiceAccount}}}}
		file := p.ID + ".patch.json"
		plan.Artifacts = append(plan.Artifacts, Artifact{file, "kubernetes-strategic-merge-patch", encode(patch)})
		plan.ApplyCommands = append(plan.ApplyCommands, []string{"kubectl", "patch", "deployment", p.WorkloadName, "--namespace", p.Namespace, "--type", "strategic", "--patch-file", file})
		plan.Scope = "Requests native GKE Agent Identity and Agent Registry registration for an existing Deployment; cluster prerequisites and credentials remain Google-managed."
	case AWSPrivateLink:
		resources := map[string]any{}
		for _, suffix := range []string{"bedrock-agentcore", "bedrock-agentcore-control", "bedrock-agentcore.gateway"} {
			key := strings.NewReplacer("-", "_", ".", "_").Replace(suffix)
			principal := any(map[string]any{"AWS": sorted(p.AWSPrincipals)})
			if p.AWSAuthMode == "oauth" {
				principal = "*"
			}
			policy := map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Principal": principal, "Action": sorted(p.AWSActions), "Resource": sorted(p.AWSResourceARNs)}}}
			resources[key] = map[string]any{"vpc_id": p.VPCID, "service_name": "com.amazonaws." + p.Region + "." + suffix, "vpc_endpoint_type": "Interface", "subnet_ids": sorted(p.SubnetIDs), "security_group_ids": sorted(p.SecurityGroupIDs), "private_dns_enabled": true, "policy": strings.TrimSpace(encode(policy))}
		}
		document := map[string]any{"terraform": map[string]any{"required_providers": map[string]any{"aws": map[string]any{"source": "hashicorp/aws"}}}, "provider": map[string]any{"aws": map[string]any{"region": p.Region}}, "resource": map[string]any{"aws_vpc_endpoint": resources}}
		plan.Artifacts = append(plan.Artifacts, Artifact{p.ID + ".tf.json", "terraform-json", encode(document)})
		plan.ApplyCommands = [][]string{{"terraform", "init"}, {"terraform", "plan", "-out", p.ID + ".tfplan"}, {"terraform", "apply", p.ID + ".tfplan"}}
		plan.Scope = "Creates AgentCore data, control and gateway interface endpoints with private DNS. OAuth requires endpoint Principal=*; gateway OAuth authorization remains mandatory. Security groups and existing VPC/subnets are referenced, not replaced."
	case AzurePrivateEndpoint:
		endpoint := map[string]any{"name": p.ID, "location": p.Region, "resource_group_name": p.ResourceGroup, "subnet_id": p.SubnetIDs[0], "private_service_connection": []any{map[string]any{"name": p.ID, "private_connection_resource_id": p.TargetResourceID, "is_manual_connection": false, "subresource_names": []string{"account"}}}, "private_dns_zone_group": []any{map[string]any{"name": "default", "private_dns_zone_ids": sorted(p.PrivateDNSZoneIDs)}}}
		document := map[string]any{"terraform": map[string]any{"required_providers": map[string]any{"azurerm": map[string]any{"source": "hashicorp/azurerm"}}}, "provider": map[string]any{"azurerm": map[string]any{"features": []any{map[string]any{}}}}, "resource": map[string]any{"azurerm_private_endpoint": map[string]any{p.ID: endpoint}}}
		plan.Artifacts = append(plan.Artifacts, Artifact{p.ID + ".tf.json", "terraform-json", encode(document)})
		plan.ApplyCommands = [][]string{{"terraform", "init"}, {"terraform", "plan", "-out", p.ID + ".tfplan"}, {"terraform", "apply", p.ID + ".tfplan"}}
		plan.Scope = "Creates Foundry account private ingress and attaches existing private DNS zones. Foundry subnet delegation, dependency private endpoints and egress policies must be provisioned separately. Public access is not silently changed."
	case KubernetesEgress:
		blocks := func(cidrs []string) []any {
			out := []any{}
			for _, cidr := range sorted(cidrs) {
				out = append(out, map[string]any{"ipBlock": map[string]any{"cidr": cidr}})
			}
			return out
		}
		manifest := map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]any{"name": p.ID, "namespace": p.Namespace}, "spec": map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/name": p.WorkloadName}}, "policyTypes": []string{"Egress"}, "egress": []any{map[string]any{"to": blocks(p.DNSCIDRs), "ports": []any{map[string]any{"protocol": "UDP", "port": 53}, map[string]any{"protocol": "TCP", "port": 53}}}, map[string]any{"to": blocks(p.DestinationCIDRs), "ports": []any{map[string]any{"protocol": "TCP", "port": 443}}}}}}
		file := p.ID + ".network-policy.json"
		plan.Artifacts = append(plan.Artifacts, Artifact{file, "kubernetes-json", encode(manifest)})
		plan.ApplyCommands = append(plan.ApplyCommands, []string{"kubectl", "apply", "-f", file})
		plan.Scope = "Creates an isolating egress policy allowing HTTPS to declared CIDRs and DNS to resolver CIDRs. Other NetworkPolicies are additive and can widen reachability. Requires an enforcing CNI; does not infer DNS/FQDN policies."
	}
	return plan, nil
}
