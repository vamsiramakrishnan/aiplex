package deployment

import "time"

// Observation records configured native health criteria. It does not establish
// desired-spec convergence or rewrite the original invocation outcome.
type Observation struct {
	ID                  string    `json:"id"`
	TenantID            string    `json:"tenant_id"`
	PrincipalID         string    `json:"principal_id"`
	ActorID             string    `json:"actor_id,omitempty"`
	PlanID              string    `json:"plan_id"`
	ActionDigest        string    `json:"action_digest"`
	ReceiptDigest       string    `json:"receipt_digest"`
	ConfigurationDigest string    `json:"configuration_digest"`
	TargetRef           string    `json:"target_ref"`
	NativeState         string    `json:"native_state"`
	Ready               bool      `json:"ready"`
	ResultDigest        string    `json:"result_digest"`
	ObservedAt          time.Time `json:"observed_at"`
	ExpiresAt           time.Time `json:"expires_at"`
}
