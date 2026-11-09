package networking

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutableNetworkingPlans(t *testing.T) {
	profiles := []Profile{
		{ID: "agent-id", TenantID: "t", Kind: GKEAgentIdentity, Namespace: "agents", WorkloadName: "agent", ServiceAccount: "agent", TrustDomain: "agents.global.org-123.system.id.goog"},
		{ID: "aws-agent", TenantID: "t", Kind: AWSPrivateLink, Region: "us-east-1", VPCID: "vpc-123abc", SubnetIDs: []string{"subnet-123abc"}, SecurityGroupIDs: []string{"sg-123abc"}, AWSAuthMode: "sigv4", AWSPrincipals: []string{"arn:aws:iam::123456789012:role/agent"}, AWSActions: []string{"bedrock-agentcore:InvokeAgentRuntime"}, AWSResourceARNs: []string{"arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/agent"}},
		{ID: "azure-agent", TenantID: "t", Kind: AzurePrivateEndpoint, Region: "eastus", ResourceGroup: "rg", SubnetIDs: []string{"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v/subnets/private"}, TargetResourceID: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/foundry", PrivateDNSZoneIDs: []string{"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/privateDnsZones/privatelink.services.ai.azure.com"}},
		{ID: "agent-egress", TenantID: "t", Kind: KubernetesEgress, Namespace: "agents", WorkloadName: "agent", DestinationCIDRs: []string{"10.0.0.0/24"}, DNSCIDRs: []string{"10.0.1.2/32"}},
	}
	for _, p := range profiles {
		t.Run(string(p.Kind), func(t *testing.T) {
			plan, e := Plan(p)
			if e != nil {
				t.Fatal(e)
			}
			if len(plan.Artifacts) != 1 || len(plan.ApplyCommands) == 0 {
				t.Fatal(plan)
			}
			if !json.Valid([]byte(plan.Artifacts[0].Content)) {
				t.Fatal("not executable JSON")
			}
			again, _ := Plan(p)
			if plan.Artifacts[0].Content != again.Artifacts[0].Content {
				t.Fatal("nondeterministic plan")
			}
			if p.Kind == AWSPrivateLink && strings.Contains(plan.Artifacts[0].Content, `bedrock-agentcore:*`) {
				t.Fatal("wildcard endpoint action")
			}
		})
	}
}
func TestNetworkProfilesRejectInjectionAndUnrestrictedEgress(t *testing.T) {
	p := Profile{ID: "test", TenantID: "t", Kind: KubernetesEgress, Namespace: "a", WorkloadName: "a", DestinationCIDRs: []string{"0.0.0.0/0"}, DNSCIDRs: []string{"10.0.0.1/32"}}
	if _, e := Plan(p); e == nil {
		t.Fatal("unrestricted egress accepted")
	}
	p = Profile{ID: "test", TenantID: "t", Kind: GKEAgentIdentity, Namespace: "agents", WorkloadName: "agent", ServiceAccount: "agent", TrustDomain: "agents.global.org-123.system.id.goog", AgentCardPath: "/agent\nmalicious: true"}
	if _, e := Plan(p); e == nil {
		t.Fatal("agent metadata injection accepted")
	}
}
