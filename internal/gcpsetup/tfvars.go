package gcpsetup

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// Toggles are the Terraform variables that switch features on and off.
type Toggles struct {
	ManageDNS      bool
	OrganizationID string
	AlertEmail     string
	OTelCollector  bool
	// Found reports whether a tfvars file was read.
	Found bool
}

// DefaultToggles are the Terraform variable defaults.
func DefaultToggles() Toggles { return Toggles{ManageDNS: true, OTelCollector: true} }

var tfvarLine = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$`)

// ReadToggles reads the feature variables from a terraform.tfvars file. It
// understands simple `name = "string"` and `name = true|false` lines (what
// aiplex init writes); anything else is ignored. A missing file returns the
// defaults with Found false.
func ReadToggles(path string) (Toggles, error) {
	t := DefaultToggles()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	defer f.Close()
	t.Found = true
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if i := strings.Index(line, "#"); i >= 0 && !strings.Contains(line[:i], `"`) {
			line = line[:i]
		}
		m := tfvarLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		value := strings.Trim(m[2], `"`)
		switch m[1] {
		case "manage_dns":
			t.ManageDNS = value != "false"
		case "organization_id":
			t.OrganizationID = value
		case "alert_email":
			t.AlertEmail = value
		case "otel_collector":
			t.OTelCollector = value != "false"
		}
	}
	return t, s.Err()
}

// EnabledFeatures returns the features the toggles switch on.
func EnabledFeatures(t Toggles) map[FeatureID]bool {
	return map[FeatureID]bool{
		FeatureState:      true,
		FeatureCore:       true,
		FeatureBackup:     true,
		FeatureBinaryAuth: true,
		FeatureDNS:        t.ManageDNS,
		FeatureWorkforce:  t.OrganizationID != "",
		FeatureAlertEmail: t.AlertEmail != "",
		FeatureTelemetry:  t.OTelCollector,
	}
}
