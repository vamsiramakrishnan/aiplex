package api

import (
	"net/http"
	"strings"
)

// Response headers of an access point (docs/access-points.md, "Response
// headers"). A reverse proxy in front of an access point reads them:
//
//   - X-Aiplex-Request-Id is on every response from a route under /ap/{id}/. Its
//     value is the request_id of the ledger record of the call. X-Request-Id
//     cannot carry it, because on a model response the provider's own
//     X-Request-Id replaces it (pkg/modelapi copyResponseHeaders).
//   - X-Aiplex-Error-Code (pkg/errorcode) is on a refusal that AIPlex itself
//     produced, and never on an error relayed from a provider. The producer of
//     the refusal sets it, with its own code, where it writes the refusal.
const (
	// AccessPointRequestIDHeader carries AIPlex's request id.
	AccessPointRequestIDHeader = "X-Aiplex-Request-Id"

	accessPointPathPrefix = "/ap/"
)

// accessPointRequest reports whether a request is served under an access
// point: the client-facing /ap/{id}/... path, or a managed request delegated
// from it (which carries the access point's call in its context).
func accessPointRequest(r *http.Request) bool {
	return r != nil && (strings.HasPrefix(r.URL.Path, accessPointPathPrefix) || accessPointCallFrom(r.Context()) != nil)
}
