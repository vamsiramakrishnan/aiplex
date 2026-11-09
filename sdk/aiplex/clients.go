package aiplex

import (
	"context"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"net/http"
	"regexp"
)

type ClientProfile = clientplex.Profile
type ClientRegistration = clientplex.Registration
type ClientConnectionPlan = clientplex.Plan
type ClientRegistrationRecord = resources.Record

var clientIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func clientRegistrationPath(id string) (string, error) {
	if !clientIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid client registration id")
	}
	return "/api/v1/clients/registrations/" + id, nil
}
func (c *Client) ListClientProfiles(ctx context.Context) ([]ClientProfile, error) {
	var result []ClientProfile
	e := c.do(ctx, http.MethodGet, "/api/v1/clients/profiles", nil, &result)
	return result, e
}
func (c *Client) ListClientRegistrations(ctx context.Context) ([]ClientRegistrationRecord, error) {
	var result []ClientRegistrationRecord
	e := c.do(ctx, http.MethodGet, "/api/v1/clients/registrations", nil, &result)
	return result, e
}
func (c *Client) GetClientRegistration(ctx context.Context, id string) (ClientRegistrationRecord, error) {
	path, e := clientRegistrationPath(id)
	if e != nil {
		return ClientRegistrationRecord{}, e
	}
	var result ClientRegistrationRecord
	e = c.do(ctx, http.MethodGet, path, nil, &result)
	return result, e
}
func (c *Client) PutClientRegistration(ctx context.Context, r ClientRegistration, expected uint64) (ClientRegistrationRecord, error) {
	path, e := clientRegistrationPath(r.ID)
	if e != nil {
		return ClientRegistrationRecord{}, e
	}
	headers := map[string]string{}
	if expected > 0 {
		headers["If-Match"] = fmt.Sprintf("\"%d\"", expected)
	}
	var result ClientRegistrationRecord
	e = c.doWithHeaders(ctx, http.MethodPut, path, r, &result, headers)
	return result, e
}
func (c *Client) DeleteClientRegistration(ctx context.Context, id string, expected uint64) error {
	path, e := clientRegistrationPath(id)
	if e != nil {
		return e
	}
	if expected == 0 {
		return fmt.Errorf("positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, path, nil, nil, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", expected)})
}
func (c *Client) PlanClientConnection(ctx context.Context, id string) (ClientConnectionPlan, error) {
	path, e := clientRegistrationPath(id)
	if e != nil {
		return ClientConnectionPlan{}, e
	}
	var result ClientConnectionPlan
	e = c.do(ctx, http.MethodPost, path+"/plan", nil, &result)
	return result, e
}
