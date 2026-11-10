package gcpsetup

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	manifestKind = regexp.MustCompile(`APIVersion:\s*"([^"]+)",\s*\n\s*Kind:\s*"([A-Za-z]+)"`)
	roleRule     = regexp.MustCompile(`(?m)^- apiGroups: \[([^\]]*)\]\n\s+resources: \[([^\]]*)\]\n\s+verbs: \[([^\]]*)\]`)
)

func plural(kind string) string {
	k := strings.ToLower(kind)
	if strings.HasSuffix(k, "y") {
		return k[:len(k)-1] + "ies"
	}
	return k + "s"
}

func unquote(list string) []string {
	var out []string
	for _, f := range strings.Split(list, ",") {
		out = append(out, strings.Trim(strings.TrimSpace(f), `"`))
	}
	return out
}

// The chart's Role for the API must grant exactly the kinds the deploy engine
// renders: a kind the code adds without RBAC fails in the cluster, and a kind
// in the Role the code never renders is privilege nobody uses.
func TestHelmAPIRoleMatchesDeployEngine(t *testing.T) {
	files, _ := filepath.Glob("../deploy/*.go")
	want := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range manifestKind.FindAllStringSubmatch(string(b), -1) {
			group := ""
			if i := strings.Index(m[1], "/"); i >= 0 {
				group = m[1][:i]
			}
			want[group+"/"+plural(m[2])] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("no manifests found in internal/deploy")
	}

	b, err := os.ReadFile("../../deploy/helm/aiplex/templates/api-rbac.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	wantVerbs := "create,delete,patch" // server-side apply (patch, create) and cleanup (delete); no reads
	for _, m := range roleRule.FindAllStringSubmatch(string(b), -1) {
		verbs := unquote(m[3])
		sort.Strings(verbs)
		if strings.Join(verbs, ",") != wantVerbs {
			t.Errorf("rule %s/%s verbs %v, want exactly %s", m[1], m[2], verbs, wantVerbs)
		}
		for _, g := range unquote(m[1]) {
			for _, r := range unquote(m[2]) {
				got[g+"/"+r] = true
			}
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("deploy engine renders %s but the API Role does not grant it", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("API Role grants %s, which the deploy engine never renders", k)
		}
	}
	if strings.Contains(string(b), "ClusterRole") && !strings.Contains(string(b), "No ClusterRole") {
		t.Error("the API's deploy RBAC must stay namespaced")
	}
}
