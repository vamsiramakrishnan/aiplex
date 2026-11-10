package gcpsetup

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Advisory is shown with every preflight result. testIamPermissions answers
// from allow policies only.
const Advisory = "testIamPermissions is advisory: organization policy constraints, IAM deny policies, " +
	"VPC Service Controls perimeters, quotas and resource-specific conditions can still block an apply " +
	"that this check passes."

// MaxPermissionsPerCall bounds each testIamPermissions request.
const MaxPermissionsPerCall = 100

// ResourceKind is what a testIamPermissions call targets.
type ResourceKind string

const (
	KindProject      ResourceKind = "project"
	KindOrganization ResourceKind = "organization"
	KindBucket       ResourceKind = "bucket"
)

// ErrServiceDisabled marks an answer that failed because the API that would
// answer it is disabled in the project.
var ErrServiceDisabled = errors.New("service disabled")

// Google is everything the preflight asks Google. The REST implementation
// is NewREST; tests use a fake.
type Google interface {
	// TestPermissions returns the subset of perms the caller holds on the
	// resource. Implementations must not be passed more than
	// MaxPermissionsPerCall permissions.
	TestPermissions(ctx context.Context, kind ResourceKind, name string, perms []string) ([]string, error)
	// EnabledServices lists the services enabled in the project.
	EnabledServices(ctx context.Context, project string) (map[string]bool, error)
	// BillingEnabled reports whether the project has active billing.
	BillingEnabled(ctx context.Context, project string) (bool, error)
	// BucketExists reports whether a Cloud Storage bucket exists (a bucket the
	// caller cannot see but that exists counts as existing).
	BucketExists(ctx context.Context, bucket string) (bool, error)
}

// Config is the input to Run.
type Config struct {
	Project      string
	Organization string // required when FeatureWorkforce is enabled
	StateBucket  string // defaults to StateBucketName(Project)
	// Enabled lists the features to check; missing optional features are
	// skipped. Use EnabledFeatures to derive it from terraform.tfvars.
	Enabled map[FeatureID]bool
}

// StateBucketName is the bucket aiplex platform apply keeps Terraform state in.
func StateBucketName(project string) string { return project + "-aiplex-tfstate" }

// State of one check.
type State string

const (
	StateOK       State = "ok"
	StateMissing  State = "missing"
	StateUnknown  State = "unknown"  // could not check
	StateDisabled State = "disabled" // API disabled
	StateSkipped  State = "skipped"  // feature off
	StateWarning  State = "warning"
)

// PermissionResult is one permission that is missing or could not be checked.
type PermissionResult struct {
	Name       string `json:"name"`
	Level      Level  `json:"level"`
	Resource   string `json:"resource"`
	State      State  `json:"state"`
	Role       string `json:"narrowest_role,omitempty"`
	Unverified string `json:"unverified,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// FeatureResult is the outcome for one feature.
type FeatureResult struct {
	ID          FeatureID          `json:"feature"`
	Title       string             `json:"title"`
	Toggle      string             `json:"toggle"`
	State       State              `json:"state"`
	Checked     int                `json:"permissions_checked"`
	Problems    []PermissionResult `json:"problems,omitempty"`
	Unverified  int                `json:"unverified_permissions"`
	Explanation string             `json:"explanation,omitempty"`
}

// APIResult is the state of one API.
type APIResult struct {
	Name      string `json:"name"`
	Bootstrap bool   `json:"bootstrap"`
	State     State  `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

// Report is the result of Run.
type Report struct {
	Project       string          `json:"project"`
	Organization  string          `json:"organization,omitempty"`
	StateBucket   string          `json:"state_bucket"`
	OK            bool            `json:"ok"`
	Features      []FeatureResult `json:"features"`
	APIs          []APIResult     `json:"apis"`
	Billing       State           `json:"billing"`
	BillingReason string          `json:"billing_reason,omitempty"`
	Failures      []string        `json:"failures,omitempty"`
	Warnings      []string        `json:"warnings,omitempty"`
	Advisory      string          `json:"advisory"`
}

// Run checks every enabled feature. It never returns an error for a check
// that could not be made; that is recorded in the report as unknown and
// makes the report fail (fail closed).
func Run(ctx context.Context, g Google, cfg Config) (*Report, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("a project is required")
	}
	if cfg.StateBucket == "" {
		cfg.StateBucket = StateBucketName(cfg.Project)
	}
	r := &Report{Project: cfg.Project, Organization: cfg.Organization, StateBucket: cfg.StateBucket, Advisory: Advisory}

	// APIs first: a disabled bootstrap API explains every other failure.
	services, svcErr := g.EnabledServices(ctx, cfg.Project)
	for _, api := range AllAPIs() {
		res := APIResult{Name: api, Bootstrap: contains(BootstrapAPIs, api)}
		switch {
		case svcErr != nil && res.Bootstrap && errors.Is(svcErr, ErrServiceDisabled) && api == "serviceusage.googleapis.com":
			res.State, res.Reason = StateDisabled, "the Service Usage API answered that it is disabled"
		case svcErr != nil:
			res.State, res.Reason = StateUnknown, "cannot list enabled services: "+svcErr.Error()
		case services[api]:
			res.State = StateOK
		default:
			res.State = StateDisabled
		}
		r.APIs = append(r.APIs, res)
		switch {
		case res.Bootstrap && res.State == StateDisabled:
			r.Failures = append(r.Failures, fmt.Sprintf("API %s is disabled; Terraform needs it before it can enable anything else (gcloud services enable %s --project %s)", api, api, cfg.Project))
		case res.Bootstrap && res.State == StateUnknown:
			r.Failures = append(r.Failures, fmt.Sprintf("cannot check whether API %s is enabled", api))
		}
	}
	if svcErr != nil {
		r.Warnings = append(r.Warnings, "could not list enabled APIs ("+svcErr.Error()+"); Terraform-managed APIs are reported as unknown")
	}

	// Billing.
	billing, err := g.BillingEnabled(ctx, cfg.Project)
	switch {
	case err != nil:
		r.Billing, r.BillingReason = StateUnknown, err.Error()
		r.Warnings = append(r.Warnings, "cannot check billing: "+err.Error())
	case billing:
		r.Billing = StateOK
	default:
		r.Billing = StateMissing
		r.Failures = append(r.Failures, "billing is not enabled on project "+cfg.Project)
	}

	bucketExists, bucketErr := g.BucketExists(ctx, cfg.StateBucket)

	for _, f := range Features {
		fr := FeatureResult{ID: f.ID, Title: f.Title, Toggle: f.Toggle}
		if !cfg.Enabled[f.ID] {
			fr.State = StateSkipped
			r.Features = append(r.Features, fr)
			continue
		}
		perms := FeaturePermissions(f.ID)
		fr.Checked = len(perms)
		for _, perm := range perms {
			if perm.Unverified != "" {
				fr.Unverified++
			}
		}
		byTarget := map[target][]Permission{}
		var order []target
		for _, perm := range perms {
			t, why := resolveTarget(perm.Level, cfg, bucketExists, bucketErr)
			if why != "" {
				fr.Problems = append(fr.Problems, problem(perm, StateUnknown, "", why))
				continue
			}
			if _, ok := byTarget[t]; !ok {
				order = append(order, t)
			}
			byTarget[t] = append(byTarget[t], perm)
		}
		for _, t := range order {
			fr.Problems = append(fr.Problems, testTarget(ctx, g, t, byTarget[t])...)
		}
		fr.State = StateOK
		for _, pr := range fr.Problems {
			switch {
			case pr.State == StateUnknown:
				fr.State = StateUnknown
			case pr.State == StateMissing && pr.Unverified == "" && fr.State != StateUnknown:
				fr.State = StateMissing
			case pr.State == StateMissing && fr.State == StateOK:
				fr.State = StateWarning
			}
		}
		switch fr.State {
		case StateMissing:
			r.Failures = append(r.Failures, fmt.Sprintf("feature %s: missing permissions", f.ID))
		case StateUnknown:
			r.Failures = append(r.Failures, fmt.Sprintf("feature %s: some permissions could not be checked", f.ID))
		case StateWarning:
			r.Warnings = append(r.Warnings, fmt.Sprintf("feature %s: missing permissions whose need is unverified", f.ID))
		}
		r.Features = append(r.Features, fr)
	}
	r.OK = len(r.Failures) == 0
	return r, nil
}

type target struct {
	kind ResourceKind
	name string
}

func resolveTarget(level Level, cfg Config, bucketExists bool, bucketErr error) (target, string) {
	switch level {
	case LevelProject:
		return target{KindProject, cfg.Project}, ""
	case LevelOrganization:
		if cfg.Organization == "" {
			return target{}, "organization_id is not set; workforce pools are created in an organization"
		}
		return target{KindOrganization, cfg.Organization}, ""
	case LevelStateBucket:
		if bucketErr != nil {
			return target{}, "cannot check the state bucket: " + bucketErr.Error()
		}
		if bucketExists {
			return target{KindBucket, cfg.StateBucket}, ""
		}
		// Not created yet: the creator becomes able to write only through
		// project-level grants, so test there.
		return target{KindProject, cfg.Project}, ""
	}
	return target{}, "unknown level " + string(level)
}

func testTarget(ctx context.Context, g Google, t target, perms []Permission) []PermissionResult {
	var out []PermissionResult
	for start := 0; start < len(perms); start += MaxPermissionsPerCall {
		end := min(start+MaxPermissionsPerCall, len(perms))
		batch := perms[start:end]
		names := make([]string, len(batch))
		for i, perm := range batch {
			names[i] = perm.Name
		}
		granted, err := g.TestPermissions(ctx, t.kind, t.name, names)
		if err != nil {
			// One bad name fails a whole batch; retry one at a time so a single
			// unknown permission does not hide the answer for the rest.
			for _, perm := range batch {
				one, e := g.TestPermissions(ctx, t.kind, t.name, []string{perm.Name})
				switch {
				case e != nil:
					out = append(out, problem(perm, StateUnknown, describe(t), e.Error()))
				case !contains(one, perm.Name):
					out = append(out, problem(perm, StateMissing, describe(t), ""))
				}
			}
			continue
		}
		for _, perm := range batch {
			if !contains(granted, perm.Name) {
				out = append(out, problem(perm, StateMissing, describe(t), ""))
			}
		}
	}
	return out
}

func describe(t target) string {
	switch t.kind {
	case KindOrganization:
		return "organizations/" + t.name
	case KindBucket:
		return "gs://" + t.name
	}
	return "projects/" + t.name
}

func problem(perm Permission, s State, resource, reason string) PermissionResult {
	return PermissionResult{Name: perm.Name, Level: perm.Level, Resource: resource, State: s,
		Role: narrowestRole[perm.Name], Unverified: perm.Unverified, Reason: reason}
}

// NarrowestRole returns the narrowest predefined role holding perm, or "".
func NarrowestRole(perm string) string { return narrowestRole[perm] }

// RoleSize returns the number of permissions in a predefined role from
// Google's reference, or 0 when unknown.
func RoleSize(role string) int { return roleSize[role] }

// MissingRoles groups the missing permissions of a report by the role that
// would grant them, per resource. The map key is the resource
// ("projects/P", "organizations/O", "gs://B").
func (r *Report) MissingRoles() map[string][]string {
	set := map[string]map[string]bool{}
	for _, f := range r.Features {
		for _, pr := range f.Problems {
			if pr.State != StateMissing || pr.Role == "" {
				continue
			}
			if set[pr.Resource] == nil {
				set[pr.Resource] = map[string]bool{}
			}
			set[pr.Resource][pr.Role] = true
		}
	}
	out := map[string][]string{}
	for res, roles := range set {
		out[res] = sortedKeys(roles)
	}
	return out
}

// FeatureRoles returns, for a feature, the distinct narrowest predefined roles
// that together grant all its permissions, by level. It is the documented
// alternative to the custom deployer role.
func FeatureRoles(id FeatureID) map[Level][]string {
	set := map[Level]map[string]bool{}
	for _, perm := range FeaturePermissions(id) {
		role := narrowestRole[perm.Name]
		if role == "" {
			continue
		}
		if set[perm.Level] == nil {
			set[perm.Level] = map[string]bool{}
		}
		set[perm.Level][role] = true
	}
	out := map[Level][]string{}
	for l, roles := range set {
		out[l] = sortedKeys(roles)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Member returns the IAM member string for an account e-mail.
func Member(account string) string {
	if account == "" {
		return "user:YOUR_ACCOUNT"
	}
	if strings.HasSuffix(account, ".gserviceaccount.com") {
		return "serviceAccount:" + account
	}
	return "user:" + account
}

// BindingCommands returns gcloud commands that grant roles to member, one per
// role and resource, sorted.
func BindingCommands(byResource map[string][]string, member string) []string {
	var out []string
	resources := make([]string, 0, len(byResource))
	for res := range byResource {
		resources = append(resources, res)
	}
	sort.Strings(resources)
	for _, res := range resources {
		for _, role := range byResource[res] {
			out = append(out, bindingCommand(res, role, member))
		}
	}
	return out
}

func bindingCommand(resource, role, member string) string {
	switch {
	case strings.HasPrefix(resource, "organizations/"):
		return fmt.Sprintf("gcloud organizations add-iam-policy-binding %s --member=%s --role=%s --condition=None",
			strings.TrimPrefix(resource, "organizations/"), member, role)
	case strings.HasPrefix(resource, "gs://"):
		return fmt.Sprintf("gcloud storage buckets add-iam-policy-binding %s --member=%s --role=%s", resource, member, role)
	}
	return fmt.Sprintf("gcloud projects add-iam-policy-binding %s --member=%s --role=%s --condition=None",
		strings.TrimPrefix(resource, "projects/"), member, role)
}
