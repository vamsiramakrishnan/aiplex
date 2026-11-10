// Package quotaplex implements provider-independent quota and admission controls.
// Inputs must be bound to authenticated host facts; validation is not authentication.
package quotaplex

import (
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

const Version = "v1"

type Vector map[string]int64

type Meter struct {
	ID       string `json:"id"`
	Unit     string `json:"unit"` // tokens, requests, calls, milliseconds, bytes, rows, or money
	Currency string `json:"currency,omitempty"`
	Scale    uint8  `json:"scale,omitempty"`
	Billable bool   `json:"billable"`
}

// UnitPrice expresses Numerator fixed-point money units per Denominator raw units.
// A declared zero price is distinct from a missing price.
type UnitPrice struct {
	MeterID      string `json:"meter_id"`
	MoneyMeterID string `json:"money_meter_id"`
	Numerator    int64  `json:"numerator"`
	Denominator  int64  `json:"denominator"`
}

type Period struct {
	Kind         string        `json:"kind"` // fixed, anchored, calendar; rolling is explicitly unsupported
	Start        time.Time     `json:"start,omitempty"`
	End          time.Time     `json:"end,omitempty"`
	Anchor       time.Time     `json:"anchor,omitempty"`
	Duration     time.Duration `json:"duration,omitempty"`
	CalendarUnit string        `json:"calendar_unit,omitempty"` // day, week, month
	TimeZone     string        `json:"time_zone,omitempty"`
}

type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type GroupRef struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

type Membership struct {
	Ref       contracts.SnapshotRef `json:"ref"`
	TenantID  string                `json:"tenant_id"`
	SubjectID string                `json:"subject_id"`
	Groups    []GroupRef            `json:"groups"`
	IssuedAt  time.Time             `json:"issued_at"`
	ExpiresAt time.Time             `json:"expires_at"`
}

type Subject struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"` // human, agent, application
	Membership Membership `json:"membership"`
}

type Target struct {
	Plane          string `json:"plane"`
	Provider       string `json:"provider"`
	ResourceID     string `json:"resource_id"`
	Operation      string `json:"operation"`
	PoolID         string `json:"pool_id,omitempty"`
	BillingAccount string `json:"billing_account,omitempty"`
	Region         string `json:"region,omitempty"`
	ModelVersion   string `json:"model_version,omitempty"`
	// AccessPointID, CallerKind and ClientID are host-derived facts for a
	// request that entered through an access point. They are empty otherwise.
	// ClientID is a registered client registration; a public OAuth client id
	// is attribution, not a security boundary.
	AccessPointID       string `json:"access_point_id,omitempty"`
	CallerKind          string `json:"caller_kind,omitempty"`
	ClientID            string `json:"client_id,omitempty"`
	ConfigurationDigest string `json:"configuration_digest"`
}

type Match struct {
	Plane          string `json:"plane,omitempty"`
	Provider       string `json:"provider,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
	Operation      string `json:"operation,omitempty"`
	PoolID         string `json:"pool_id,omitempty"`
	BillingAccount string `json:"billing_account,omitempty"`
	Region         string `json:"region,omitempty"`
	ModelVersion   string `json:"model_version,omitempty"`
	AccessPointID  string `json:"access_point_id,omitempty"`
	CallerKind     string `json:"caller_kind,omitempty"` // agent, user or client
	ClientID       string `json:"client_id,omitempty"`   // registered client registration id
}

type Eligibility struct {
	SubjectRole string    `json:"subject_role"` // principal or actor
	SubjectID   string    `json:"subject_id,omitempty"`
	Group       *GroupRef `json:"group,omitempty"`
}

type Account struct {
	ID         string        `json:"id"`
	Priority   int           `json:"priority"` // higher wins; ties without selection are denied
	TenantWide bool          `json:"tenant_wide,omitempty"`
	Eligible   []Eligibility `json:"eligible,omitempty"`
}

type Scope struct {
	Kind        string    `json:"kind"` // tenant, account, principal, actor, group, resource, pool
	ID          string    `json:"id,omitempty"`
	Group       *GroupRef `json:"group,omitempty"`
	SubjectRole string    `json:"subject_role,omitempty"`
}

// PopulationAllocation is a host-verified, frozen allocation strategy. It is
// never inferred from one request's group list or a live directory count.
type PopulationAllocation struct {
	Ref        contracts.SnapshotRef `json:"ref"`
	Group      GroupRef              `json:"group"`
	Count      int64                 `json:"count"`
	PerMember  int64                 `json:"per_member"`
	ObservedAt time.Time             `json:"observed_at"`
}

type BudgetRule struct {
	ID         string                `json:"id"`
	Scope      Scope                 `json:"scope"`
	Match      Match                 `json:"match"`
	MeterID    string                `json:"meter_id"`
	Limit      int64                 `json:"limit"`
	Period     Period                `json:"period"`
	Population *PopulationAllocation `json:"population,omitempty"`
}

type RateRule struct {
	ID       string        `json:"id"`
	Scope    Scope         `json:"scope"`
	Match    Match         `json:"match"`
	MeterID  string        `json:"meter_id"`
	Rate     int64         `json:"rate"`
	Interval time.Duration `json:"interval"`
	Burst    int64         `json:"burst"`
	Basis    string        `json:"basis"` // admitted
}

type ConcurrencyRule struct {
	ID    string `json:"id"`
	Scope Scope  `json:"scope"`
	Match Match  `json:"match"`
	Limit int64  `json:"limit"`
}

type PriceBook struct {
	ID       string      `json:"id"`
	Revision uint64      `json:"revision"`
	Match    Match       `json:"match"`
	Prices   []UnitPrice `json:"prices"`
}

// LoanRule explicitly permits lending uncommitted allocation between two
// account-scoped budget rules. Throughput sharing belongs to scheduling.
type LoanRule struct {
	ID             string        `json:"id"`
	LenderRuleID   string        `json:"lender_rule_id"`
	BorrowerRuleID string        `json:"borrower_rule_id"`
	MaxAmount      int64         `json:"max_amount"`
	ProtectedFloor int64         `json:"protected_floor"`
	MaxDuration    time.Duration `json:"max_duration"`
}

type Policy struct {
	Version     string            `json:"version"`
	TenantID    string            `json:"tenant_id"`
	ID          string            `json:"id"`
	Revision    uint64            `json:"revision"`
	EffectiveAt time.Time         `json:"effective_at"`
	ExpiresAt   time.Time         `json:"expires_at,omitempty"`
	Meters      map[string]Meter  `json:"meters"`
	PriceBooks  []PriceBook       `json:"price_books,omitempty"`
	Accounts    []Account         `json:"accounts"`
	Budgets     []BudgetRule      `json:"budgets,omitempty"`
	Rates       []RateRule        `json:"rates,omitempty"`
	Concurrency []ConcurrencyRule `json:"concurrency,omitempty"`
	Sharing     []LoanRule        `json:"sharing,omitempty"`
}

type PolicyRef struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Digest   string `json:"digest"`
}

type Demand struct {
	RequireModelVersion bool                     `json:"require_model_version,omitempty"`
	Version             string                   `json:"version"`
	Context             contracts.RequestContext `json:"context"`
	Principal           Subject                  `json:"principal"`
	Actor               *Subject                 `json:"actor,omitempty"`
	LogicalOperationID  string                   `json:"logical_operation_id"`
	AttemptID           string                   `json:"attempt_id"`
	PayloadDigest       string                   `json:"payload_digest"`
	Target              Target                   `json:"target"`
	FundingAccountID    string                   `json:"funding_account_id,omitempty"`
	Bounds              Vector                   `json:"bounds"` // trusted maximum raw exposure, excluding computed money
	// Budgets are tenant budgets the host resolved from verified facts (see
	// BudgetDraw). The demand draws from every one of them in the same
	// admission transaction as the policy's own controls.
	Budgets []BudgetDraw `json:"budgets,omitempty"`
}

type BudgetLimit struct {
	Key        string `json:"key"`
	LineageKey string `json:"lineage_key"`
	Shape      string `json:"shape"`
	RuleID     string `json:"rule_id"`
	MeterID    string `json:"meter_id"`
	Window     Window `json:"window"`
	Limit      int64  `json:"limit"`
	Demand     int64  `json:"demand"`
	// BudgetID, MeterIDs, UnknownCost and Thresholds are set for a tenant
	// budget draw (BudgetDraw). MeterIDs are summed; a flagged limit holds and
	// consumes nothing and only counts the request as unknown cost.
	BudgetID    string   `json:"budget_id,omitempty"`
	MeterIDs    []string `json:"meter_ids,omitempty"`
	UnknownCost bool     `json:"unknown_cost,omitempty"`
	Thresholds  []int    `json:"thresholds,omitempty"`
}

type RateLimit struct {
	Key        string        `json:"key"`
	LineageKey string        `json:"lineage_key"`
	Shape      string        `json:"shape"`
	RuleID     string        `json:"rule_id"`
	MeterID    string        `json:"meter_id"`
	Rate       int64         `json:"rate"`
	Interval   time.Duration `json:"interval"`
	Burst      int64         `json:"burst"`
	Demand     int64         `json:"demand"`
}

type ConcurrencyLimit struct {
	Key        string `json:"key"`
	LineageKey string `json:"lineage_key"`
	Shape      string `json:"shape"`
	RuleID     string `json:"rule_id"`
	Limit      int64  `json:"limit"`
	Demand     int64  `json:"demand"`
}

type Plan struct {
	Policy            PolicyRef          `json:"policy"`
	PayerID           string             `json:"payer_id"`
	Bounds            Vector             `json:"bounds"`
	Meters            map[string]Meter   `json:"meters"`
	Prices            []UnitPrice        `json:"prices,omitempty"`
	PriceBookID       string             `json:"price_book_id,omitempty"`
	PriceBookRevision uint64             `json:"price_book_revision,omitempty"`
	Budgets           []BudgetLimit      `json:"budgets,omitempty"`
	Rates             []RateLimit        `json:"rates,omitempty"`
	Concurrency       []ConcurrencyLimit `json:"concurrency,omitempty"`
}

type Decision struct {
	TenantID   string        `json:"tenant_id"`
	AttemptID  string        `json:"attempt_id"`
	Allowed    bool          `json:"allowed"`
	State      string        `json:"state"`
	Reason     string        `json:"reason"`
	Policy     PolicyRef     `json:"policy"`
	PayerID    string        `json:"payer_id,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	Overrun    bool          `json:"overrun,omitempty"`
	// BudgetID names the tenant budget (BudgetDraw) that caused a denial.
	BudgetID string `json:"budget_id,omitempty"`
}

type DispatchClaim struct {
	TenantID  string `json:"tenant_id"`
	AttemptID string `json:"attempt_id"`
	Fence     uint64 `json:"fence"`
}

// Settlement must originate from a host-authorized terminal usage producer.
// Successful=false does not erase consumption; client disconnect is not terminal.
type Settlement struct {
	ModelVersion       string        `json:"model_version,omitempty"`
	TenantID           string        `json:"tenant_id"`
	AttemptID          string        `json:"attempt_id"`
	EventID            string        `json:"event_id"`
	Claim              DispatchClaim `json:"claim"`
	Usage              Vector        `json:"usage"`
	ProviderResourceID string        `json:"provider_resource_id,omitempty"`
	// Unpriced settles every money meter at zero instead of pricing the
	// usage: no provider ran (an answer served from the AIPlex response
	// cache), so no provider price applies, while request-count meters and
	// budgets still count the request.
	Unpriced bool `json:"unpriced,omitempty"`
}
