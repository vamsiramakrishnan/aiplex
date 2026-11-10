package directory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// oktaGroups lists the user's groups through the Okta management API.
//
// Request: GET {org}/api/v1/users/{id}/groups?limit=200. The id may be the
// user ID, login or an unambiguous login shortname; `after` is an opaque
// cursor carried in the Link header; limit max 200; required OAuth scope
// okta.users.read; response is an array of groups with id, profile and type.
// Confirmed against
// https://developer.okta.com/docs/api/openapi/okta-management/management/tags/userresources/other/listusergroups
//
// Paging: follow the Link header's rel="next" URL verbatim; the last page has
// no next link. Confirmed against https://developer.okta.com/docs/api/
// The next URL must stay on the configured org origin.
//
// Okta groups are not nested, so the direct list is the complete, expanded view.
//
// Credentials:
//   - API token: header "Authorization: SSWS {token}"; the token inherits the
//     privileges of the admin that created it. Confirmed against
//     https://developer.okta.com/docs/guides/create-an-api-token/main/
//   - OAuth service app: client credentials at {org}/oauth2/v1/token with
//     private_key_jwt (client_assertion signed RS256/ES256..., iss = sub =
//     client_id, aud = token endpoint, exp at most one hour), scope
//     okta.users.read, presented as a Bearer token. Confirmed against
//     https://developer.okta.com/docs/guides/implement-oauth-for-okta-serviceapp/main/
//     DPoP-bound service apps are not supported and fail with an explicit reason.
//
// Throttling (429) and every other non-200 fail the lookup.
func (a *Authority) oktaGroups(ctx context.Context, userID string) ([]string, error) {
	if userID == "" || len(userID) > 512 || strings.ContainsAny(userID, "/?#\\ \t\r\n") {
		return nil, fail("the verified subject cannot be used as an Okta user ID")
	}
	org := a.spec.OrgURL
	var authorize func(*http.Request)
	if ref := a.spec.Credential.APITokenRef; ref != "" {
		token, err := a.secret(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !headerSafe(token) {
			return nil, fail("the Okta API token secret is not a usable header value")
		}
		authorize = func(r *http.Request) { r.Header.Set("Authorization", "SSWS "+token) }
	} else {
		token, err := a.cachedToken(ctx, a.oktaToken)
		if err != nil {
			return nil, err
		}
		authorize = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	}
	next := org + "/api/v1/users/" + url.PathEscape(userID) + "/groups?limit=200"
	ids := []string{}
	seen := map[string]bool{}
	for page := 0; next != ""; page++ {
		if page >= maxPages || seen[next] {
			return nil, fail("Okta pagination did not terminate within %d pages", maxPages)
		}
		seen[next] = true
		header, body, err := a.get(ctx, next, org, authorize, "application/json")
		if err != nil {
			return nil, err
		}
		var groups *[]struct {
			ID      string `json:"id"`
			Profile *struct {
				Name string `json:"name"`
			} `json:"profile"`
		}
		if err := decode(body, &groups); err != nil {
			return nil, err
		}
		if groups == nil {
			return nil, fail("an Okta page was not a group array")
		}
		for _, g := range *groups {
			id := g.ID
			if a.spec.GroupID == GroupIDName {
				id = ""
				if g.Profile != nil {
					id = g.Profile.Name
				}
			}
			if id == "" {
				return nil, fail("Okta returned a group without its %s", a.spec.GroupID)
			}
			ids = append(ids, id)
			if len(ids) > maxGroups*4 {
				return nil, fail("the user is a member of more than %d groups; AIPlex refuses a truncated view", maxGroups)
			}
		}
		next, err = nextLink(header)
		if err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// nextLink returns the rel="next" target of an RFC 8288 Link header ("" when none).
func nextLink(header http.Header) (string, error) {
	next := ""
	for _, value := range header.Values("Link") {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			segments := strings.Split(part, ";")
			target := strings.TrimSpace(segments[0])
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				return "", fail("Okta returned a malformed Link header")
			}
			for _, param := range segments[1:] {
				param = strings.TrimSpace(param)
				if strings.EqualFold(param, `rel="next"`) || strings.EqualFold(param, "rel=next") {
					if next != "" {
						return "", fail("Okta returned more than one next link")
					}
					next = strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">")
				}
			}
		}
	}
	return next, nil
}

func (a *Authority) oktaToken(ctx context.Context) (string, time.Duration, error) {
	pem, err := a.secret(ctx, a.spec.Credential.PrivateKeyRef)
	if err != nil {
		return "", 0, err
	}
	endpoint := a.spec.OrgURL + "/oauth2/v1/token"
	now := a.deps.Clock()
	var jti [16]byte
	if _, err := rand.Read(jti[:]); err != nil {
		return "", 0, fail("could not create the client assertion")
	}
	claims := jwt.MapClaims{"iss": a.spec.Credential.ClientID, "sub": a.spec.Credential.ClientID, "aud": endpoint, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": hex.EncodeToString(jti[:])}
	var token *jwt.Token
	if key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pem)); err == nil {
		token = jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		if a.spec.Credential.KeyID != "" {
			token.Header["kid"] = a.spec.Credential.KeyID
		}
		assertion, err := token.SignedString(key)
		if err != nil {
			return "", 0, fail("could not sign the client assertion")
		}
		return a.oktaExchange(ctx, endpoint, assertion)
	}
	key, err := jwt.ParseECPrivateKeyFromPEM([]byte(pem))
	if err != nil || key.Curve.Params().Name != "P-256" {
		return "", 0, fail("the Okta private key secret must be a PEM RSA or P-256 EC private key")
	}
	token = jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	if a.spec.Credential.KeyID != "" {
		token.Header["kid"] = a.spec.Credential.KeyID
	}
	assertion, err := token.SignedString(key)
	if err != nil {
		return "", 0, fail("could not sign the client assertion")
	}
	return a.oktaExchange(ctx, endpoint, assertion)
}

func (a *Authority) oktaExchange(ctx context.Context, endpoint, assertion string) (string, time.Duration, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {"okta.users.read"}, "client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"}, "client_assertion": {assertion}}
	return a.postToken(ctx, endpoint, form)
}
