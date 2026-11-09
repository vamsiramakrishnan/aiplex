package quotaplex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const maxPolicyRules = 32

// ResolveError is a deterministic policy denial, rather than an unavailable
// transaction or malformed input. An admission engine may persist its replay.
type ResolveError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ResolveError) Error() string         { return e.Code + ": " + e.Message }
func policyDenied(code, message string) error { return &ResolveError{Code: code, Message: message} }

// CompiledPolicy owns an immutable, validated policy snapshot. Policy returns a
// copy; changing the original input or a returned plan cannot alter this object.
type CompiledPolicy struct {
	policy   Policy
	ref      PolicyRef
	lineages map[string]string
}

func policyCopy[T any](value T) T {
	raw, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}

func policyDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func policyKey(parts ...string) string { return policyDigest(parts) }

func policyText(value string, optional bool) error {
	if value == "" && optional {
		return nil
	}
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return fmt.Errorf("bounded nonempty identifier required")
	}
	for _, ch := range value {
		if ch < 0x20 || ch == 0x7f {
			return fmt.Errorf("control characters are not identifiers")
		}
	}
	return nil
}

func policyGroup(group GroupRef) error {
	if policyText(group.Source, false) != nil || policyText(group.ID, false) != nil {
		return fmt.Errorf("source-qualified group required")
	}
	return nil
}

func policyMatchValid(match Match) error {
	for _, value := range []string{match.Plane, match.Provider, match.ResourceID, match.Operation, match.PoolID, match.BillingAccount, match.Region, match.ModelVersion} {
		if policyText(value, true) != nil || strings.Contains(value, "*") {
			return fmt.Errorf("match selectors require exact bounded values; empty selects all")
		}
	}
	return nil
}

func policyMatches(match Match, target Target) bool {
	return (match.Plane == "" || match.Plane == target.Plane) &&
		(match.Provider == "" || match.Provider == target.Provider) &&
		(match.ResourceID == "" || match.ResourceID == target.ResourceID) &&
		(match.Operation == "" || match.Operation == target.Operation) &&
		(match.PoolID == "" || match.PoolID == target.PoolID) &&
		(match.BillingAccount == "" || match.BillingAccount == target.BillingAccount) &&
		(match.Region == "" || match.Region == target.Region) &&
		(match.ModelVersion == "" || match.ModelVersion == target.ModelVersion)
}

func policyScopeValid(scope Scope, tenant string, accounts map[string]Account) error {
	if policyText(scope.ID, true) != nil {
		return fmt.Errorf("invalid scope identifier")
	}
	switch scope.Kind {
	case "group":
		if scope.Group == nil || policyGroup(*scope.Group) != nil || scope.ID != "" || (scope.SubjectRole != "principal" && scope.SubjectRole != "actor") {
			return fmt.Errorf("group scope requires a qualified group and principal/actor role")
		}
		return nil
	case "tenant", "account", "principal", "actor", "resource", "pool":
		if scope.Group != nil || scope.SubjectRole != "" {
			return fmt.Errorf("group fields are valid only for group scopes")
		}
		if scope.Kind == "tenant" && scope.ID != "" && scope.ID != tenant {
			return fmt.Errorf("scope crosses policy tenant")
		}
		if scope.Kind == "account" && scope.ID != "" {
			if _, ok := accounts[scope.ID]; !ok {
				return fmt.Errorf("unknown scoped account")
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported scope kind %q", scope.Kind)
	}
}

func policyPopulation(rule BudgetRule, accounts map[string]Account) (int64, error) {
	if rule.Population == nil {
		return rule.Limit, nil
	}
	population := rule.Population
	if rule.Limit != 0 || policyGroup(population.Group) != nil || policyText(population.Ref.ID, false) != nil || policyText(population.Ref.Version, false) != nil || population.ObservedAt.IsZero() || population.Count < 0 || population.PerMember < 0 {
		return 0, fmt.Errorf("population strategy requires a frozen qualified snapshot and no fixed limit")
	}
	bound := false
	switch rule.Scope.Kind {
	case "group":
		bound = rule.Scope.Group != nil && *rule.Scope.Group == population.Group
	case "account":
		if rule.Scope.ID != "" {
			for _, eligibility := range accounts[rule.Scope.ID].Eligible {
				if eligibility.Group != nil && *eligibility.Group == population.Group {
					bound = true
				}
			}
		}
	}
	if !bound {
		return 0, fmt.Errorf("population snapshot must identify the allocation's group or group-backed account")
	}
	if population.PerMember != 0 && population.Count > int64(^uint64(0)>>1)/population.PerMember {
		return 0, ErrQuantityOverflow
	}
	return population.Count * population.PerMember, nil
}

func policyLineage(policyID, kind, ruleID string) string { return policyKey(policyID, kind, ruleID) }

func budgetShape(rule BudgetRule, meter Meter) string {
	var populationGroup *GroupRef
	if rule.Population != nil {
		populationGroup = &rule.Population.Group
	}
	return policyDigest(struct {
		Scope           Scope
		Match           Match
		Meter           Meter
		Period          Period
		PopulationGroup *GroupRef
	}{rule.Scope, rule.Match, meter, rule.Period, populationGroup})
}

func rateShape(rule RateRule, meter Meter) string {
	return policyDigest(struct {
		Scope    Scope
		Match    Match
		Meter    Meter
		Rate     int64
		Interval time.Duration
		Burst    int64
		Basis    string
	}{rule.Scope, rule.Match, meter, rule.Rate, rule.Interval, rule.Burst, rule.Basis})
}

func concurrencyShape(rule ConcurrencyRule) string {
	return policyDigest(struct {
		Scope Scope
		Match Match
	}{rule.Scope, rule.Match})
}

// Compile validates one complete policy before any admission can touch state.
// Structural lineage fingerprints prevent a later revision from changing the
// interpretation of saved capacity under the same policy/rule identity.
func Compile(input Policy) (*CompiledPolicy, error) {
	if input.Version != Version || policyText(input.TenantID, false) != nil || policyText(input.ID, false) != nil || input.Revision == 0 || input.EffectiveAt.IsZero() || (!input.ExpiresAt.IsZero() && !input.ExpiresAt.After(input.EffectiveAt)) {
		return nil, fmt.Errorf("versioned tenant policy and valid effective interval required")
	}
	if len(input.Meters) == 0 || len(input.Meters) > 64 || len(input.Accounts) == 0 || len(input.Accounts) > 64 || len(input.PriceBooks) > 64 || len(input.Budgets)+len(input.Rates)+len(input.Concurrency) == 0 || len(input.Budgets)+len(input.Rates)+len(input.Concurrency)+len(input.Sharing) > maxPolicyRules {
		return nil, fmt.Errorf("bounded meters, accounts and 1..%d control rules required", maxPolicyRules)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("policy cannot be serialized: %w", err)
	}
	if len(raw) > MaxRecordBytes {
		return nil, fmt.Errorf("policy snapshot exceeds storage bound: %w", ErrTransactionLimit)
	}
	var policy Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, fmt.Errorf("policy cannot be decoded: %w", err)
	}
	for id, meter := range policy.Meters {
		if id != meter.ID || policyText(id, false) != nil {
			return nil, fmt.Errorf("meter key and identity disagree")
		}
		if err := ValidateMeter(meter); err != nil {
			return nil, err
		}
	}
	accounts := map[string]Account{}
	for _, account := range policy.Accounts {
		if policyText(account.ID, false) != nil {
			return nil, fmt.Errorf("invalid account")
		}
		if _, exists := accounts[account.ID]; exists {
			return nil, fmt.Errorf("duplicate account")
		}
		if (account.TenantWide && len(account.Eligible) != 0) || (!account.TenantWide && len(account.Eligible) == 0) || len(account.Eligible) > 128 {
			return nil, fmt.Errorf("account requires explicit tenant-wide access or bounded eligibility")
		}
		seen := map[string]bool{}
		for _, eligibility := range account.Eligible {
			if eligibility.SubjectRole != "principal" && eligibility.SubjectRole != "actor" {
				return nil, fmt.Errorf("account eligibility requires an explicit subject role")
			}
			if (eligibility.SubjectID == "") == (eligibility.Group == nil) || policyText(eligibility.SubjectID, true) != nil || (eligibility.Group != nil && policyGroup(*eligibility.Group) != nil) {
				return nil, fmt.Errorf("eligibility selects one subject or source-qualified group")
			}
			key := policyDigest(eligibility)
			if seen[key] {
				return nil, fmt.Errorf("duplicate account eligibility")
			}
			seen[key] = true
		}
		accounts[account.ID] = account
	}
	books := map[string]bool{}
	for _, book := range policy.PriceBooks {
		if policyText(book.ID, false) != nil || books[book.ID] || book.Revision == 0 || len(book.Prices) == 0 || len(book.Prices) > 128 {
			return nil, fmt.Errorf("bounded uniquely versioned price book required")
		}
		if err := policyMatchValid(book.Match); err != nil {
			return nil, err
		}
		if err := ValidatePrices(policy.Meters, book.Prices); err != nil {
			return nil, err
		}
		books[book.ID] = true
	}
	compiled := &CompiledPolicy{policy: policy, lineages: map[string]string{}}
	// Meter identity is immutable even when the raw meter appears only in a
	// price book rather than directly in a budget or rate rule.
	for id, meter := range policy.Meters {
		compiled.lineages[policyLineage(policy.ID, "meter", id)] = policyDigest(meter)
	}
	seen := map[string]bool{}
	validateRule := func(kind, id string, scope Scope, match Match) error {
		if policyText(id, false) != nil || seen[kind+"\x00"+id] {
			return fmt.Errorf("invalid or duplicate %s rule", kind)
		}
		seen[kind+"\x00"+id] = true
		if err := policyScopeValid(scope, policy.TenantID, accounts); err != nil {
			return err
		}
		return policyMatchValid(match)
	}
	for _, rule := range policy.Budgets {
		if err := validateRule("budget", rule.ID, rule.Scope, rule.Match); err != nil {
			return nil, err
		}
		meter, ok := policy.Meters[rule.MeterID]
		if !ok || rule.Limit < 0 {
			return nil, fmt.Errorf("budget requires a known meter and nonnegative limit")
		}
		if err := rule.Period.Validate(); err != nil {
			return nil, err
		}
		if _, err := policyPopulation(rule, accounts); err != nil {
			return nil, err
		}
		compiled.lineages[policyLineage(policy.ID, "budget", rule.ID)] = budgetShape(rule, meter)
	}
	for _, rule := range policy.Rates {
		if err := validateRule("rate", rule.ID, rule.Scope, rule.Match); err != nil {
			return nil, err
		}
		meter, ok := policy.Meters[rule.MeterID]
		if !ok || rule.Rate <= 0 || rule.Interval <= 0 || rule.Burst <= 0 || rule.Basis != "admitted" {
			return nil, fmt.Errorf("rate requires a known meter, positive rate/interval/burst and admitted basis")
		}
		compiled.lineages[policyLineage(policy.ID, "rate", rule.ID)] = rateShape(rule, meter)
	}
	for _, rule := range policy.Concurrency {
		if err := validateRule("concurrency", rule.ID, rule.Scope, rule.Match); err != nil {
			return nil, err
		}
		if rule.Limit < 0 {
			return nil, fmt.Errorf("concurrency limit must be nonnegative")
		}
		compiled.lineages[policyLineage(policy.ID, "concurrency", rule.ID)] = concurrencyShape(rule)
	}
	budgetRules := map[string]BudgetRule{}
	for _, rule := range policy.Budgets {
		budgetRules[rule.ID] = rule
	}
	for _, loan := range policy.Sharing {
		if policyText(loan.ID, false) != nil || seen["loan\x00"+loan.ID] || loan.MaxAmount <= 0 || loan.ProtectedFloor < 0 || loan.MaxDuration <= 0 {
			return nil, fmt.Errorf("sharing requires a unique identity, positive bounded quantity/duration and nonnegative protected floor")
		}
		seen["loan\x00"+loan.ID] = true
		lender, lenderOK := budgetRules[loan.LenderRuleID]
		borrower, borrowerOK := budgetRules[loan.BorrowerRuleID]
		if !lenderOK || !borrowerOK || lender.ID == borrower.ID || lender.Scope.Kind != "account" || borrower.Scope.Kind != "account" || lender.Scope.ID == "" || borrower.Scope.ID == "" || lender.Scope.ID == borrower.Scope.ID || lender.MeterID != borrower.MeterID || lender.Match != borrower.Match {
			return nil, fmt.Errorf("sharing requires distinct explicit accounts, known budget rules and identical meter/target selectors")
		}
		compiled.lineages[policyLineage(policy.ID, "loan", loan.ID)] = policyDigest(struct {
			Rule             LoanRule
			Lender, Borrower string
		}{loan, budgetShape(lender, policy.Meters[lender.MeterID]), budgetShape(borrower, policy.Meters[borrower.MeterID])})
	}
	if len(compiled.lineages)+len(policy.PriceBooks)+2 > MaxTransactionRecords {
		return nil, fmt.Errorf("policy activation exceeds transaction record bound: %w", ErrTransactionLimit)
	}
	// The input's declaration order must not affect its digest or admission plan.
	sort.Slice(compiled.policy.Accounts, func(i, j int) bool { return compiled.policy.Accounts[i].ID < compiled.policy.Accounts[j].ID })
	for i := range compiled.policy.Accounts {
		sort.Slice(compiled.policy.Accounts[i].Eligible, func(a, b int) bool {
			return policyDigest(compiled.policy.Accounts[i].Eligible[a]) < policyDigest(compiled.policy.Accounts[i].Eligible[b])
		})
	}
	sort.Slice(compiled.policy.PriceBooks, func(i, j int) bool { return compiled.policy.PriceBooks[i].ID < compiled.policy.PriceBooks[j].ID })
	for i := range compiled.policy.PriceBooks {
		sort.Slice(compiled.policy.PriceBooks[i].Prices, func(a, b int) bool {
			x, y := compiled.policy.PriceBooks[i].Prices[a], compiled.policy.PriceBooks[i].Prices[b]
			if x.MeterID == y.MeterID {
				return x.MoneyMeterID < y.MoneyMeterID
			}
			return x.MeterID < y.MeterID
		})
	}
	sort.Slice(compiled.policy.Budgets, func(i, j int) bool { return compiled.policy.Budgets[i].ID < compiled.policy.Budgets[j].ID })
	sort.Slice(compiled.policy.Rates, func(i, j int) bool { return compiled.policy.Rates[i].ID < compiled.policy.Rates[j].ID })
	sort.Slice(compiled.policy.Concurrency, func(i, j int) bool { return compiled.policy.Concurrency[i].ID < compiled.policy.Concurrency[j].ID })
	sort.Slice(compiled.policy.Sharing, func(i, j int) bool { return compiled.policy.Sharing[i].ID < compiled.policy.Sharing[j].ID })
	compiled.ref = PolicyRef{ID: policy.ID, Revision: policy.Revision, Digest: policyDigest(compiled.policy)}
	// Activation persists this envelope, not the bare policy. Validate the
	// actual serialized shape so near-limit documents fail before a transaction.
	if _, err := EncodeStoreValue(struct {
		Reference PolicyRef `json:"reference"`
		Policy    Policy    `json:"policy"`
	}{compiled.ref, compiled.policy}); err != nil {
		return nil, fmt.Errorf("policy activation envelope exceeds storage bound: %w", err)
	}
	return compiled, nil
}

func (c *CompiledPolicy) Reference() PolicyRef { return c.ref }
func (c *CompiledPolicy) Policy() Policy       { return policyCopy(c.policy) }
func (c *CompiledPolicy) Lineages() map[string]string {
	out := make(map[string]string, len(c.lineages))
	for key, shape := range c.lineages {
		out[key] = shape
	}
	return out
}

func policyMembershipStatic(subject Subject, tenant string) error {
	if policyText(subject.ID, false) != nil || (subject.Kind != "human" && subject.Kind != "agent" && subject.Kind != "application") {
		return fmt.Errorf("typed subject required")
	}
	membership := subject.Membership
	if membership.TenantID != tenant || membership.SubjectID != subject.ID || policyText(membership.Ref.ID, false) != nil || policyText(membership.Ref.Version, false) != nil || membership.IssuedAt.IsZero() || !membership.ExpiresAt.After(membership.IssuedAt) || len(membership.Groups) > 1024 {
		return fmt.Errorf("membership identity or snapshot is invalid")
	}
	seen := map[string]bool{}
	for _, group := range membership.Groups {
		if err := policyGroup(group); err != nil {
			return err
		}
		key := policyDigest(group)
		if seen[key] {
			return fmt.Errorf("duplicate membership group")
		}
		seen[key] = true
	}
	return nil
}

// ValidateDemand checks immutable envelope shape without using the clock or
// active policy, so a replay can be located before freshness is re-evaluated.
func ValidateDemand(demand Demand) error {
	if demand.Version != Version {
		return fmt.Errorf("unsupported quota demand version")
	}
	if err := demand.Context.Validate(); err != nil {
		return err
	}
	for _, value := range []string{demand.Context.TenantID, demand.Context.RequestID, demand.Context.PrincipalID, demand.Context.ResourceID, demand.LogicalOperationID, demand.AttemptID, demand.Target.Plane, demand.Target.Provider, demand.Target.ResourceID, demand.Target.Operation} {
		if err := policyText(value, false); err != nil {
			return err
		}
	}
	for _, value := range []string{demand.Context.ActorID, demand.Context.ApplicationID, demand.Context.Purpose, demand.Target.PoolID, demand.Target.BillingAccount, demand.Target.Region, demand.Target.ModelVersion, demand.FundingAccountID} {
		if err := policyText(value, true); err != nil {
			return err
		}
	}
	for _, digest := range []string{demand.PayloadDigest, demand.Target.ConfigurationDigest} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size || strings.ToLower(digest) != digest {
			return fmt.Errorf("payload and configuration digests must be canonical SHA-256 hex digests")
		}
	}
	if demand.Principal.ID != demand.Context.PrincipalID || demand.Target.ResourceID != demand.Context.ResourceID {
		return fmt.Errorf("quota demand does not bind the trusted principal/resource")
	}
	if err := policyMembershipStatic(demand.Principal, demand.Context.TenantID); err != nil {
		return err
	}
	if demand.Context.MembershipSnapshotRef == nil || *demand.Context.MembershipSnapshotRef != demand.Principal.Membership.Ref {
		return fmt.Errorf("principal membership reference does not bind the trusted context")
	}
	if demand.Actor != nil {
		if demand.Context.ActorID != demand.Actor.ID || demand.Actor.Kind != "agent" {
			return fmt.Errorf("agent actor does not bind the trusted context")
		}
		if err := policyMembershipStatic(*demand.Actor, demand.Context.TenantID); err != nil {
			return err
		}
	} else if demand.Context.ActorID != "" {
		return fmt.Errorf("trusted actor snapshot is missing")
	}
	if len(demand.Bounds) == 0 || len(demand.Bounds) > 64 {
		return fmt.Errorf("bounded meter exposure required")
	}
	return ValidateVector(demand.Bounds)
}

func policyContainsGroup(subject *Subject, group GroupRef) bool {
	if subject == nil {
		return false
	}
	for _, member := range subject.Membership.Groups {
		if member == group {
			return true
		}
	}
	return false
}

func policySubject(demand Demand, role string) *Subject {
	if role == "principal" {
		return &demand.Principal
	}
	return demand.Actor
}

func policyEligible(account Account, demand Demand) bool {
	if account.TenantWide {
		return true
	}
	for _, eligibility := range account.Eligible {
		subject := policySubject(demand, eligibility.SubjectRole)
		if subject != nil && ((eligibility.SubjectID != "" && eligibility.SubjectID == subject.ID) || (eligibility.Group != nil && policyContainsGroup(subject, *eligibility.Group))) {
			return true
		}
	}
	return false
}

func policyScopeValue(scope Scope, demand Demand, payer string) (string, bool, error) {
	var value string
	switch scope.Kind {
	case "tenant":
		value = demand.Context.TenantID
	case "account":
		value = payer
	case "principal":
		value = demand.Principal.ID
	case "actor":
		if demand.Actor == nil {
			return "", false, nil
		}
		value = demand.Actor.ID
	case "resource":
		value = policyKey(demand.Target.Provider, demand.Target.ResourceID)
	case "pool":
		if demand.Target.PoolID == "" {
			return "", false, policyDenied("unsupported_target", "a capacity pool is required by the matched policy")
		}
		value = demand.Target.PoolID
	case "group":
		if !policyContainsGroup(policySubject(demand, scope.SubjectRole), *scope.Group) {
			return "", false, nil
		}
		return policyKey(scope.Group.Source, scope.Group.ID), true, nil
	}
	compare := value
	if scope.Kind == "resource" {
		compare = demand.Target.ResourceID
	}
	if scope.ID != "" && scope.ID != compare {
		return "", false, nil
	}
	return value, true, nil
}

func policyMeterDemand(meter string, bounds Vector, meters map[string]Meter) (int64, error) {
	quantity, ok := bounds[meter]
	if !ok {
		return 0, policyDenied("unsupported_meter", "matched meter has no bounded exposure: "+meter)
	}
	if (meters[meter].Unit == "requests" || meters[meter].Unit == "calls") && quantity == 0 {
		return 0, policyDenied("unsupported_meter", "requests/calls require a positive attempt bound")
	}
	return quantity, nil
}

// Resolve compiles trusted facts into all simultaneous constraints. It does not
// read balances, choose another payer after dispatch, or perform provider calls.
func (c *CompiledPolicy) Resolve(demand Demand, now time.Time) (Plan, error) {
	if err := ValidateDemand(demand); err != nil {
		return Plan{}, err
	}
	policy := c.policy
	if demand.Context.TenantID != policy.TenantID {
		return Plan{}, policyDenied("tenant_mismatch", "demand crosses policy tenant")
	}
	if now.Before(policy.EffectiveAt) || (!policy.ExpiresAt.IsZero() && !now.Before(policy.ExpiresAt)) {
		return Plan{}, policyDenied("policy_not_effective", "policy is not effective at admission")
	}
	for _, subject := range []*Subject{&demand.Principal, demand.Actor} {
		if subject != nil && (subject.Membership.IssuedAt.After(now) || !subject.Membership.ExpiresAt.After(now)) {
			return Plan{}, policyDenied("membership_invalid", "subject membership is stale or not yet valid")
		}
	}
	var payer *Account
	ambiguous := false
	for i := range policy.Accounts {
		account := &policy.Accounts[i]
		if !policyEligible(*account, demand) {
			continue
		}
		if demand.FundingAccountID != "" {
			if account.ID == demand.FundingAccountID {
				payer = account
				break
			}
			continue
		}
		if payer == nil || account.Priority > payer.Priority {
			payer = account
			ambiguous = false
		} else if account.Priority == payer.Priority {
			ambiguous = true
		}
	}
	if payer == nil {
		return Plan{}, policyDenied("funding_unavailable", "no eligible funding account")
	}
	if ambiguous {
		return Plan{}, policyDenied("funding_ambiguous", "equal funding priorities require an explicit eligible selection")
	}
	plan := Plan{Policy: c.ref, PayerID: payer.ID, Bounds: CloneVector(demand.Bounds), Meters: policyCopy(policy.Meters)}
	for id, quantity := range demand.Bounds {
		meter, ok := policy.Meters[id]
		if !ok || meter.Unit == "money" {
			return Plan{}, policyDenied("unsupported_meter", "bounds require known raw meters: "+id)
		}
		if (meter.Unit == "requests" || meter.Unit == "calls") && quantity == 0 {
			return Plan{}, policyDenied("unsupported_meter", "requests/calls require a positive attempt bound")
		}
	}
	var selected *PriceBook
	for i := range policy.PriceBooks {
		book := &policy.PriceBooks[i]
		if !policyMatches(book.Match, demand.Target) {
			continue
		}
		if selected != nil {
			return Plan{}, policyDenied("pricebook_ambiguous", "more than one price book matches the native target")
		}
		selected = book
	}
	moneyMeters := map[string]bool{}
	for _, rule := range policy.Budgets {
		if !policyMatches(rule.Match, demand.Target) {
			continue
		}
		_, applies, err := policyScopeValue(rule.Scope, demand, payer.ID)
		if err != nil {
			return Plan{}, err
		}
		if applies && policy.Meters[rule.MeterID].Unit == "money" {
			moneyMeters[rule.MeterID] = true
		}
	}
	for _, rule := range policy.Rates {
		if !policyMatches(rule.Match, demand.Target) {
			continue
		}
		_, applies, err := policyScopeValue(rule.Scope, demand, payer.ID)
		if err != nil {
			return Plan{}, err
		}
		if applies && policy.Meters[rule.MeterID].Unit == "money" {
			moneyMeters[rule.MeterID] = true
		}
	}
	if len(moneyMeters) > 0 && selected == nil {
		return Plan{}, policyDenied("unpriced_meter", "money controls require a matching versioned price book")
	}
	if selected != nil {
		coverage := map[string]map[string]bool{}
		for _, price := range selected.Prices {
			if _, known := demand.Bounds[price.MeterID]; !known {
				return Plan{}, policyDenied("unsupported_meter", "price input has no bounded exposure: "+price.MeterID)
			}
			if coverage[price.MoneyMeterID] == nil {
				coverage[price.MoneyMeterID] = map[string]bool{}
			}
			coverage[price.MoneyMeterID][price.MeterID] = true
		}
		for money := range moneyMeters {
			if coverage[money] == nil {
				return Plan{}, policyDenied("unpriced_meter", "price book does not price the required currency/money meter")
			}
			for raw := range demand.Bounds {
				if policy.Meters[raw].Billable && !coverage[money][raw] {
					return Plan{}, policyDenied("unpriced_meter", "billable meter is missing an explicit price: "+raw)
				}
			}
		}
		priced, err := PriceUsage(demand.Bounds, selected.Prices, policy.Meters)
		if err != nil {
			return Plan{}, policyDenied("unpriced_meter", err.Error())
		}
		for meter, quantity := range priced {
			plan.Bounds[meter] = quantity
		}
		plan.Prices = policyCopy(selected.Prices)
		plan.PriceBookID = selected.ID
		plan.PriceBookRevision = selected.Revision
	}
	for _, rule := range policy.Budgets {
		if !policyMatches(rule.Match, demand.Target) {
			continue
		}
		scope, applies, err := policyScopeValue(rule.Scope, demand, payer.ID)
		if err != nil {
			return Plan{}, err
		}
		if !applies {
			continue
		}
		quantity, err := policyMeterDemand(rule.MeterID, plan.Bounds, policy.Meters)
		if err != nil {
			return Plan{}, err
		}
		window, err := rule.Period.Resolve(now)
		if err != nil {
			return Plan{}, policyDenied("period_inactive", err.Error())
		}
		limit := rule.Limit
		if rule.Population != nil {
			// Compilation already validated account/group binding and overflow.
			limit = rule.Population.Count * rule.Population.PerMember
			if rule.Population.ObservedAt.After(now) {
				return Plan{}, policyDenied("population_invalid", "allocation population snapshot is not yet valid")
			}
		}
		lineage := policyLineage(policy.ID, "budget", rule.ID)
		key := policyKey(lineage, scope, window.Start.UTC().Format(time.RFC3339Nano), window.End.UTC().Format(time.RFC3339Nano))
		plan.Budgets = append(plan.Budgets, BudgetLimit{Key: key, LineageKey: lineage, Shape: c.lineages[lineage], RuleID: rule.ID, MeterID: rule.MeterID, Window: window, Limit: limit, Demand: quantity})
	}
	for _, rule := range policy.Rates {
		if !policyMatches(rule.Match, demand.Target) {
			continue
		}
		scope, applies, err := policyScopeValue(rule.Scope, demand, payer.ID)
		if err != nil {
			return Plan{}, err
		}
		if !applies {
			continue
		}
		quantity, err := policyMeterDemand(rule.MeterID, plan.Bounds, policy.Meters)
		if err != nil {
			return Plan{}, err
		}
		lineage := policyLineage(policy.ID, "rate", rule.ID)
		plan.Rates = append(plan.Rates, RateLimit{Key: policyKey(lineage, scope), LineageKey: lineage, Shape: c.lineages[lineage], RuleID: rule.ID, MeterID: rule.MeterID, Rate: rule.Rate, Interval: rule.Interval, Burst: rule.Burst, Demand: quantity})
	}
	for _, rule := range policy.Concurrency {
		if !policyMatches(rule.Match, demand.Target) {
			continue
		}
		scope, applies, err := policyScopeValue(rule.Scope, demand, payer.ID)
		if err != nil {
			return Plan{}, err
		}
		if !applies {
			continue
		}
		lineage := policyLineage(policy.ID, "concurrency", rule.ID)
		plan.Concurrency = append(plan.Concurrency, ConcurrencyLimit{Key: policyKey(lineage, scope), LineageKey: lineage, Shape: c.lineages[lineage], RuleID: rule.ID, Limit: rule.Limit, Demand: 1})
	}
	if len(plan.Budgets)+len(plan.Rates)+len(plan.Concurrency) == 0 {
		return Plan{}, policyDenied("no_matching_controls", "protected demand matches no quota control")
	}
	return plan, nil
}
