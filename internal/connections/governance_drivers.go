package connections

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	awsgovernance "github.com/vamsiramakrishnan/aiplex/pkg/governance/aws"
	"github.com/vamsiramakrishnan/aiplex/pkg/governance/entra"
	googlegovernance "github.com/vamsiramakrishnan/aiplex/pkg/governance/google"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

func providerOptions(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid native governance options")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing native governance options")
	}
	return nil
}

// GovernanceDriver supplies concrete native semantics. HTTP credentials and
// endpoint policy are bound by GovernanceClient, never by resource payloads.
func (r *Runtime) GovernanceDriver(spec GovernanceProvider) (nativeapi.Driver, string, error) {
	client, fingerprint, err := r.GovernanceClient(spec)
	if err != nil {
		return nil, "", err
	}
	var driver nativeapi.Driver
	switch spec.Type {
	case "google-agent-registry", "google-iap", "google-auth-manager":
		var cfg googlegovernance.Config
		if err = providerOptions(spec.Options, &cfg); err != nil {
			return nil, "", err
		}
		switch spec.Type {
		case "google-agent-registry":
			driver, err = googlegovernance.New(cfg, client)
		case "google-iap":
			driver, err = googlegovernance.NewIAP(cfg, client)
		default:
			driver, err = googlegovernance.NewIdentityManager(cfg, client)
		}
	case "aws-agentcore-identity":
		var cfg awsgovernance.Config
		if err = providerOptions(spec.Options, &cfg); err != nil {
			return nil, "", err
		}
		if cfg.Region != spec.Region {
			return nil, "", fmt.Errorf("AgentCore signing region and native authority must match")
		}
		driver, err = awsgovernance.New(cfg, client)
		if err == nil {
			stsOrigin := "https://sts." + spec.Region + ".amazonaws.com"
			if cfg.Partition == "aws-cn" {
				stsOrigin += ".cn"
			}
			if err = r.Policy.ValidateURL(stsOrigin); err != nil {
				return nil, "", fmt.Errorf("AgentCore governance requires allowlisted regional STS account verification")
			}
			if err = r.CheckDestination(spec.TenantID, spec.IdentityRef, stsOrigin); err != nil {
				return nil, "", fmt.Errorf("STS verification credential destination is not approved")
			}
			source, sourceErr := r.governanceAWSCredentials(spec)
			if sourceErr != nil {
				return nil, "", sourceErr
			}
			verifier, verifyErr := awsgovernance.NewSTSAccountVerifier(r.HTTP, source, spec.Region)
			if verifyErr != nil {
				return nil, "", verifyErr
			}
			driver, err = driver.(*awsgovernance.Driver).WithAccountVerifier(verifier)
		}
	case "entra-agent-id":
		var cfg entra.Config
		if err = providerOptions(spec.Options, &cfg); err != nil {
			return nil, "", err
		}
		driver, err = entra.New(cfg, client)
	default:
		return nil, "", fmt.Errorf("unsupported native governance provider type")
	}
	if err != nil {
		return nil, "", err
	}
	return driver, fingerprint, nil
}
