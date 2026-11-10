package gcpsetup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

type fakeGoogle struct {
	mu        sync.Mutex
	granted   map[string]map[string]bool // resource -> permission
	invalid   map[string]bool            // permissions the API rejects
	services  map[string]bool
	svcErr    error
	billing   bool
	billErr   error
	bucket    bool
	calls     []int
	failBatch bool
}

func (f *fakeGoogle) TestPermissions(_ context.Context, kind ResourceKind, name string, perms []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(perms) > MaxPermissionsPerCall {
		return nil, fmt.Errorf("too many permissions: %d", len(perms))
	}
	f.calls = append(f.calls, len(perms))
	for _, p := range perms {
		if f.invalid[p] {
			return nil, fmt.Errorf("HTTP 400: permission %s is not valid", p)
		}
	}
	key := string(kind) + "/" + name
	var out []string
	for _, p := range perms {
		if f.granted[key][p] {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeGoogle) EnabledServices(context.Context, string) (map[string]bool, error) {
	return f.services, f.svcErr
}
func (f *fakeGoogle) BillingEnabled(context.Context, string) (bool, error) {
	return f.billing, f.billErr
}
func (f *fakeGoogle) BucketExists(context.Context, string) (bool, error) { return f.bucket, nil }

func allGranted(project string, enabled map[FeatureID]bool) map[string]map[string]bool {
	g := map[string]map[string]bool{"project/" + project: {}, "organization/123": {}, "bucket/" + StateBucketName(project): {}}
	for _, f := range Features {
		if !enabled[f.ID] {
			continue
		}
		for _, perm := range FeaturePermissions(f.ID) {
			switch perm.Level {
			case LevelOrganization:
				g["organization/123"][perm.Name] = true
			case LevelStateBucket:
				g["bucket/"+StateBucketName(project)][perm.Name] = true
				g["project/"+project][perm.Name] = true
			default:
				g["project/"+project][perm.Name] = true
			}
		}
	}
	return g
}

func allServices() map[string]bool {
	s := map[string]bool{}
	for _, a := range AllAPIs() {
		s[a] = true
	}
	return s
}

func defaultEnabled() map[FeatureID]bool { return EnabledFeatures(DefaultToggles()) }

func TestPreflightPassesWhenEverythingIsGranted(t *testing.T) {
	en := defaultEnabled()
	f := &fakeGoogle{granted: allGranted("p", en), services: allServices(), billing: true, bucket: true}
	r, err := Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK {
		t.Fatalf("expected OK, failures %v", r.Failures)
	}
	for _, n := range f.calls {
		if n > MaxPermissionsPerCall {
			t.Fatalf("batch of %d", n)
		}
	}
	for _, fr := range r.Features {
		if fr.ID == FeatureWorkforce && fr.State != StateSkipped {
			t.Errorf("workforce should be skipped without organization_id, got %s", fr.State)
		}
	}
	if r.Advisory == "" || !strings.Contains(r.Advisory, "advisory") {
		t.Error("report must carry the advisory note")
	}
}

func TestPreflightReportsMissingPermissionWithNarrowestRole(t *testing.T) {
	en := defaultEnabled()
	g := allGranted("p", en)
	delete(g["project/p"], "resourcemanager.projects.setIamPolicy")
	f := &fakeGoogle{granted: g, services: allServices(), billing: true, bucket: true}
	r, _ := Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if r.OK {
		t.Fatal("expected failure")
	}
	var found *PermissionResult
	for _, fr := range r.Features {
		for i, pr := range fr.Problems {
			if pr.Name == "resourcemanager.projects.setIamPolicy" {
				found = &fr.Problems[i]
				if fr.State != StateMissing {
					t.Errorf("feature %s state %s, want missing", fr.ID, fr.State)
				}
			}
		}
	}
	if found == nil || found.Role != "roles/resourcemanager.projectIamAdmin" || found.Resource != "projects/p" {
		t.Fatalf("missing permission not reported with its role: %+v", found)
	}
	cmds := BindingCommands(r.MissingRoles(), Member("me@example.com"))
	want := "gcloud projects add-iam-policy-binding p --member=user:me@example.com --role=roles/resourcemanager.projectIamAdmin --condition=None"
	if len(cmds) != 1 || cmds[0] != want {
		t.Fatalf("binding commands %q", cmds)
	}
}

func TestPreflightUnverifiedMissingIsAWarningNotAFailure(t *testing.T) {
	en := defaultEnabled()
	g := allGranted("p", en)
	delete(g["project/p"], "compute.networks.updatePolicy") // unverified need
	f := &fakeGoogle{granted: g, services: allServices(), billing: true, bucket: true}
	r, _ := Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if !r.OK {
		t.Fatalf("unverified permission should not fail: %v", r.Failures)
	}
	if len(r.Warnings) == 0 {
		t.Fatal("expected a warning")
	}
}

func TestPreflightCannotCheckFailsClosed(t *testing.T) {
	en := defaultEnabled()
	f := &fakeGoogle{granted: allGranted("p", en), services: allServices(), billing: true, bucket: true,
		invalid: map[string]bool{"datastore.schemas.create": true}}
	r, _ := Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if r.OK {
		t.Fatal("a permission that could not be checked must fail the preflight")
	}
	unknown := 0
	for _, fr := range r.Features {
		for _, pr := range fr.Problems {
			if pr.State == StateUnknown {
				unknown++
				if pr.Name != "datastore.schemas.create" {
					t.Errorf("only the invalid permission should be unknown, got %s", pr.Name)
				}
			}
		}
	}
	if unknown != 1 {
		t.Fatalf("unknown = %d, want 1 (batch retried one by one)", unknown)
	}
}

func TestPreflightAPIStates(t *testing.T) {
	en := defaultEnabled()
	svcs := allServices()
	delete(svcs, "dns.googleapis.com")
	f := &fakeGoogle{granted: allGranted("p", en), services: svcs, billing: true, bucket: true}
	r, _ := Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if !r.OK {
		t.Fatalf("a disabled Terraform-managed API is not a failure: %v", r.Failures)
	}
	for _, a := range r.APIs {
		if a.Name == "dns.googleapis.com" && a.State != StateDisabled {
			t.Errorf("dns state %s", a.State)
		}
	}

	delete(svcs, "cloudresourcemanager.googleapis.com")
	r, _ = Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if r.OK {
		t.Fatal("a disabled bootstrap API must fail")
	}

	f.svcErr = fmt.Errorf("HTTP 403: denied")
	r, _ = Run(context.Background(), f, Config{Project: "p", Enabled: en})
	for _, a := range r.APIs {
		if a.State != StateUnknown {
			t.Errorf("%s: cannot-check must read unknown, got %s", a.Name, a.State)
		}
	}
	if r.OK {
		t.Fatal("unknown bootstrap API state must fail")
	}

	f.svcErr = fmt.Errorf("HTTP 403: x: %w", ErrServiceDisabled)
	r, _ = Run(context.Background(), f, Config{Project: "p", Enabled: en})
	for _, a := range r.APIs {
		if a.Name == "serviceusage.googleapis.com" && a.State != StateDisabled {
			t.Errorf("serviceusage should read disabled, got %s", a.State)
		}
	}
}

func TestPreflightBilling(t *testing.T) {
	en := defaultEnabled()
	f := &fakeGoogle{granted: allGranted("p", en), services: allServices(), billing: false, bucket: true}
	r, _ := Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if r.OK || r.Billing != StateMissing {
		t.Fatalf("billing off must fail, got %s", r.Billing)
	}
	f.billErr = errors.New("HTTP 403")
	r, _ = Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if r.Billing != StateUnknown || !r.OK {
		t.Fatalf("billing unknown is a warning: %s ok=%v %v", r.Billing, r.OK, r.Failures)
	}
}

func TestPreflightWorkforceNeedsOrganization(t *testing.T) {
	en := EnabledFeatures(Toggles{ManageDNS: true, OrganizationID: "123"})
	f := &fakeGoogle{granted: allGranted("p", en), services: allServices(), billing: true, bucket: false}
	r, _ := Run(context.Background(), f, Config{Project: "p", Organization: "123", Enabled: en})
	if !r.OK {
		t.Fatalf("expected OK: %v", r.Failures)
	}
	r, _ = Run(context.Background(), f, Config{Project: "p", Enabled: en})
	if r.OK {
		t.Fatal("workforce enabled without an organization must fail closed")
	}
}

func TestReadToggles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfvars")
	content := "project_id = \"p\"\nmanage_dns = false # external\norganization_id = \"42\"\n# alert_email = \"x@y\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tg, err := ReadToggles(path)
	if err != nil {
		t.Fatal(err)
	}
	if tg.ManageDNS || tg.OrganizationID != "42" || tg.AlertEmail != "" || !tg.Found {
		t.Fatalf("%+v", tg)
	}
	tg, _ = ReadToggles(filepath.Join(dir, "absent"))
	if !tg.ManageDNS || tg.Found {
		t.Fatalf("defaults expected: %+v", tg)
	}
}

func TestRESTNeverPutsTheTokenInErrorsOrURLs(t *testing.T) {
	const secret = "ya29.secret-token-value"
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if strings.Contains(r.URL.String(), secret) {
			t.Error("token in URL")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, ":testIamPermissions"):
			w.Write([]byte(`{"permissions":["a.b.c"]}`))
		case strings.Contains(r.URL.Path, "/services"):
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":403,"message":"Service Usage API has not been used","status":"PERMISSION_DENIED","details":[{"reason":"SERVICE_DISABLED"}]}}`))
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	r := &REST{HTTP: srv.Client(), Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: secret}),
		ResourceManager: srv.URL, ServiceUsage: srv.URL, Billing: srv.URL, Storage: srv.URL}
	got, err := r.TestPermissions(context.Background(), KindProject, "p", []string{"a.b.c", "d.e.f"})
	if err != nil || len(got) != 1 || got[0] != "a.b.c" {
		t.Fatalf("%v %v", got, err)
	}
	if sawAuth != "Bearer "+secret {
		t.Fatalf("auth header not sent")
	}
	_, err = r.EnabledServices(context.Background(), "p")
	if !errors.Is(err, ErrServiceDisabled) {
		t.Fatalf("want ErrServiceDisabled, got %v", err)
	}
	_, err = r.BillingEnabled(context.Background(), "p")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error must not carry the token: %v", err)
	}
	if _, err := r.TestPermissions(context.Background(), KindProject, "p", make([]string, 101)); err == nil {
		t.Fatal("more than 100 permissions per call must be refused")
	}
}

func TestPredefinedBindingsCoverEveryEnabledFeature(t *testing.T) {
	en := EnabledFeatures(Toggles{ManageDNS: true, OrganizationID: "42"})
	out := PredefinedBindings("p", "42", "user:me@example.com", en)
	for _, want := range []string{
		"gcloud organizations add-iam-policy-binding 42 --member=user:me@example.com --role=roles/iam.workforcePoolAdmin",
		"--role=roles/container.clusterAdmin",
		"--role=roles/resourcemanager.projectIamAdmin",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("bindings missing %q", want)
		}
	}
	if strings.Contains(out, "roles/owner") || strings.Contains(out, "roles/editor") {
		t.Error("basic roles must never be suggested")
	}
}
