package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// surfacePreset is what `llm surfaces create --provider X` fills in.
type surfacePreset struct {
	providerID string
	protocol   string
	origin     string
	operations []string
	provider   map[string]any
	auth       func(secretRef string) map[string]any
}

var surfacePresets = map[string]surfacePreset{
	"openai": {
		providerID: "openai", protocol: "openai-chat", origin: "https://api.openai.com",
		operations: []string{"openai.v1.chat-completions", "openai.v1.responses", "openai.v1.embeddings"},
		provider:   map[string]any{"id": "openai", "stream_usage": "inject"},
		auth: func(ref string) map[string]any {
			return map[string]any{"type": "bearer", "secret_ref": ref}
		},
	},
	"anthropic": {
		providerID: "anthropic", protocol: "anthropic-messages", origin: "https://api.anthropic.com",
		operations: []string{"anthropic.messages", "anthropic.messages-count-tokens"},
		provider:   map[string]any{"id": "anthropic", "anthropic_version": "2023-06-01"},
		auth: func(ref string) map[string]any {
			return map[string]any{"type": "api_key", "in": "header", "name": "x-api-key", "secret_ref": ref}
		},
	},
	"gemini": {
		providerID: "gemini-developer", protocol: "google-genai", origin: "https://generativelanguage.googleapis.com",
		operations: []string{"gemini.models.generateContent", "gemini.models.streamGenerateContent", "gemini.models.countTokens"},
		provider:   map[string]any{"id": "gemini-developer"},
		auth: func(ref string) map[string]any {
			return map[string]any{"type": "api_key", "in": "header", "name": "x-goog-api-key", "secret_ref": ref}
		},
	},
	"openai-compatible": {
		providerID: "openai-compatible", protocol: "openai-chat",
		operations: []string{"compat.chat-completions"},
		provider:   map[string]any{"id": "openai-compatible", "usage_semantics": "openai", "stream_usage": "inject"},
		auth: func(ref string) map[string]any {
			if ref == "" {
				return map[string]any{"type": "none"}
			}
			return map[string]any{"type": "bearer", "secret_ref": ref}
		},
	},
}

var surfaceProviderAliases = map[string]string{"gemini-developer": "gemini", "google-genai": "gemini", "compat": "openai-compatible", "ollama": "openai-compatible", "vllm": "openai-compatible"}

func surfacePresetNames() string { return "openai, anthropic, gemini or openai-compatible" }

type surfaceAddFlags struct {
	provider, origin, secretRef string
	models, operations          []string
	update                      bool
}

func llmxSurfacesCreateCmd() *cobra.Command {
	var f surfaceAddFlags
	cmd := &cobra.Command{
		Use:     "create <id> --provider <provider> --models <a,b> [--secret-ref <name>]",
		Aliases: []string{"add"},
		Short:   "Connect a model provider from flags (or use apply -f for a full spec)",
		Long: `Creates a model surface: one provider endpoint, the models callers may use on
it, the operations (API calls) it allows and a secret REFERENCE for its key.

--provider fills in the endpoint, protocol, credential header and a sensible
set of operations from the server's provider catalog:
  openai             chat completions, responses, embeddings (bearer key)
  anthropic          messages, count tokens (x-api-key)
  gemini             generateContent, streamGenerateContent, countTokens (x-goog-api-key)
  openai-compatible  chat completions on --origin (Ollama, vLLM, LM Studio, ...)
Override them with --operations (IDs from: aiplex llm providers <provider>).

--secret-ref names the secret holding the provider key; the key itself is
never passed to the CLI. With aiplex dev, a reference NAME resolves from the
environment variable AIPLEX_TOOL_SECRET_<NAME> (upper case, '-' as '_') of
the shell that started aiplex dev, and the provider's origin must be allowed:
aiplex dev --allow-origin https://api.openai.com.

Then expose the models on an access point:
  aiplex access-points create team --model gpt-4o-mini=openai --allow-groups eng`,
		Example: `  aiplex llm surfaces add openai --provider openai --models gpt-4o-mini,gpt-4o --secret-ref openai-key
  aiplex llm surfaces add claude --provider anthropic --models claude-sonnet-4-5 --secret-ref anthropic-key
  aiplex llm surfaces add local-ollama --provider openai-compatible --origin http://127.0.0.1:11434 --models llama3.2
  aiplex llm surfaces add openai --provider openai --models gpt-4o-mini --operations openai.v1.chat-completions --secret-ref openai-key --update`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			name := strings.ToLower(f.provider)
			if a, ok := surfaceProviderAliases[name]; ok {
				name = a
			}
			preset, ok := surfacePresets[name]
			if !ok {
				hints := []string{"Providers: " + surfacePresetNames()}
				keys := make([]string, 0, len(surfacePresets))
				for k := range surfacePresets {
					keys = append(keys, k)
				}
				for k := range surfaceProviderAliases {
					keys = append(keys, k)
				}
				if near := nearest(f.provider, keys); len(near) > 0 {
					hints = append([]string{"Did you mean: --provider " + near[0]}, hints...)
				}
				hints = append(hints, "Azure OpenAI, Bedrock and Vertex AI surfaces need more settings: write a spec and use aiplex llm surfaces apply -f")
				if f.provider == "" {
					return usageError("--provider is required", hints...)
				}
				return usageError(fmt.Sprintf("--provider %q is not one this command sets up", f.provider), hints...)
			}
			models := nonEmpty(f.models)
			if len(models) == 0 {
				return usageError("--models is required: the model names callers may use, comma separated", "Example: --models gpt-4o-mini,gpt-4o")
			}
			origin := strings.TrimRight(f.origin, "/")
			if origin == "" {
				origin = preset.origin
			}
			if origin == "" {
				return usageError("--origin is required for --provider "+name, "Example: --origin http://127.0.0.1:11434")
			}
			if preset.origin != "" && name != "openai-compatible" && f.secretRef == "" {
				return usageError("--secret-ref is required for --provider "+name+": the NAME of the secret that holds the API key (never the key)",
					"With aiplex dev: export AIPLEX_TOOL_SECRET_OPENAI_KEY=... before starting it, then pass --secret-ref openai-key")
			}
			if f.secretRef != "" && !aiplex.ValidToolSecretRef(f.secretRef) {
				return usageError("--secret-ref takes a secret reference NAME (letters, digits, '-' or '_'), never a key")
			}
			ops := preset.operations
			if len(f.operations) > 0 {
				ops = nonEmpty(f.operations)
			}
			opsAny := make([]any, len(ops))
			for i, o := range ops {
				opsAny[i] = o
			}
			providerBlock := map[string]any{}
			for k, v := range preset.provider {
				providerBlock[k] = v
			}
			generic := map[string]any{
				"surface": map[string]any{"id": id, "protocol": preset.protocol, "origin": origin, "models": models, "operations": opsAny, "provider": providerBlock},
				"auth":    preset.auth(f.secretRef),
			}
			c := newClient()
			if err := llmxExpandOperationIDs(generic, llmxClientCatalog(cmd.Context(), c)); err != nil {
				return usageError(err.Error())
			}
			raw, err := json.Marshal(generic)
			if err != nil {
				return err
			}
			spec, err := llmxReadSpec(bytes.NewReader(raw))
			if err != nil {
				return err
			}
			var revision uint64
			current, err := c.GetModelSurface(cmd.Context(), id)
			switch {
			case err == nil:
				if !f.update {
					return newCLIError(exitConflict, fmt.Sprintf("model surface %q already exists (revision %d)", id, current.Revision),
						"Replace it with the same command and --update", "Or edit it: aiplex llm surfaces get "+id+" -o yaml > s.yaml; aiplex llm surfaces apply -f s.yaml")
				}
				revision = current.Revision
			case llmxIsNotFound(err):
			default:
				return fmt.Errorf("read surface: %w", err)
			}
			saved, err := llmxApply(context.WithoutCancel(cmd.Context()), c, spec, revision, true)
			if err != nil {
				return err
			}
			if llmxMachine() {
				llmxPrint(llmxRecordJSON{saved.ID, saved.Revision, saved.Spec})
				return nil
			}
			verb := "Created"
			if revision > 0 {
				verb = "Updated"
			}
			fmt.Printf("%s model surface %s (revision %d): %s at %s, models %s, %d operations.\n", verb, saved.ID, saved.Revision, preset.providerID, origin, strings.Join(models, ", "), len(ops))
			fmt.Printf("\nNext: expose a model on an access point:\n  aiplex access-points create <id> --model %s=%s --allow-users <email>\n", models[0], saved.ID)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.provider, "provider", "", "Provider: "+surfacePresetNames())
	fl.StringSliceVar(&f.models, "models", nil, "Model names callers may use (comma separated)")
	fl.StringVar(&f.secretRef, "secret-ref", "", "Secret REFERENCE name holding the provider API key (never the key)")
	fl.StringVar(&f.origin, "origin", "", "Provider base URL (default: the provider's public API; required for openai-compatible)")
	fl.StringSliceVar(&f.operations, "operations", nil, "Operation IDs to allow instead of the defaults (see: aiplex llm providers <provider>)")
	fl.BoolVar(&f.update, "update", false, "Replace the surface if it exists")
	return cmd
}
