package cloudagents

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var governanceAliasARN = regexp.MustCompile(`^arn:(aws|aws-us-gov|aws-cn):bedrock:([a-z]{2}(?:-[a-z]+)+-[0-9]+):([0-9]{12}):agent-alias/([0-9A-Za-z]{10})/([0-9A-Za-z]{10})$`)

// GovernanceTargetRef identifies the target actually selected by this native
// adapter, rather than an arbitrary ResourceID label. Hosts enforce it only
// for connections declaring GovernanceBindingRef, preserving unlinked clients.
// A matching declaration is not proof of native authorization or enforcement.
func (c Connection) GovernanceTargetRef() (string, error) {
	switch c.Provider {
	case GoogleRuntime:
		if !googleResource.MatchString(c.ResourceID) {
			return "", fmt.Errorf("governed Google runtime requires a canonical reasoningEngine resource")
		}
	case AgentCoreRuntime:
		if !runtimeARN.MatchString(c.ResourceID) || strings.Split(c.ResourceID, ":")[3] != c.Region {
			return "", fmt.Errorf("governed AgentCore runtime ARN must match its region")
		}
	case BedrockAgent:
		if _, _, err := c.GovernanceAWSAccount(); err != nil {
			return "", err
		}
	case FoundryAgent:
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(c.Endpoint, "?#\\\r\n") || (u.Path != "" && u.Path != "/") || !segment.MatchString(c.Project) || !segment.MatchString(c.AgentID) {
			return "", fmt.Errorf("governed Foundry connection requires a pinned origin, project and named agent")
		}
		// This is the existing native named-agent invocation endpoint, including
		// its account origin. Generic project Responses cannot identify one agent.
		target := strings.TrimSuffix(c.Endpoint, "/") + "/api/projects/" + url.PathEscape(c.Project) + "/agents/" + url.PathEscape(c.AgentID) + "/endpoint/protocols/openai/responses"
		if c.ResourceID != target {
			return "", fmt.Errorf("governed Foundry resource must equal its named-agent invocation URL")
		}
	case CopilotStudio:
		return "", fmt.Errorf("DirectLine credentials select the bot; this connection cannot establish a native governance target")
	default:
		return "", fmt.Errorf("unsupported native governance target provider")
	}
	return c.ResourceID, nil
}

// GovernanceAWSAccount returns the signing account required for a linked
// Bedrock Classic alias. InvokeAgent's URI selects by agent/alias names, so the
// host must STS-attest the SAME credential source against this account before
// signing the invocation. An ARN declaration alone cannot attest caller identity.
// AgentCore uses its runtime ARN in the actual URI and does not use this helper.
func (c Connection) GovernanceAWSAccount() (account, partition string, err error) {
	parts := governanceAliasARN.FindStringSubmatch(c.ResourceID)
	if c.Provider != BedrockAgent || len(parts) != 6 || parts[2] != c.Region || parts[4] != c.AgentID || parts[5] != c.AliasID {
		return "", "", fmt.Errorf("governed Bedrock resource must be an agent-alias ARN matching region, agent and alias")
	}
	if (parts[1] == "aws-cn") != strings.HasPrefix(c.Region, "cn-") || (parts[1] == "aws-us-gov") != strings.HasPrefix(c.Region, "us-gov-") {
		return "", "", fmt.Errorf("governed Bedrock ARN partition and region disagree")
	}
	return parts[3], parts[1], nil
}
