package cloudagents

import (
	"strings"
	"testing"
)

func TestGovernanceTargetRefTracksActualNativeSelectors(t *testing.T) {
	google := baseConnection(GoogleRuntime, "https://us-central1-aiplatform.googleapis.com")
	google.ResourceID = "projects/p/locations/us-central1/reasoningEngines/runtime1"
	core := baseConnection(AgentCoreRuntime, "https://bedrock-agentcore.us-east-1.amazonaws.com")
	core.Region = "us-east-1"
	core.ResourceID = "arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/runtime1"
	bedrock := baseConnection(BedrockAgent, "https://bedrock-agent-runtime.us-east-1.amazonaws.com")
	bedrock.Region = "us-east-1"
	bedrock.AgentID = "AGENT12345"
	bedrock.AliasID = "TSTALIASID"
	bedrock.ResourceID = "arn:aws:bedrock:us-east-1:123456789012:agent-alias/AGENT12345/TSTALIASID"
	foundry := baseConnection(FoundryAgent, "https://account.services.ai.azure.com/")
	foundry.Project = "project1"
	foundry.AgentID = "agent1"
	foundry.APIVersion = "2025-05-01"
	foundry.ResourceID = "https://account.services.ai.azure.com/api/projects/project1/agents/agent1/endpoint/protocols/openai/responses"
	for _, c := range []Connection{google, core, bedrock, foundry} {
		if ref, err := c.GovernanceTargetRef(); err != nil || ref != c.ResourceID {
			t.Fatalf("exact %s target rejected %q %v", c.Provider, ref, err)
		}
	}
	account, partition, err := bedrock.GovernanceAWSAccount()
	if err != nil || account != "123456789012" || partition != "aws" {
		t.Fatalf("wrong account attestation requirement %q %q %v", account, partition, err)
	}
	for _, mutate := range []func(*Connection){func(c *Connection) { c.AgentID = "AGENT99999" }, func(c *Connection) { c.AliasID = "ALIAS99999" }, func(c *Connection) { c.Region = "us-west-2" }, func(c *Connection) { c.ResourceID = strings.Replace(c.ResourceID, ":aws:", ":aws-cn:", 1) }, func(c *Connection) { c.ResourceID = "label" }, func(c *Connection) { c.ResourceID = strings.Replace(c.ResourceID, "agent-alias/", "agent/", 1) }} {
		c := bedrock
		mutate(&c)
		if _, err := c.GovernanceTargetRef(); err == nil {
			t.Fatalf("Bedrock selector/ARN drift accepted %+v", c)
		}
	}
	for _, mutate := range []func(*Connection){func(c *Connection) { c.AgentID = "different" }, func(c *Connection) { c.Project = "different" }, func(c *Connection) { c.Endpoint = "https://other-account.services.ai.azure.com" }, func(c *Connection) { c.AgentID = "" }, func(c *Connection) { c.ResourceID = "label" }, func(c *Connection) { c.Endpoint = "https://account.services.ai.azure.com/extra" }} {
		c := foundry
		mutate(&c)
		if _, err := c.GovernanceTargetRef(); err == nil {
			t.Fatalf("Foundry selector/origin drift accepted %+v", c)
		}
	}
	if _, err := baseConnection(CopilotStudio, "https://directline.botframework.com").GovernanceTargetRef(); err == nil {
		t.Fatal("credential-selected Copilot bot label claimed native target")
	}
}

func TestGovernanceTargetRefPreservesUnlinkedConnectionValidation(t *testing.T) {
	for _, provider := range []Provider{BedrockAgent, FoundryAgent, CopilotStudio} {
		c := baseConnection(provider, "https://native.example")
		c.AgentID = "agent1"
		c.AliasID = "alias1"
		c.Region = "us-east-1"
		c.Project = "project1"
		c.APIVersion = "2025-05-01"
		if err := c.Validate(); err != nil {
			t.Fatalf("unlinked legacy %s connection changed: %v", provider, err)
		}
	}
}
