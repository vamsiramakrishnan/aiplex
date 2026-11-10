package gcpsetup

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReadTogglesClusterNetworkAndExternalAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfvars")

	// Unset: the original behaviour, nothing extra enabled.
	if err := os.WriteFile(path, []byte("project_id = \"p\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tg, _ := ReadToggles(path)
	f := EnabledFeatures(tg)
	if tg.ClusterNetwork != "default" || f[FeatureDedicatedNetwork] || f[FeatureExternalAgents] {
		t.Fatalf("defaults: %+v %+v", tg, f)
	}

	content := "cluster_network = \"aiplex\"\naws_account_id = \"210987654321\"\n# azure_agent_tenant_id = \"x\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tg, _ = ReadToggles(path)
	f = EnabledFeatures(tg)
	if tg.ClusterNetwork != "aiplex" || tg.AWSAccountID != "210987654321" || tg.AzureTenantID != "" {
		t.Fatalf("%+v", tg)
	}
	if !f[FeatureDedicatedNetwork] || !f[FeatureExternalAgents] {
		t.Fatalf("features not enabled: %+v", f)
	}

	// The Azure variable alone enables external agents too.
	if err := os.WriteFile(path, []byte("azure_agent_tenant_id = \"00000000-0000-0000-0000-000000000000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tg, _ = ReadToggles(path)
	if f = EnabledFeatures(tg); !f[FeatureExternalAgents] || f[FeatureDedicatedNetwork] {
		t.Fatalf("%+v", f)
	}
}

// The identity providers carry no placeholder values: each is created only
// when its variable is set, and every variable validates its input.
func TestIdentityProvidersHaveNoPlaceholders(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(terraformDir, "identity_pool.tf"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"123456789012", "AZURE_TENANT_ID", "Placeholder"} {
		if strings.Contains(s, bad) {
			t.Errorf("identity_pool.tf still contains the placeholder %q", bad)
		}
	}
	for _, res := range []string{"google_iam_workload_identity_pool_provider\" \"aws", "google_iam_workload_identity_pool_provider\" \"azure"} {
		i := strings.Index(s, res)
		if i < 0 {
			t.Fatalf("resource %s not found", res)
		}
		if !strings.Contains(s[i:i+200], "count") {
			t.Errorf("%s must be created conditionally (count)", res)
		}
	}
	for _, v := range []string{"aws_account_id", "azure_agent_tenant_id", "aws_agent_role", "cluster_network"} {
		all := ""
		files, _ := filepath.Glob(filepath.Join(terraformDir, "*.tf"))
		for _, f := range files {
			c, _ := os.ReadFile(f)
			all += string(c)
		}
		re := regexp.MustCompile(`(?s)variable "` + v + `" \{.*?\n\}`)
		block := re.FindString(all)
		if block == "" {
			t.Errorf("variable %s is not declared", v)
		} else if !strings.Contains(block, "validation") {
			t.Errorf("variable %s has no validation block", v)
		}
	}
}

// The default cluster network must stay the original one: an existing
// deployment's plan must not replace its cluster.
func TestClusterNetworkDefaultKeepsExistingCluster(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(terraformDir, "gke.tf"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !regexp.MustCompile(`(?s)variable "cluster_network" \{.*?default\s+=\s+"default"`).MatchString(s) {
		t.Error(`cluster_network must default to "default"`)
	}
	for _, line := range []string{"network    = local.dedicated_network ? google_compute_network.aiplex.id : null", "subnetwork = local.dedicated_network ? google_compute_subnetwork.aiplex.id : null"} {
		if !strings.Contains(s, line) {
			t.Errorf("gke.tf: %q missing; the cluster must leave network unset by default", line)
		}
	}
	for _, res := range []string{`"google_compute_router" "aiplex"`, `"google_compute_router_nat" "aiplex"`} {
		i := strings.Index(s, res)
		if i < 0 || !strings.Contains(s[i:i+120], "count") {
			t.Errorf("%s must exist only for the dedicated network", res)
		}
	}
}

// The chart's Gateway is wired to the Terraform-created load balancer pieces
// by name; a rename on one side must fail here.
func TestGatewayChartMatchesTerraformNames(t *testing.T) {
	values, err := os.ReadFile("../../deploy/helm/aiplex/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var tf string
	files, _ := filepath.Glob(filepath.Join(terraformDir, "*.tf"))
	for _, f := range files {
		c, _ := os.ReadFile(f)
		tf += string(c)
	}
	for key, tfName := range map[string]string{
		"addressName":      `name    = "aiplex-ip"`,
		"certMap":          `name    = "aiplex-cert-map"`,
		"cloudArmorPolicy": `name    = "aiplex-waf"`,
	} {
		m := regexp.MustCompile(`(?m)^\s+` + key + `:\s+(\S+)`).FindStringSubmatch(string(values))
		if m == nil {
			t.Errorf("values.yaml gateway.%s not found", key)
			continue
		}
		if !strings.Contains(tf, tfName) || !strings.Contains(tfName, `"`+m[1]+`"`) {
			t.Errorf("gateway.%s = %s does not match Terraform (%s)", key, m[1], tfName)
		}
	}
	tpl, err := os.ReadFile("../../deploy/helm/aiplex/templates/gateway.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: Gateway", "kind: GCPBackendPolicy", "name: aiplex-api-waf", "name: aiplex-console-waf", "kind: ReferenceGrant"} {
		if !strings.Contains(string(tpl), want) {
			t.Errorf("gateway.yaml template lacks %q", want)
		}
	}
	if strings.Contains(string(tpl), `"DOMAIN"`) {
		t.Error("gateway.yaml still carries the DOMAIN placeholder")
	}
	if _, err := os.Stat("../../deploy/k8s/gateway.yaml"); err == nil {
		t.Error("deploy/k8s/gateway.yaml was replaced by the chart template; remove it so the two cannot drift")
	}
}
