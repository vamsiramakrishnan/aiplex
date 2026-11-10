package api

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Provider-native caller credentials.
//
// An unmodified vendor SDK is pointed at an AIPlex base URL and given an AIPlex
// token as its "API key". Some SDKs and raw clients put that key in a
// provider-specific header instead of Authorization: Bearer. NativeKeyHeaders
// is a narrow adapter at the route boundary: on the provider-native model
// routes only, it moves the header that the called wire protocol documents
// into Authorization: Bearer, before identity extraction, and removes it. The
// value is then verified exactly like a bearer token by the unchanged
// auth.WIFValidator; no new credential type exists.
//
// Vendor documentation consulted (the header each protocol documents):
//   - Anthropic Messages: Authorization: Bearer, with x-api-key as the
//     "legacy fallback ... still supported"
//     https://platform.claude.com/docs/en/api/overview
//     (anthropic 0.40.0 sent X-Api-Key; current releases, e.g. 2.7.0, send the
//     key as Authorization: Bearer; both are accepted here.)
//   - Google GenAI: the x-goog-api-key header
//     https://aistudio.google.com/api-reference
//     (google-genai sets it for Developer API and Vertex express mode.)
//   - Azure OpenAI: the api-key header, or Authorization: Bearer for Entra
//     https://learn.microsoft.com/en-us/azure/foundry/openai/reference
//     (the openai package's AzureOpenAI client sends api-key.)
//
// OpenAI's SDKs send Authorization: Bearer, so OpenAI paths need no adapter;
// the access-point OpenAI wires (/ap/{id}/v1/... and the Azure-shaped
// /ap/{id}/openai/...) also accept the api-key header.
// AWS SDKs sign with SigV4 (not an AIPlex-accepted token) and are not handled.
//
// Never accepted: a token in a URL query parameter. Such requests on these
// routes are refused with a wire-shaped 401 before anything can log the value.

type nativeKeyKind int

const (
	nativeKeyNone nativeKeyKind = iota
	nativeKeyAnthropic
	nativeKeyGenAI
	nativeKeyAzure
)

var (
	// The native header each wire accepts as an alternative to Authorization.
	nativeKeyHeaderOf = map[nativeKeyKind]string{
		nativeKeyAnthropic: "X-Api-Key",
		nativeKeyGenAI:     "X-Goog-Api-Key",
		nativeKeyAzure:     "Api-Key",
	}
	// Every provider key header is removed on a model route so none can be
	// mistaken for, or forwarded as, a credential.
	nativeKeyAllHeaders = []string{"X-Api-Key", "X-Goog-Api-Key", "Api-Key"}
	// Query parameter names SDKs use for keys and tokens.
	nativeKeyQueryNames = map[string]bool{"key": true, "api_key": true, "api-key": true, "apikey": true, "access_token": true}
	genaiMethodSuffix   = regexp.MustCompile(`:(generateContent|streamGenerateContent|countTokens|embedContent|batchEmbedContents)$`)
)

// nativeKeyRoute classifies a request path. model reports whether the path is
// a provider-native model route at all; kind names the native header allowed.
// errWire is the wire protocol used to shape a refusal.
func nativeKeyRoute(r *http.Request) (model bool, kind nativeKeyKind, errWire string) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/ap/"):
		rest := strings.TrimPrefix(path, "/ap/")
		i := strings.IndexByte(rest, '/')
		if i <= 0 {
			return false, nativeKeyNone, ""
		}
		rest = rest[i:]
		switch {
		case rest == "/v1/messages" || rest == "/v1/messages/count_tokens":
			return true, nativeKeyAnthropic, WireAnthropic
		case rest == "/v1/models" && r.Header.Get("anthropic-version") != "":
			return true, nativeKeyAnthropic, WireAnthropic
		case strings.HasPrefix(rest, "/v1beta/") || rest == geminiLivePath:
			return true, nativeKeyGenAI, WireGenAI
		case strings.HasPrefix(rest, "/model/"):
			return true, nativeKeyNone, WireBedrockConverse
		case strings.HasPrefix(rest, "/openai/"), strings.HasPrefix(rest, "/v1/"):
			// OpenAI wires, including the Azure OpenAI SDK's /openai/... shapes
			// (access_point_azure.go): the Azure SDK's api-key header is
			// accepted as the AIPlex token. An Authorization header that
			// differs from it is refused, never merged.
			return true, nativeKeyAzure, WireOpenAIChat
		}
		return false, nativeKeyNone, ""
	case strings.HasPrefix(path, "/api/v1/llm/gateway/"):
		rest := strings.TrimPrefix(path, "/api/v1/llm/gateway/")
		i := strings.IndexByte(rest, '/')
		if i <= 0 {
			return true, nativeKeyNone, WireOpenAIChat
		}
		rest = rest[i:]
		switch {
		case rest == "/v1/messages" || rest == "/v1/messages/count_tokens":
			return true, nativeKeyAnthropic, WireAnthropic
		case strings.HasPrefix(rest, "/v1beta/") || rest == geminiLivePath || genaiMethodSuffix.MatchString(rest):
			return true, nativeKeyGenAI, WireGenAI
		case strings.HasPrefix(rest, "/openai/"):
			return true, nativeKeyAzure, WireOpenAIChat
		case strings.HasPrefix(rest, "/model/"):
			return true, nativeKeyNone, WireBedrockConverse
		}
		return true, nativeKeyNone, WireOpenAIChat
	}
	return false, nativeKeyNone, ""
}

// NativeKeyHeaders must be installed before the identity middleware.
func NativeKeyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model, kind, wire := nativeKeyRoute(r)
		if !model {
			next.ServeHTTP(w, r)
			return
		}
		// Reject URL-carried credentials; the value is never read into a message.
		if q, _ := url.ParseQuery(r.URL.RawQuery); len(q) > 0 {
			for name := range q {
				if nativeKeyQueryNames[strings.ToLower(name)] {
					hint := "the Authorization: Bearer header"
					if h := nativeKeyHeaderOf[kind]; h != "" {
						hint = "the " + h + " header or Authorization: Bearer"
					}
					writeNativeError(w, wire, http.StatusUnauthorized, "UNAUTHENTICATED", "credentials in the URL query are not accepted; send the AIPlex token in "+hint)
					return
				}
			}
		}
		present := false
		for _, name := range nativeKeyAllHeaders {
			if len(r.Header.Values(name)) > 0 {
				present = true
			}
		}
		if !present {
			next.ServeHTTP(w, r)
			return
		}
		r = r.Clone(r.Context())
		var native []string
		if name := nativeKeyHeaderOf[kind]; name != "" {
			native = r.Header.Values(name)
		}
		for _, name := range nativeKeyAllHeaders {
			r.Header.Del(name)
		}
		value, ok := nativeKeyValue(native)
		if !ok {
			writeNativeError(w, wire, http.StatusUnauthorized, "UNAUTHENTICATED", "conflicting credentials: the "+nativeKeyHeaderOf[kind]+" header was repeated with different values; send the AIPlex token once")
			return
		}
		if value == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Goog-Iap-Jwt-Assertion") != "" {
			writeNativeError(w, wire, http.StatusUnauthorized, "UNAUTHENTICATED", "conflicting credentials: send one credential")
			return
		}
		if existing := r.Header.Values("Authorization"); len(existing) > 0 {
			token, bearer := strings.CutPrefix(strings.Join(existing, ","), "Bearer ")
			if len(existing) != 1 || !bearer || subtle.ConstantTimeCompare([]byte(token), []byte(value)) != 1 {
				writeNativeError(w, wire, http.StatusUnauthorized, "UNAUTHENTICATED", nativeKeyConflictMessage(nativeKeyHeaderOf[kind]))
				return
			}
		}
		r.Header.Set("Authorization", "Bearer "+value)
		next.ServeHTTP(w, r)
	})
}

// nativeKeyValue returns the single trimmed value of a header, "" when empty
// or absent, and false when repeated values differ.
func nativeKeyValue(values []string) (string, bool) {
	out := ""
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if out != "" && v != out {
			return "", false
		}
		out = v
	}
	return out, true
}

// nativeKeyConflictMessage tells a caller who sent the AIPlex token in
// Authorization and a different value in a provider key header (Claude Code
// does this when its settings already hold ANTHROPIC_API_KEY) which header to
// remove. AIPlex never uses that value; it is dropped before identity
// extraction and appears in no log, record or span. It was, however, received
// by the gateway in transit, so the message says so without claiming more.
func nativeKeyConflictMessage(header string) string {
	return "conflicting credentials: this request has an Authorization header and a " + header + " header with different values. " +
		"Send the AIPlex token once: remove the " + header + " header (AIPlex does not use a provider key). " +
		"The value in " + header + " reached this gateway in this request; it is discarded and never logged, stored or forwarded, " +
		"so treat it as seen by the gateway operator in transit only."
}
