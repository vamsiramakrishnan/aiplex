package quotaplex

import (
	"errors"
	"fmt"
)

var ErrModelVersionProof = errors.New("terminal model version does not satisfy admission evidence")

func validateSettlementModel(d Demand, s Settlement) error {
	if policyText(s.ModelVersion, true) != nil {
		return ErrModelVersionProof
	}
	if d.RequireModelVersion && (d.Target.ModelVersion == "" || s.ModelVersion == "") {
		return fmt.Errorf("%w: required version missing", ErrModelVersionProof)
	}
	if d.Target.ModelVersion != "" && s.ModelVersion != "" && d.Target.ModelVersion != s.ModelVersion {
		return fmt.Errorf("%w: version mismatch", ErrModelVersionProof)
	}
	return nil
}

func settlementDigests(s Settlement) (string, string) {
	// Preserve legacy empty-version identities; a supplied proof is immutable.
	if s.ModelVersion == "" {
		return quotaHash(s.AttemptID, s.Claim, s.Usage, s.ProviderResourceID), quotaHash(s.Usage, s.ProviderResourceID)
	}
	return quotaHash(s.AttemptID, s.Claim, s.Usage, s.ProviderResourceID, s.ModelVersion), quotaHash(s.Usage, s.ProviderResourceID, s.ModelVersion)
}
