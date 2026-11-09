package clientplex

import (
	"fmt"
	"strings"
)

func renderCodex(p *Plan, r Registration, d Destination) {
	var b strings.Builder
	if r.ModelID != "" {
		fmt.Fprintf(&b, "model = %s\n", q(r.ModelID))
	}
	if r.Mode == "direct" {
		fmt.Fprintf(&b, "model_provider = \"gcp\"\n\n[model_providers.gcp]\nproject_id = %s\nlocation = %s\n", q(r.ProjectID), q(r.Location))
		if r.CredentialFile != "" {
			fmt.Fprintf(&b, "credential_file = %s\n", q(r.CredentialFile))
		}
		p.Services = append(p.Services, ServiceEndpoint{googleModelHost(r.Location), "model inference", true})
		p.Warnings = append(p.Warnings, "Codex's Google Cloud provider obtains ADC tokens using gcloud; configure workforce/workload federation in ADC where appropriate.")
	} else {
		b.WriteString("model_provider = \"aiplex\"\nweb_search = \"disabled\"\n\n[model_providers.aiplex]\nname = \"Aiplex gateway\"\n")
		fmt.Fprintf(&b, "base_url = %s\nwire_api = \"responses\"\n", q(d.Endpoint))
		if r.AuthMode == "gateway-key" {
			fmt.Fprintf(&b, "requires_openai_auth = false\nenv_key = %s\n", q(r.CredentialEnv))
			p.CredentialBindings = append(p.CredentialBindings, CredentialBinding{r.CredentialRef, r.CredentialEnv})
		} else {
			fmt.Fprintf(&b, "\n[model_providers.aiplex.auth]\ncommand = %s\n", q(r.TokenHelper.Command))
			args := make([]string, len(r.TokenHelper.Args))
			for i, arg := range r.TokenHelper.Args {
				args[i] = q(arg)
			}
			fmt.Fprintf(&b, "args = [%s]\n", strings.Join(args, ", "))
			timeout, refresh := r.TokenHelper.TimeoutMS, r.TokenHelper.RefreshIntervalMS
			if timeout == 0 {
				timeout = 5000
			}
			fmt.Fprintf(&b, "timeout_ms = %d\nrefresh_interval_ms = %d\n", timeout, refresh)
			p.Warnings = append(p.Warnings, "Configured token helper must print only the short-lived bearer token. Aiplex did not execute it or inspect credentials.")
		}
		p.Warnings = append(p.Warnings, "Codex requires Responses-compatible semantics; a Chat Completions endpoint alone is insufficient.")
	}
	p.Artifacts = append(p.Artifacts, Artifact{"~/.codex/config.toml", "toml", "reviewed-fragment", b.String()})
}
func renderAntigravity(p *Plan, r Registration, d Destination) {
	if r.Mode == "direct" {
		p.Services = append(p.Services, ServiceEndpoint{"businessaicode.googleapis.com", "global license discovery and model catalog", true}, ServiceEndpoint{"cloudcode-pa.googleapis.com", "global client services and telemetry", true}, ServiceEndpoint{"discoveryengine.googleapis.com", "enterprise license discovery", true}, ServiceEndpoint{googleModelHost(r.Location), "model inference when using project/ADC", true})
		if r.Location == "us" || r.Location == "eu" {
			p.Services = append(p.Services, ServiceEndpoint{"businessaicode." + r.Location + ".rep.googleapis.com", "regional developer-tools orchestration", true})
			p.Warnings = append(p.Warnings, "Regional multi-level *.rep.googleapis.com hostnames need matching regional PSC/DNS; global *.googleapis.com private VIP TLS is insufficient.")
		}
		if r.AuthMode == "adc" {
			p.Artifacts = append(p.Artifacts, Artifact{"workstation environment", "environment", "reviewed-fragment", "AGY_ADC_AUTH=true\nGOOGLE_CLOUD_PROJECT=" + r.ProjectID + "\n"})
		} else {
			p.Warnings = append(p.Warnings, "Use Antigravity Business account > Advanced WIF Configuration for workforce sign-in; deploy the organization's workforce provider configuration through its managed identity workflow.")
		}
		return
	}
	p.VersionRequirements = append(p.VersionRequirements, "Antigravity App >=2.15.0; agy CLI >=1.2.15; ACP >=1.3.0")
	protocol := "genai"
	if r.Protocol == OpenAIChat {
		protocol = "openai"
	}
	gateway := map[string]any{"enabled": true, "url": d.Endpoint, "wireProtocol": protocol}
	// Omit a plaintext key: AGY_LLM_GATEWAY_API_KEY is populated separately from
	// the opaque credential_ref by the organization's credential distribution.
	target := "~/.antigravity/settings.json"
	if r.Managed || r.ProfileID == "antigravity-acp" {
		target = "Antigravity admin_settings.json (MDM)"
	}
	addJSON(p, target, map[string]any{"gateway": gateway, "models": map[string]any{"defaultModelId": r.ModelID, "list": modelList(r)}})
	p.CredentialBindings = append(p.CredentialBindings, CredentialBinding{r.CredentialRef, "AGY_LLM_GATEWAY_API_KEY"})
	p.PreserveHeaders = []string{"User-Agent", "X-Goog-Api-Client"}
	p.Warnings = append(p.Warnings, "Antigravity gateway mode uses centralized service-account PAYG billing; included seat subscription quotas do not apply.", "Gateway must preserve attribution headers, optionally inject trusted X-Goog-User-Project, flush SSE, and explicitly acknowledge POST /v1beta:* telemetry.", "Antigravity web does not support enterprise gateway routing; ACP supports only the genai protocol.", "Client-provided cost-center headers are attribution hints, not trusted user/group identity.")
}
func renderClaudeCode(p *Plan, r Registration, d Destination) {
	if r.Mode == "claude-apps-gateway" {
		if r.Managed {
			addJSON(p, "Claude Code managed-settings.json (MDM)", map[string]any{"forceLoginMethod": "gateway", "forceLoginGatewayUrl": d.Endpoint, "parentSettingsBehavior": "merge"})
		} else {
			addJSON(p, "~/.claude/settings.json", map[string]any{"forceLoginMethod": "gateway", "forceLoginGatewayUrl": d.Endpoint})
			p.VersionRequirements = append(p.VersionRequirements, "Claude Code >=2.1.295 for gateway login settings in the user file")
		}
		p.Warnings = append(p.Warnings, "Claude apps gateway sign-in requires a human browser device flow; unattended CI must use provider authentication directly.", "Gateway-native managed settings require the deployed Claude apps gateway protocol and supported client version, not a generic Messages proxy.")
		return
	}
	env := map[string]string{}
	if r.Mode == "direct" {
		env["CLAUDE_CODE_USE_VERTEX"] = "1"
		env["ANTHROPIC_VERTEX_PROJECT_ID"] = r.ProjectID
		env["CLOUD_ML_REGION"] = r.Location
		p.Services = append(p.Services, ServiceEndpoint{googleModelHost(r.Location), "Claude Vertex rawPredict inference", true})
	} else {
		if r.Protocol == VertexAnthropic {
			env["CLAUDE_CODE_USE_VERTEX"] = "1"
			env["CLAUDE_CODE_SKIP_VERTEX_AUTH"] = "1"
			env["ANTHROPIC_VERTEX_BASE_URL"] = d.Endpoint
			project, location := r.ProjectID, r.Location
			if project == "" {
				project = d.ProjectID
			}
			if location == "" {
				location = d.Location
			}
			env["ANTHROPIC_VERTEX_PROJECT_ID"] = project
			env["CLOUD_ML_REGION"] = location
		} else {
			env["ANTHROPIC_BASE_URL"] = d.Endpoint
		}
		p.CredentialBindings = append(p.CredentialBindings, CredentialBinding{r.CredentialRef, "ANTHROPIC_AUTH_TOKEN"})
		p.Warnings = append(p.Warnings, "Resolve credential_ref at the workstation or trusted launcher; the returned token is not stored in this plan.")
	}
	if r.ModelID != "" {
		env["ANTHROPIC_MODEL"] = r.ModelID
	}
	addJSON(p, "~/.claude/settings.json", map[string]any{"env": env})
	p.PreserveHeaders = []string{"anthropic-beta", "anthropic-version"}
	p.Warnings = append(p.Warnings, "Forward unknown provider-native fields and streaming events unchanged; preserve final message_delta/message_stop events and upstream error headers.")
}
func renderClaudeDesktop(p *Plan, r Registration, d Destination) {
	addJSON(p, "Claude Desktop managed configuration (MDM)", map[string]any{"bootstrapUrl": strings.TrimSuffix(d.Endpoint, "/") + "/user/bootstrap"})
	p.Warnings = append(p.Warnings, "The Claude apps gateway must opt this user/group policy into desktop and provide /user/bootstrap; generic ANTHROPIC_BASE_URL does not configure Claude Desktop.", "CLI and Desktop sign in separately; deploy parentSettingsBehavior=merge on embedded Claude Code hosts to apply gateway egress policy.")
}
