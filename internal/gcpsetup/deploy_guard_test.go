package gcpsetup

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// adminRoleAllowlist lists project-level admin roles deploy/ may grant, each
// with the reason. It is empty today; add an entry only with a comment that
// says why nothing narrower works.
var adminRoleAllowlist = map[string]string{}

const deployDir = "../../deploy"

func deployFiles(t *testing.T, exts ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(deployDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		for _, ext := range exts {
			if strings.HasSuffix(path, ext) {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(deployDir, path)
				out["deploy/"+filepath.ToSlash(rel)] = string(b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var (
	tfResourceStart = regexp.MustCompile(`(?m)^resource\s+"([a-z0-9_]+)"\s+"([A-Za-z0-9_-]+)"\s*\{`)
	roleAttr        = regexp.MustCompile(`(?m)^\s*role\s*=\s*"([^"]+)"`)
	basicRole       = regexp.MustCompile(`roles/(owner|editor)\b`)
	adminRole       = regexp.MustCompile(`^roles/[A-Za-z0-9_.]*(\.admin|Admin)$`)
)

// terraformBlocks splits HCL into top-level resource blocks by brace depth.
func terraformBlocks(src string) []struct{ typ, name, body string } {
	var out []struct{ typ, name, body string }
	for _, loc := range tfResourceStart.FindAllStringSubmatchIndex(src, -1) {
		typ, name := src[loc[2]:loc[3]], src[loc[4]:loc[5]]
		depth, end := 0, len(src)
		for i := loc[1] - 1; i < len(src); i++ {
			switch src[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				end = i + 1
				break
			}
		}
		out = append(out, struct{ typ, name, body string }{typ, name, src[loc[0]:end]})
	}
	return out
}

func TestDeployGrantsNoBroadRoles(t *testing.T) {
	for path, src := range deployFiles(t, ".tf", ".yaml", ".yml", ".tpl") {
		if m := basicRole.FindString(src); m != "" {
			t.Errorf("%s: grants or mentions basic role %s; use the narrowest predefined role", path, m)
		}
		if strings.Contains(src, "cluster-admin") {
			t.Errorf("%s: references cluster-admin", path)
		}
	}

	for path, src := range deployFiles(t, ".tf") {
		for _, b := range terraformBlocks(src) {
			if b.typ != "google_project_iam_member" && b.typ != "google_project_iam_binding" &&
				b.typ != "google_organization_iam_member" && b.typ != "google_organization_iam_binding" {
				continue
			}
			m := roleAttr.FindStringSubmatch(b.body)
			if m == nil {
				continue // role from a reference, e.g. a custom role id
			}
			role := m[1]
			if adminRole.MatchString(role) {
				if _, ok := adminRoleAllowlist[role]; !ok {
					t.Errorf("%s: %s.%s grants %s at project/organization level; grant on the resource or use a narrower role", path, b.typ, b.name, role)
				}
			}
			if role == "roles/secretmanager.secretAccessor" && !regexp.MustCompile(`(?m)^\s*condition\s*\{`).MatchString(b.body) {
				t.Errorf("%s: %s.%s grants secretAccessor on every secret in the project; bind per secret or add an IAM condition", path, b.typ, b.name)
			}
		}
		if regexp.MustCompile(`google_project_iam_(policy|binding)\b`).MatchString(src) {
			t.Errorf("%s: authoritative project IAM resources replace bindings made outside Terraform; use google_project_iam_member", path)
		}
	}
}

var (
	rbacKind     = regexp.MustCompile(`(?m)^kind:\s*(ClusterRole|ClusterRoleBinding|Role|RoleBinding)\s*$`)
	wildcardVerb = regexp.MustCompile(`(?m)^\s*-?\s*(verbs|resources|apiGroups)\s*:\s*\[[^\]]*"\*"[^\]]*\]|^\s*-\s*["']?\*["']?\s*$`)
)

func TestDeployKubernetesRBACHasNoWildcards(t *testing.T) {
	for path, src := range deployFiles(t, ".yaml", ".yml", ".tpl") {
		for _, doc := range strings.Split(src, "\n---") {
			if !rbacKind.MatchString(doc) {
				continue
			}
			if wildcardVerb.MatchString(doc) {
				t.Errorf("%s: RBAC rule with a wildcard: %s", path, strings.TrimSpace(wildcardVerb.FindString(doc)))
			}
		}
	}
}

// The guard itself must catch what it promises to catch.
func TestDeployGuardDetectsViolations(t *testing.T) {
	bad := `resource "google_project_iam_member" "x" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:a@b"
}
resource "google_project_iam_member" "y" {
  role = "roles/storage.admin"
}`
	blocks := terraformBlocks(bad)
	if len(blocks) != 2 || !strings.Contains(blocks[0].body, "secretAccessor") || strings.Contains(blocks[0].body, "storage.admin") {
		t.Fatalf("block split wrong: %+v", blocks)
	}
	if !adminRole.MatchString("roles/storage.admin") || !adminRole.MatchString("roles/datastore.importExportAdmin") || adminRole.MatchString("roles/datastore.user") {
		t.Fatal("admin role pattern wrong")
	}
	rbac := "kind: Role\nrules:\n- apiGroups: [\"\"]\n  verbs: [\"*\"]\n"
	if !wildcardVerb.MatchString(rbac) {
		t.Fatal("wildcard verbs not detected")
	}
	if !wildcardVerb.MatchString("kind: Role\nrules:\n- resources:\n  - \"*\"\n") {
		t.Fatal("block-style wildcard not detected")
	}
}
