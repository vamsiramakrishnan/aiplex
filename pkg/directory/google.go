package directory

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// googleLabel selects Google Groups in searchTransitiveGroups. Security groups
// carry this label as well. Confirmed against
// https://docs.cloud.google.com/identity/docs/how-to/query-memberships
// ("member_key_id == '{}' && 'cloudidentity.googleapis.com/groups.discussion_forum' in labels").
const googleLabel = "cloudidentity.googleapis.com/groups.discussion_forum"

// googleGroups searches the member's transitive groups in Cloud Identity.
//
// Request: GET https://cloudidentity.googleapis.com/v1/groups/-/memberships:searchTransitiveGroups
// with query (CEL: member_key_id and a label), pageSize (default 200, max
// 1000) and pageToken; response memberships[].groupKey{id,namespace},
// relationType and nextPageToken (empty on the last page). Authorization
// scopes: cloud-identity.groups.readonly, cloud-identity.groups,
// cloud-identity or cloud-platform. The caller must be able to view the
// membership of every group in the result, otherwise the API answers 403
// PERMISSION_DENIED; the feature needs Google Workspace Enterprise Standard /
// Plus, Enterprise for Education or Cloud Identity Premium. Confirmed against
// https://docs.cloud.google.com/identity/docs/reference/rest/v1/groups.memberships/searchTransitiveGroups
//
// The access token comes from the deployment's gcp-adc identity profile
// (Application Default Credentials); its scopes are configured on the profile.
// The member key is the user's verified primary email (Cloud Identity
// documents the member key as, for example, the email address).
//
// Group IDs are the group's key ID (its email address); keys in a non-empty
// namespace (identity-mapped groups) are qualified as "namespace:id".
func (a *Authority) googleGroups(ctx context.Context, email string) ([]string, error) {
	if !validEmail(email) {
		return nil, fail("the verified token carries no verified email address to look the user up in Cloud Identity")
	}
	if a.deps.CheckDestination != nil {
		if err := a.deps.CheckDestination(a.target.Tenant, a.spec.Credential.IdentityRef, a.endpoints.CloudIdentity); err != nil {
			return nil, fail("identity profile %q is not approved for the Cloud Identity origin", a.spec.Credential.IdentityRef)
		}
	}
	token, err := a.deps.GoogleToken(ctx, a.target.Tenant, a.spec.Credential.IdentityRef)
	if err != nil || !headerSafe(token) {
		return nil, fail("no Google access token for identity profile %q", a.spec.Credential.IdentityRef)
	}
	base := strings.TrimSuffix(a.endpoints.CloudIdentity, "/") + "/v1/groups/-/memberships:searchTransitiveGroups"
	authorize := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	query := "member_key_id == '" + email + "' && '" + googleLabel + "' in labels"
	ids := []string{}
	pageToken := ""
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, fail("Cloud Identity pagination did not terminate within %d pages", maxPages)
		}
		params := url.Values{"query": {query}, "pageSize": {"1000"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		_, body, err := a.get(ctx, base+"?"+params.Encode(), a.endpoints.CloudIdentity, authorize, "application/json")
		if err != nil {
			return nil, err
		}
		var doc struct {
			Memberships []struct {
				GroupKey *struct {
					ID        string `json:"id"`
					Namespace string `json:"namespace"`
				} `json:"groupKey"`
			} `json:"memberships"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := decode(body, &doc); err != nil {
			return nil, err
		}
		for _, m := range doc.Memberships {
			if m.GroupKey == nil || m.GroupKey.ID == "" {
				return nil, fail("Cloud Identity returned a membership without a group key")
			}
			id := strings.ToLower(m.GroupKey.ID)
			if m.GroupKey.Namespace != "" {
				id = m.GroupKey.Namespace + ":" + m.GroupKey.ID
			}
			ids = append(ids, id)
			if len(ids) > maxGroups*4 {
				return nil, fail("the user is a member of more than %d groups; AIPlex refuses a truncated view", maxGroups)
			}
		}
		if doc.NextPageToken == "" {
			return ids, nil
		}
		if seen[doc.NextPageToken] {
			return nil, fail("Cloud Identity repeated a page token")
		}
		seen[doc.NextPageToken] = true
		pageToken = doc.NextPageToken
	}
}

// validEmail accepts a bounded address that cannot break out of the CEL string.
func validEmail(s string) bool {
	if len(s) < 3 || len(s) > 254 || strings.Count(s, "@") != 1 || strings.HasPrefix(s, "@") || strings.HasSuffix(s, "@") {
		return false
	}
	for _, r := range s {
		if r <= 0x20 || r >= 0x7f || r == '\'' || r == '"' || r == '\\' {
			return false
		}
	}
	return true
}
