package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// commandExamples fills --help examples for commands that do not set their
// own. Keys are command paths without the leading "aiplex ". Every line is
// checked against the command tree by TestEveryHelpExampleParses.
var commandExamples = map[string]string{
	"dev env": `eval "$(aiplex dev env)"
aiplex dev env --context local`,
	"login":  "aiplex login\naiplex login --context prod",
	"logout": "aiplex logout\naiplex --context prod logout",

	"config":                 "aiplex config show\naiplex config set-context prod --url https://aiplex.example.com",
	"config show":            "aiplex config show\naiplex config show -o json",
	"config set-context":     "aiplex config set-context prod --url https://aiplex.example.com\naiplex config set-context gcp --url https://aiplex.example.com --project my-project --region us-central1",
	"config use-context":     "aiplex config use-context prod",
	"config delete-context":  "aiplex config delete-context staging",
	"config current-context": "aiplex config current-context",
	"ctx":                    "aiplex ctx\naiplex ctx prod",
	"doctor":                 "aiplex doctor\naiplex --context prod doctor",
	"health":                 "aiplex health\naiplex health --verbose",
	"init":                   "aiplex init\naiplex init --project my-project --region us-central1 --dry-run",
	"quickstart":             "aiplex quickstart --project my-project --region us-central1",

	"tools":              "aiplex tools auth-types\naiplex tools set-auth local-tools --deny reset_counter",
	"tools auth-types":   "aiplex tools auth-types\naiplex tools auth-types -o json",
	"tool-auth":          "aiplex tool-auth status github\naiplex tool-auth start github",
	"tool-auth start":    "aiplex tool-auth start github\naiplex tool-auth start github --redirect-uri http://127.0.0.1:8765/callback",
	"tool-auth complete": "aiplex tool-auth complete github response.json",
	"tool-auth status":   "aiplex tool-auth status github",
	"tool-auth revoke":   "aiplex tool-auth revoke github",

	"agents":                  "aiplex agents register tutor-agent --name \"Tutor agent\" --scopes llm:model:mock-gpt\naiplex agents identity presets",
	"agents register":         "aiplex agents register tutor-agent --name \"Tutor agent\" --scopes llm:model:mock-gpt,mcp:tools:local-tools__echo",
	"agents delete":           "aiplex agents delete tutor-agent",
	"agents permissions":      "aiplex agents permissions tutor-agent",
	"agents user-scopes":      "aiplex agents user-scopes get principal-0123abcd\naiplex agents user-scopes set principal-0123abcd --scopes llm:model:mock-gpt",
	"agents user-scopes get":  "aiplex agents user-scopes get principal-0123abcd",
	"agents user-scopes set":  "aiplex agents user-scopes set principal-0123abcd --scopes llm:model:mock-gpt,mcp:tools:*",
	"agents identity":         "aiplex agents identity presets\naiplex agents identity list tutor-agent",
	"agents identity presets": "aiplex agents identity presets\naiplex agents identity presets -o json",
	"agents identity list":    "aiplex agents identity list tutor-agent",
	"agents identity get":     "aiplex agents identity get tutor-agent ci",
	"agents identity bind":    "aiplex agents identity bind tutor-agent ci --preset github-actions --input repository=acme/app\naiplex agents identity bind tutor-agent k8s --preset kubernetes --input issuer=https://oidc.example.com --input jwks_url=https://oidc.example.com/keys --input namespace=agents --input service_account=tutor",
	"agents identity delete":  "aiplex agents identity delete tutor-agent ci",
	"agents identity plan":    "aiplex agents identity plan tutor-agent ci",
	"agents identity verify":  "aiplex agents identity verify --agent tutor-agent --binding ci --token-file token.jwt\ncat token.jwt | aiplex agents identity verify --token-file -",

	"identity":                        "aiplex identity user-providers presets\naiplex identity user-providers list",
	"identity user-providers":         "aiplex identity user-providers presets\naiplex identity user-providers apply -f okta.yaml",
	"identity user-providers presets": "aiplex identity user-providers presets",
	"identity user-providers list":    "aiplex identity user-providers list",
	"identity user-providers get":     "aiplex identity user-providers get okta",
	"identity user-providers apply":   "aiplex identity user-providers apply -f okta.yaml\naiplex identity user-providers get okta -o yaml | aiplex identity user-providers apply -f -",
	"identity user-providers delete":  "aiplex identity user-providers delete okta",
	"identity user-providers plan":    "aiplex identity user-providers plan okta",
	"identity user-providers verify":  "aiplex identity user-providers verify --provider okta --token-file token.jwt",

	"usage":        "aiplex usage records --since 24h\naiplex usage summary --by model,user --since 7d\naiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60",
	"usage prices": "aiplex usage prices list\naiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60",

	"quota":                 "aiplex quota policy\naiplex quota attempt qa_0123",
	"quota attempt":         "aiplex quota attempt qa_0123",
	"quota policy":          "aiplex quota policy\naiplex quota policy -o json",
	"quota policy-validate": "aiplex quota policy-validate policy.json",
	"quota preview":         "aiplex quota preview demand.json",
	"quota inventory":       "aiplex quota inventory budgets --limit 50",
	"quota balance":         "aiplex quota balance team-eng",
	"quota loan":            "aiplex quota loan loan-1",
	"quota loan-grant":      "aiplex quota loan-grant loan.json --idempotency-key loan-1-v1",
	"quota loan-return":     "aiplex quota loan-return loan-1 --idempotency-key loan-1-return",
	"quota evidence":        "aiplex quota evidence ev_0123",
	"quota reconcile-plan":  "aiplex quota reconcile-plan proposal.json --idempotency-key rec-2026-11",
	"quota reconcile":       "aiplex quota reconcile plan-0123",
	"quota reconcile-apply": "aiplex quota reconcile-apply plan-0123 --digest sha256:0123 --idempotency-key rec-2026-11-apply",

	"status":                      "aiplex status",
	"apply":                       "aiplex apply -f aiplex.yaml",
	"validate":                    "aiplex validate aiplex.yaml",
	"modules":                     "aiplex modules list\naiplex modules inspect llmplex",
	"modules list":                "aiplex modules list",
	"modules inspect":             "aiplex modules inspect llmplex",
	"platform":                    "aiplex platform status\naiplex platform apply --yes",
	"platform apply":              "aiplex platform apply\naiplex platform apply --skip-infra --yes",
	"platform status":             "aiplex platform status",
	"platform destroy":            "aiplex platform destroy --yes",
	"upgrade":                     "aiplex upgrade --yes",
	"infra":                       "aiplex infra approve plan-0123 --digest sha256:0123\naiplex infra receipt plan-0123",
	"infra approve":               "aiplex infra approve plan-0123 --digest sha256:0123 --reason \"reviewed\"",
	"infra execute":               "aiplex infra execute plan-0123 --approval appr-1 --digest sha256:0123",
	"infra receipt":               "aiplex infra receipt plan-0123",
	"infra observe":               "aiplex infra observe plan-0123 --idempotency-key obs-1",
	"infra observation":           "aiplex infra observation plan-0123 --idempotency-key obs-1",
	"native":                      "aiplex native governance providers",
	"native governance":           "aiplex native governance providers\naiplex native governance plan bq-prod",
	"native governance providers": "aiplex native governance providers",
	"native governance read":      "aiplex native governance read bq-prod operation.json",
	"native governance plan":      "aiplex native governance plan bq-prod --ttl 1h",
	"native governance apply":     "aiplex native governance apply plan-0123 --approval-digest sha256:0123",
	"native governance get-plan":  "aiplex native governance get-plan plan-0123",
	"native governance receipt":   "aiplex native governance receipt plan-0123",
	"catalog":                     "aiplex catalog list --plane mcplex",
	"catalog list":                "aiplex catalog list\naiplex catalog list --plane llmplex",
	"catalog get":                 "aiplex catalog get github-mcp",
	"deploy":                      "aiplex deploy github-mcp --plane mcplex --name github",
	"undeploy":                    "aiplex undeploy github-abc",
	"list":                        "aiplex list instances\naiplex list denials",
	"list instances":              "aiplex list instances --plane mcplex",
	"list agents":                 "aiplex list agents",
	"list delegations":            "aiplex list delegations",
	"list denials":                "aiplex list denials",
	"get":                         "aiplex get instance github-abc",
	"get instance":                "aiplex get instance github-abc",
	"get agent":                   "aiplex get agent tutor-agent",
	"get delegation":              "aiplex get delegation del-0123 --chain",
	"logs":                        "aiplex logs github-abc --follow\naiplex logs github-abc --tail 100",
	"runs":                        "aiplex runs list --limit 20\naiplex runs inspect run-0123",
	"runs list":                   "aiplex runs list --limit 20\naiplex runs list --has-unknown",
	"runs inspect":                "aiplex runs inspect run-0123",
	"runs events":                 "aiplex runs events run-0123",
	"runs audit":                  "aiplex runs audit run-0123",
	"runs redrive":                "aiplex runs redrive run-0123",
	"runs reconcile":              "aiplex runs reconcile run-0123",
	"runs cancel":                 "aiplex runs cancel run-0123 --reason \"superseded\"",
	"runs signal":                 "aiplex runs signal run-0123 --gate-name approval --resolution approved",
	"runs compensate":             "aiplex runs compensate run-0123",
	"runs compact":                "aiplex runs compact run-0123",
	"console":                     "aiplex console\naiplex console --port 9191",
	"tui":                         "aiplex tui",

	"llm":                      "aiplex llm surfaces list\naiplex llm providers openai",
	"llm routes":               "aiplex llm routes",
	"llm batches":              "aiplex llm batches list",
	"llm costs":                "aiplex llm costs --period week",
	"llm usage":                "aiplex llm usage --since 24h --limit 20",
	"a2a":                      "aiplex a2a agents\naiplex a2a card research-agent",
	"a2a agents":               "aiplex a2a agents",
	"a2a delegate":             "aiplex a2a delegate --caller tutor-agent --callee research-agent --task research",
	"a2a card":                 "aiplex a2a card research-agent",
	"skills":                   "aiplex skills servers\naiplex skills invocations --agent tutor-agent",
	"skills servers":           "aiplex skills servers",
	"skills record-invocation": "aiplex skills record-invocation --agent tutor-agent --instance skills-abc --skill summarize",
	"skills invocations":       "aiplex skills invocations --skill summarize",
	"clients":                  "aiplex clients profiles\naiplex clients register --file cursor.json",
	"clients profiles":         "aiplex clients profiles\naiplex clients profiles -o json",
	"clients list":             "aiplex clients list",
	"clients get":              "aiplex clients get cursor-team",
	"clients plan":             "aiplex clients plan cursor-team",
	"clients register":         "aiplex clients register --file cursor.json",
	"clients delete":           "aiplex clients delete cursor-team",
}

// applyExamples sets the examples above on commands without their own,
// indented the way cobra shows them.
func applyExamples(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			key := strings.TrimPrefix(sub.CommandPath(), "aiplex ")
			if strings.TrimSpace(sub.Example) == "" {
				if ex, ok := commandExamples[key]; ok {
					lines := strings.Split(ex, "\n")
					for i, l := range lines {
						lines[i] = "  " + l
					}
					sub.Example = strings.Join(lines, "\n")
				}
			}
			walk(sub)
		}
	}
	walk(root)
}
