package modelapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// Gateway-attributed upstream failures.
//
// A caller never supplies the credential a provider sees: the gateway presents
// its own. So a provider's 401 or 403 means the gateway's credential (a wrong,
// expired or unauthorized secret reference) was refused, which is not a failure
// of the caller's authentication. Relaying the provider's 401 would look like
// one, so the gateway answers 502 with an error in the protocol's own shape
// whose code says the upstream credential was rejected, and records the error
// class "upstream_auth". The provider's body (which could name a key prefix)
// is not relayed. Operators see the surface id in the error details;
// everyone else sees none.
//
// Providers that signal a quota or billing exhaustion distinctly are classed
// "upstream_quota_exhausted" instead of a plain rate limit; the provider's
// response still reaches the caller unchanged:
//
//   - OpenAI, Azure OpenAI and OpenAI-compatible: HTTP 429 whose error.code or
//     error.type is "insufficient_quota"
//     (https://platform.openai.com/docs/guides/error-codes: "You exceeded your
//     current quota, please check your plan and billing details").
//   - Bedrock: the exception type ServiceQuotaExceededException, sent in the
//     x-amzn-ErrorType header
//     (https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html).
//
// Anthropic and Gemini answer 429 for rate limits only (Gemini's
// RESOURCE_EXHAUSTED covers both rate and quota without a distinct code), so
// their 429s stay "rate_limited".
const (
	// ErrorClassUpstreamAuth marks the gateway's own credential being refused.
	ErrorClassUpstreamAuth = "upstream_auth"
	// ErrorClassUpstreamQuotaExhausted marks an exhausted provider quota or
	// balance, as distinct from a rate limit.
	ErrorClassUpstreamQuotaExhausted = "upstream_quota_exhausted"

	// UpstreamCredentialRejectedCode is the code in the error body.
	UpstreamCredentialRejectedCode = "upstream_credential_rejected"
	upstreamAuthMessage            = "The gateway's credential for the upstream provider was rejected, so the request was not processed. This is not a problem with the credential you sent; ask the operator of this gateway to check the provider credential."
	// maxRejectionPeek bounds the body read to classify a provider rejection.
	maxRejectionPeek = 64 << 10
)

func (g *Gateway) noteClass(r *http.Request, class string) {
	if g.options.NoteError != nil {
		g.options.NoteError(r.Context(), class)
	}
}

// upstreamAuthRejected reports whether a provider response says the gateway's
// credential was refused. 401 and 403 always do. The Gemini Developer API
// answers 400 INVALID_ARGUMENT with reason API_KEY_INVALID for a wrong or
// expired key (observed live, 2026-11-10); that body is peeked and put back.
func (g *Gateway) upstreamAuthRejected(response *http.Response) bool {
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		if g.surface.Provider == nil || g.surface.Provider.ID != ProviderGeminiDeveloper {
			return false
		}
		head, _ := peekBody(response, maxRejectionPeek)
		return bytes.Contains(head, []byte(`"API_KEY_INVALID"`))
	}
	return false
}

// peekBody reads up to limit bytes of the response body and puts them back in
// front of the rest, so the body can still be relayed whole.
func peekBody(response *http.Response, limit int64) ([]byte, error) {
	head, err := io.ReadAll(io.LimitReader(response.Body, limit))
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), response.Body), response.Body}
	return head, err
}

// handleUpstreamAuth answers a refused gateway credential with a protocol
// native 502 and returns true. It finishes the execution lease as a request the
// provider rejected before processing, like any 401 or 403.
func (g *Gateway) handleUpstreamAuth(w http.ResponseWriter, r *http.Request, run *executionRun, response *http.Response) bool {
	if !g.upstreamAuthRejected(response) {
		return false
	}
	if run != nil {
		if err := run.finish(execution.Result{StatusCode: response.StatusCode, ResponseComplete: true, Rejected: true}); err != nil {
			writeExecutionError(w, err)
			return true
		}
	}
	g.noteClass(r, ErrorClassUpstreamAuth)
	operator := g.options.OperatorDetail != nil && g.options.OperatorDetail(r.Context())
	g.writeUpstreamAuthError(w, operator)
	return true
}

// writeUpstreamAuthError writes the 502 body in the surface's native shape.
func (g *Gateway) writeUpstreamAuthError(w http.ResponseWriter, operator bool) {
	provider := ""
	if g.surface.Provider != nil {
		provider = g.surface.Provider.ID
	}
	if g.surface.BedrockMantle() {
		// The Mantle wire is the Anthropic Messages API: its clients (the
		// Anthropic SDK) read the Anthropic error shape.
		provider = ProviderAnthropic
	}
	details := map[string]string{}
	if operator {
		details["surface_id"] = g.surface.ID
	}
	var body any
	switch provider {
	case ProviderAnthropic:
		inner := map[string]any{"type": "api_error", "code": UpstreamCredentialRejectedCode, "message": upstreamAuthMessage}
		if operator {
			inner["details"] = details
		}
		body = map[string]any{"type": "error", "error": inner}
	case ProviderBedrock:
		w.Header().Set("X-Amzn-Errortype", "UpstreamCredentialRejected")
		out := map[string]any{"message": upstreamAuthMessage, "code": UpstreamCredentialRejectedCode}
		if operator {
			out["details"] = details
		}
		body = out
	case ProviderGeminiDeveloper, "":
		// Google RPC status shape: an ErrorInfo detail carries the reason.
		info := map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": strings.ToUpper(UpstreamCredentialRejectedCode), "domain": "aiplex"}
		if operator {
			info["metadata"] = details
		}
		body = map[string]any{"error": map[string]any{"code": http.StatusBadGateway, "message": upstreamAuthMessage, "status": "UNAVAILABLE", "details": []any{info}}}
	default:
		inner := map[string]any{"message": upstreamAuthMessage, "type": "server_error", "code": UpstreamCredentialRejectedCode, "param": nil}
		if operator {
			inner["details"] = details
		}
		body = map[string]any{"error": inner}
	}
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write(raw)
}

// noteRejectionClass records a distinct error class for a provider rejection
// that says the provider quota or balance is exhausted. The response is left
// to be relayed unchanged.
func (g *Gateway) noteRejectionClass(r *http.Request, response *http.Response) {
	if g.surface.Provider == nil {
		return
	}
	switch g.surface.Provider.ID {
	case ProviderOpenAI, ProviderAzureOpenAI, ProviderOpenAICompatible:
		if response.StatusCode != http.StatusTooManyRequests {
			return
		}
		head, _ := peekBody(response, maxRejectionPeek)
		var parsed struct {
			Error struct {
				Code any    `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(head, &parsed) != nil {
			return
		}
		if code, _ := parsed.Error.Code.(string); code == "insufficient_quota" || parsed.Error.Type == "insufficient_quota" {
			g.noteClass(r, ErrorClassUpstreamQuotaExhausted)
		}
	case ProviderBedrock:
		if strings.HasPrefix(response.Header.Get("X-Amzn-Errortype"), "ServiceQuotaExceededException") {
			g.noteClass(r, ErrorClassUpstreamQuotaExhausted)
		}
	}
}
