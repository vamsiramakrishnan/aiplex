package gcpsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// REST answers the preflight's questions with Google's REST APIs and an
// OAuth2 token source. It holds no token itself; tokens are never logged,
// printed or placed in errors.
type REST struct {
	HTTP   *http.Client
	Tokens oauth2.TokenSource
	// QuotaProject, when set, is sent as x-goog-user-project (needed by some
	// APIs when the credentials are a user's).
	QuotaProject string
	// Endpoints, overridable in tests.
	ResourceManager, ServiceUsage, Billing, Storage, TokenInfo string
}

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// NewREST uses Application Default Credentials, the same credentials
// Terraform's Google provider uses (`gcloud auth application-default login`
// or GOOGLE_APPLICATION_CREDENTIALS).
func NewREST(ctx context.Context) (*REST, error) {
	creds, err := google.FindDefaultCredentials(ctx, cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("no Application Default Credentials (run: gcloud auth application-default login)")
	}
	r := &REST{HTTP: &http.Client{Timeout: 30 * time.Second}, Tokens: creds.TokenSource}
	var meta struct {
		QuotaProject string `json:"quota_project_id"`
	}
	if len(creds.JSON) > 0 && json.Unmarshal(creds.JSON, &meta) == nil {
		r.QuotaProject = meta.QuotaProject
	}
	return r, nil
}

func (r *REST) endpoint(v, def string) string {
	if v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return def
}

type googleError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

func (r *REST) do(ctx context.Context, method, u string, body any) ([]byte, int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.QuotaProject != "" {
		req.Header.Set("x-goog-user-project", r.QuotaProject)
	}
	tok, err := r.Tokens.Token()
	if err != nil {
		return nil, 0, fmt.Errorf("cannot obtain an access token from Application Default Credentials")
	}
	tok.SetAuthHeader(req)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %v", method, redactURL(u), err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return data, resp.StatusCode, apiError(resp.StatusCode, data)
	}
	return data, resp.StatusCode, nil
}

func redactURL(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i]
	}
	return u
}

func apiError(status int, data []byte) error {
	var ge googleError
	msg := ""
	if json.Unmarshal(data, &ge) == nil && ge.Error.Message != "" {
		msg = ge.Error.Message
		for _, d := range ge.Error.Details {
			if d.Reason == "SERVICE_DISABLED" {
				return fmt.Errorf("HTTP %d: %s: %w", status, truncate(msg, 300), ErrServiceDisabled)
			}
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Errorf("HTTP %d: %s", status, truncate(msg, 300))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestPermissions calls projects / organizations testIamPermissions
// (https://docs.cloud.google.com/resource-manager/reference/rest/v3/projects/testIamPermissions)
// or the Cloud Storage bucket testIamPermissions.
func (r *REST) TestPermissions(ctx context.Context, kind ResourceKind, name string, perms []string) ([]string, error) {
	if len(perms) > MaxPermissionsPerCall {
		return nil, fmt.Errorf("at most %d permissions per call", MaxPermissionsPerCall)
	}
	switch kind {
	case KindProject, KindOrganization:
		collection := "projects"
		if kind == KindOrganization {
			collection = "organizations"
		}
		u := fmt.Sprintf("%s/v3/%s/%s:testIamPermissions",
			r.endpoint(r.ResourceManager, "https://cloudresourcemanager.googleapis.com"), collection, url.PathEscape(name))
		data, _, err := r.do(ctx, http.MethodPost, u, map[string]any{"permissions": perms})
		if err != nil {
			return nil, err
		}
		var out struct {
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("unreadable testIamPermissions answer")
		}
		return out.Permissions, nil
	case KindBucket:
		q := url.Values{}
		for _, p := range perms {
			q.Add("permissions", p)
		}
		u := fmt.Sprintf("%s/storage/v1/b/%s/iam/testPermissions?%s",
			r.endpoint(r.Storage, "https://storage.googleapis.com"), url.PathEscape(name), q.Encode())
		data, _, err := r.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("unreadable bucket testIamPermissions answer")
		}
		return out.Permissions, nil
	}
	return nil, fmt.Errorf("unknown resource kind %q", kind)
}

// EnabledServices lists enabled services with Service Usage services.list.
func (r *REST) EnabledServices(ctx context.Context, project string) (map[string]bool, error) {
	out := map[string]bool{}
	page := ""
	for i := 0; i < 50; i++ {
		q := url.Values{"filter": {"state:ENABLED"}, "pageSize": {"200"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		u := fmt.Sprintf("%s/v1/projects/%s/services?%s",
			r.endpoint(r.ServiceUsage, "https://serviceusage.googleapis.com"), url.PathEscape(project), q.Encode())
		data, _, err := r.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Services []struct {
				Config struct {
					Name string `json:"name"`
				} `json:"config"`
				Name string `json:"name"`
			} `json:"services"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("unreadable services answer")
		}
		for _, s := range resp.Services {
			name := s.Config.Name
			if name == "" {
				name = s.Name[strings.LastIndex(s.Name, "/")+1:]
			}
			out[name] = true
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		page = resp.NextPageToken
	}
	return nil, fmt.Errorf("too many pages of services")
}

// BillingEnabled reads projects.getBillingInfo.
func (r *REST) BillingEnabled(ctx context.Context, project string) (bool, error) {
	u := fmt.Sprintf("%s/v1/projects/%s/billingInfo",
		r.endpoint(r.Billing, "https://cloudbilling.googleapis.com"), url.PathEscape(project))
	data, _, err := r.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	var out struct {
		BillingEnabled bool `json:"billingEnabled"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return false, fmt.Errorf("unreadable billing answer")
	}
	return out.BillingEnabled, nil
}

// BucketExists reads the bucket's metadata. 403 means it exists but is not
// readable by the caller.
func (r *REST) BucketExists(ctx context.Context, bucket string) (bool, error) {
	u := fmt.Sprintf("%s/storage/v1/b/%s?fields=name",
		r.endpoint(r.Storage, "https://storage.googleapis.com"), url.PathEscape(bucket))
	_, status, err := r.do(ctx, http.MethodGet, u, nil)
	switch {
	case err == nil:
		return true, nil
	case status == http.StatusNotFound:
		return false, nil
	case status == http.StatusForbidden:
		return true, nil
	}
	return false, err
}

// Account returns the e-mail of the identity behind the credentials, from
// Google's tokeninfo endpoint. The token is sent in a POST body, never in a
// URL. It returns "" when the e-mail is not available.
func (r *REST) Account(ctx context.Context) string {
	tok, err := r.Tokens.Token()
	if err != nil {
		return ""
	}
	form := url.Values{"access_token": {tok.AccessToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.endpoint(r.TokenInfo, "https://oauth2.googleapis.com/tokeninfo"), strings.NewReader(form.Encode()))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		Email string `json:"email"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil {
		return ""
	}
	return out.Email
}
