package directory

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// entraGroups lists the user's transitive group memberships with an app-only
// Microsoft Graph token.
//
// Token: OAuth 2.0 client credentials with a shared secret against the
// provider's pinned tenant, scope https://graph.microsoft.com/.default.
// Confirmed against
// https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-client-creds-grant-flow
// (POST https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token with
// client_id, scope, client_secret, grant_type=client_credentials; response
// token_type/expires_in/access_token).
//
// Membership: GET https://graph.microsoft.com/v1.0/users/{id}/transitiveMemberOf
// returns groups, directory roles and administrative units the user is a
// direct or transitive member of. Least-privileged application permission:
// User.Read.All (GroupMember.Read.All, Group.Read.All and Directory.Read.All
// also work). Default page size 100. Confirmed against
// https://learn.microsoft.com/en-us/graph/api/user-list-transitivememberof?view=graph-rest-1.0
// No query parameters are sent: the documentation states that query
// parameters on this API (including the microsoft.graph.group cast) need
// advanced query capabilities (ConsistencyLevel: eventual), which read an
// index that "might not be up-to-date"; AIPlex wants the current view and
// filters on @odata.type itself.
//
// Paging: follow @odata.nextLink verbatim until it is absent. Confirmed against
// https://learn.microsoft.com/en-us/graph/paging
// The link must stay on the Graph origin; otherwise the token is not sent and
// the lookup fails.
//
// Throttling (429) and every other non-200 fail the lookup; nothing partial is
// returned. Throttling guidance: https://learn.microsoft.com/en-us/graph/throttling
// (not re-checked for this change; AIPlex does not retry, it fails closed).
func (a *Authority) entraGroups(ctx context.Context, objectID string) ([]string, error) {
	objectID = strings.ToLower(objectID)
	if !guid.MatchString(objectID) {
		return nil, fail("the verified subject is not an Entra object ID (oid)")
	}
	token, err := a.cachedToken(ctx, a.entraToken)
	if err != nil {
		return nil, err
	}
	graph := strings.TrimSuffix(a.endpoints.Graph, "/")
	next := graph + "/v1.0/users/" + url.PathEscape(objectID) + "/transitiveMemberOf"
	authorize := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	var ids []string
	seenLinks := map[string]bool{}
	for page := 0; next != ""; page++ {
		if page >= maxPages || seenLinks[next] {
			return nil, fail("Microsoft Graph pagination did not terminate within %d pages", maxPages)
		}
		seenLinks[next] = true
		_, body, err := a.get(ctx, next, graph, authorize, "application/json")
		if err != nil {
			return nil, err
		}
		var doc struct {
			Value    *[]map[string]any `json:"value"`
			NextLink string            `json:"@odata.nextLink"`
		}
		if err := decode(body, &doc); err != nil {
			return nil, err
		}
		if doc.Value == nil {
			return nil, fail("a Microsoft Graph page had no value array")
		}
		for _, object := range *doc.Value {
			kind, _ := object["@odata.type"].(string)
			id, _ := object["id"].(string)
			if kind == "" || id == "" {
				return nil, fail("Microsoft Graph returned a membership entry without a type or ID")
			}
			if kind != "#microsoft.graph.group" {
				// Directory roles and administrative units are not groups.
				continue
			}
			id = strings.ToLower(id)
			if !guid.MatchString(id) {
				return nil, fail("Microsoft Graph returned a group whose ID is not a GUID")
			}
			ids = append(ids, id)
			if len(ids) > maxGroups*4 {
				return nil, fail("the user is a member of more than %d groups; AIPlex refuses a truncated view", maxGroups)
			}
		}
		next = doc.NextLink
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func (a *Authority) entraToken(ctx context.Context) (string, time.Duration, error) {
	secret, err := a.secret(ctx, a.spec.Credential.ClientSecretRef)
	if err != nil {
		return "", 0, err
	}
	endpoint := strings.TrimSuffix(a.endpoints.EntraLogin, "/") + "/" + a.target.EntraTenant + "/oauth2/v2.0/token"
	form := url.Values{"client_id": {a.spec.Credential.ClientID}, "scope": {"https://graph.microsoft.com/.default"}, "client_secret": {secret}, "grant_type": {"client_credentials"}}
	return a.postToken(ctx, endpoint, form)
}
