package gcpsetup

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const terraformDir = "../../deploy/terraform"

var tfBlock = regexp.MustCompile(`(?m)^(resource|data)\s+"([a-z0-9_]+)"\s+"[A-Za-z0-9_-]+"`)

// terraformTypes returns every resource type and data source type ("data."
// prefixed) declared in deploy/terraform/*.tf.
func terraformTypes(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(terraformDir, "*.tf"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Terraform files found in %s", terraformDir)
	}
	out := map[string][]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range tfBlock.FindAllStringSubmatch(string(b), -1) {
			typ := m[2]
			if m[1] == "data" {
				typ = "data." + typ
			}
			out[typ] = append(out[typ], filepath.Base(f))
		}
	}
	return out
}

func TestEveryTerraformResourceTypeHasRequirements(t *testing.T) {
	declared := terraformTypes(t)
	inTable := map[string]bool{}
	for _, r := range Requirements {
		inTable[r.Resource] = true
		if _, ok := FeatureByID(r.Feature); !ok {
			t.Errorf("%s: unknown feature %q", r.Resource, r.Feature)
		}
		if len(r.Permissions) == 0 && r.Note == "" {
			t.Errorf("%s: no permissions and no note saying why", r.Resource)
		}
		if len(r.Permissions) > 0 && !strings.HasPrefix(r.Source, "https://docs.cloud.google.com/") {
			t.Errorf("%s: permissions need a Google documentation source, got %q", r.Resource, r.Source)
		}
	}
	var missing []string
	for typ, files := range declared {
		if !inTable[typ] {
			missing = append(missing, typ+" ("+strings.Join(files, ", ")+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("Terraform declares resource types with no entry in gcpsetup.Requirements; add their permissions:\n  %s",
			strings.Join(missing, "\n  "))
	}
	for typ := range inTable {
		if strings.HasPrefix(typ, "cli:") {
			continue
		}
		if _, ok := declared[typ]; !ok {
			t.Errorf("gcpsetup.Requirements has %s, which deploy/terraform no longer declares; remove it", typ)
		}
	}
}

func TestEveryPermissionHasANarrowestRole(t *testing.T) {
	for _, f := range Features {
		for _, perm := range FeaturePermissions(f.ID) {
			if narrowestRole[perm.Name] == "" {
				t.Errorf("%s: no narrowest predefined role recorded for %s", f.ID, perm.Name)
			}
			if perm.Level == LevelOrganization && !strings.HasPrefix(perm.Name, "iam.googleapis.com/workforcePool") {
				t.Errorf("%s: unexpected organization-level permission %s", f.ID, perm.Name)
			}
		}
	}
	for perm, role := range narrowestRole {
		if roleSize[role] == 0 {
			t.Errorf("role %s (for %s) has no recorded size", role, perm)
		}
	}
}

func TestTerraformAPIListMatchesTable(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(terraformDir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)resource "google_project_service" "apis" \{\s*for_each = toset\(\[(.*?)\]\)`).FindSubmatch(b)
	if block == nil {
		t.Fatal("main.tf: google_project_service.apis for_each list not found")
	}
	var got []string
	for _, m := range regexp.MustCompile(`"([a-z0-9.-]+)"`).FindAllSubmatch(block[1], -1) {
		got = append(got, string(m[1]))
	}
	sort.Strings(got)
	want := TerraformAPIs()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("main.tf enables\n  %v\nbut gcpsetup.TerraformAPIs() is\n  %v", got, want)
	}
	for _, a := range BootstrapAPIs {
		for _, g := range got {
			if g == a {
				t.Errorf("bootstrap API %s must not be in main.tf (Terraform cannot enable it before using it)", a)
			}
		}
	}
}

func TestDeployerRoleFileIsCurrent(t *testing.T) {
	path := filepath.Join("../..", DeployerRoleFile)
	if os.Getenv("AIPLEX_UPDATE_DEPLOYER_ROLE") == "1" {
		if err := os.WriteFile(path, []byte(DeployerRoleYAML()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (regenerate: AIPLEX_UPDATE_DEPLOYER_ROLE=1 go test ./internal/gcpsetup -run TestDeployerRoleFileIsCurrent)", err)
	}
	if string(b) != DeployerRoleYAML() {
		t.Errorf("%s is stale; regenerate with: AIPLEX_UPDATE_DEPLOYER_ROLE=1 go test ./internal/gcpsetup -run TestDeployerRoleFileIsCurrent", DeployerRoleFile)
	}
}
