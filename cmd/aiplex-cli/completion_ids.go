package main

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Shell completion of resource ids from the server. Completion runs inside
// the user's shell on every TAB, so it is held to these rules:
//
//   - one read-only request (GET list routes only; see getOnly), a short
//     timeout, at most completionMaxIDs candidates;
//   - silence on any failure: an unreachable API, a caller who is not signed
//     in or lacks the role for the list route, an empty list. No error text,
//     no file names, no prompt, and nothing is written to disk (the stored
//     token is not refreshed);
//   - an id is data from a server, so a candidate that a shell could misread
//     (white space, a control character, a leading dash, a metacharacter) is
//     never offered; see safeCandidate.
//
// completionTable is the only place a command's ids are registered.
const (
	completionTimeout = 1500 * time.Millisecond
	completionMaxIDs  = 200
	completionMaxLen  = 200
)

// idSource returns the candidate ids for one positional argument. prev holds
// the positional arguments already typed, for sources that depend on them
// (the identity bindings of an agent, the ids of a resource kind).
type idSource func(ctx context.Context, c *aiplex.Client, prev []string) ([]string, error)

// getOnly refuses every request that is not a GET, so that no completion can
// change anything on the server, whatever source a table row names.
type getOnly struct{ base http.RoundTripper }

func (g getOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		return nil, http.ErrNotSupported
	}
	return g.base.RoundTrip(r)
}

// newCompletionClient builds a client for completion: the same target as every
// other command, but it never refreshes a stored token (that writes a file),
// never prints debug output, never creates ~/.aiplex, and only sends GET
// requests. It returns nil when the token it would send is known to be
// expired: completion must not make a login the CLI considers over work.
func newCompletionClient() *aiplex.Client {
	defer cliconfig.SetReadOnly()()
	t := resolveTargetOpts(false)
	if !t.ExpiresAt.IsZero() && t.ExpiresAt.Before(time.Now()) {
		return nil
	}
	if exp, ok := jwtExpiry(t.Token); ok && exp.Before(time.Now()) {
		return nil
	}
	c := aiplex.NewClient(t.URL)
	if t.Token != "" {
		c.SetToken(t.Token)
	}
	base := c.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.HTTPClient.Transport = getOnly{base}
	c.HTTPClient.Timeout = completionTimeout
	return c
}

// completeArgs completes positional arguments from the sources of a table
// row: sources[i] completes the i-th positional argument, nil or past the end
// completes nothing.
func completeArgs(sources ...idSource) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) >= len(sources) || sources[len(args)] == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		c := newCompletionClient()
		if c == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ids, err := sources[len(args)](ctx, c, args)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterIDs(ids, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeWith completes the first positional argument from a lister.
func completeWith(l lister) cobra.CompletionFunc {
	return completeArgs(func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) { return l(ctx, c) })
}

// safeCandidate reports whether an id can be offered to a shell. Cobra's
// completion protocol is one candidate per line, with an optional description
// after a tab, so a tab or newline in an id would forge a description or a
// second candidate; a leading dash would be read as a flag; the remaining
// characters a shell treats specially (quotes, $, `, ;, &, |, <, >, (, ), *,
// ?, [, ], {, }, !, #, ~, backslash, white space) are not in an id. The
// allowed set is deliberately small.
func safeCandidate(id string) bool {
	if id == "" || len(id) > completionMaxLen || id[0] == '-' {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._:/@+=,-", r):
		default:
			return false
		}
	}
	return true
}

func filterIDs(ids []string, prefix string) []string {
	out := []string{}
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) && safeCandidate(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if len(out) > completionMaxIDs {
		out = out[:completionMaxIDs]
	}
	return out
}

func completeContexts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cliconfig.SetReadOnly()()
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return filterIDs(contextNames(cfg), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// idsOf collects the ids of a typed list.
func idsOf[T any](list []T, err error, id func(T) string) ([]string, error) {
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, r := range list {
		ids = append(ids, id(r))
	}
	return ids, nil
}

// fromLister uses an id lister registered by a command group (the same one
// "did you mean" uses). It is looked up when completion runs.
func fromLister(parent string) idSource {
	return func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		if l, ok := listers[parent]; ok {
			return l(ctx, c)
		}
		return nil, nil
	}
}

// Pages are bounded: only the first page of a paged list is offered, so a
// match beyond it is not completed (typing it in full still works).
const (
	overviewPageSize = 50
	quotaPageSize    = 64 // the largest page the quota inventory route allows
)

// srcQuotaInventory is one page of ids of one kind of quota administration
// record: GET /api/v1/quota/inventory?kind=<kind>&limit=64 (admin).
func srcQuotaInventory(kind string) idSource {
	return func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		page, err := c.GetQuotaInventory(ctx, kind, "", quotaPageSize)
		if err != nil {
			return nil, err
		}
		return idsOf(page.Records, nil, func(r quotaplex.Record) string { return r.ID })
	}
}

// The sources. Each cites the read route it calls; every one is a GET.
var (
	// GET /api/v1/access-points/
	srcAccessPoints idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListAccessPoints(ctx)
		return idsOf(l, err, func(r aiplex.AccessPointRecord) string { return r.ID })
	}
	// GET /api/v1/llm/surfaces/
	srcSurfaces = fromLister("llm surfaces")
	// GET /api/v1/mcp/connections/
	srcToolConnections = fromLister("mcp connections")
	// GET /api/v1/a2a/connections/
	srcAgentConnections = fromLister("a2a connections")
	// GET /api/v1/budgets
	srcBudgets = fromLister("budgets")
	// GET /api/v1/usage/prices/
	srcPrices idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListModelPrices(ctx)
		return idsOf(l, err, func(r aiplex.ModelPriceResource) string { return r.ID })
	}
	// GET /api/v1/identity/user-providers
	srcUserProviders idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListUserIdentityProviders(ctx)
		return idsOf(l, err, func(p aiplex.UserIdentityProvider) string { return p.ID })
	}
	// GET /api/v1/hooks/policies/
	srcHookPolicies idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListHookPolicies(ctx)
		return idsOf(l, err, func(p aiplex.HookPolicyResource) string { return p.ID })
	}
	// GET /api/v1/clients/registrations
	srcClients idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListClientRegistrations(ctx)
		return idsOf(l, err, func(r aiplex.ClientRegistrationRecord) string { return r.ID })
	}
	// GET /api/v1/agents
	srcAgents idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListAgents(ctx)
		return idsOf(l, err, func(a aiplex.Agent) string { return a.ClientID })
	}
	// GET /api/v1/agents/{agent}/identities, the agent being the argument before.
	srcAgentIdentities idSource = func(ctx context.Context, c *aiplex.Client, prev []string) ([]string, error) {
		if len(prev) == 0 {
			return nil, nil
		}
		l, err := c.ListAgentIdentities(ctx, prev[0])
		return idsOf(l, err, func(b aiplex.AgentIdentityBinding) string { return b.ID })
	}
	// GET /api/v1/iam/role-bindings (admin)
	srcRoleBindings idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListRoleBindings(ctx, "")
		return idsOf(l, err, func(b aiplex.RoleBinding) string { return b.ID })
	}
	// GET /api/v1/a2a/delegations
	srcDelegations idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListDelegations(ctx)
		return idsOf(l, err, func(d aiplex.Delegation) string { return d.ID })
	}
	// GET /api/v1/runs
	srcRuns idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListRuns(ctx, aiplex.ListRunsOpts{Limit: completionMaxIDs})
		return idsOf(l, err, func(r aiplex.ExecutionRun) string { return r.RunID })
	}
	// GET /api/v1/instances
	srcInstances idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListInstances(ctx, nil)
		return idsOf(l, err, func(i aiplex.Instance) string { return i.ID })
	}
	// GET /api/v1/catalog?page_size=200
	srcTemplates idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		page, err := c.ListCatalog(ctx, &aiplex.ListCatalogOpts{PageSize: completionMaxIDs})
		if err != nil {
			return nil, err
		}
		return idsOf(page.Templates, nil, func(t aiplex.Template) string { return t.ID })
	}
	// GET /api/v1/modules (a module is named, not numbered)
	srcModules idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		l, err := c.ListModules(ctx)
		return idsOf(l, err, func(m aiplex.ModuleDescriptor) string { return m.Name })
	}
	// GET /api/v1/overview/subjects?kind=<kind>, the kind being the argument
	// before: one page of the callers of that kind.
	srcSubjectIDs idSource = func(ctx context.Context, c *aiplex.Client, prev []string) ([]string, error) {
		if len(prev) == 0 {
			return nil, nil
		}
		page, err := c.ListOverviewSubjects(ctx, aiplex.OverviewSubjectsQuery{Kind: prev[0], Limit: overviewPageSize})
		if err != nil {
			return nil, err
		}
		return idsOf(page.Subjects, nil, func(s aiplex.OverviewSubject) string { return s.ID })
	}
	// GET /api/v1/overview/subjects?kind=user, keeping only canonical principal
	// ids: a user scope is set for a principal id, and the overview also lists
	// users that an access point names by a free-form string, which is not one.
	srcPrincipals idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		page, err := c.ListOverviewSubjects(ctx, aiplex.OverviewSubjectsQuery{Kind: aiplex.CallerKindUser, Limit: overviewPageSize})
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for _, s := range page.Subjects {
			if auth.IsCanonicalPrincipalID(s.ID) {
				ids = append(ids, s.ID)
			}
		}
		return ids, nil
	}
	// GET /api/v1/iam/native/bindings/ (the agent-binding resources, which are
	// the native governance bindings).
	srcNativeBindings idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		return resourceIDs(ctx, c, aiplex.AgentBindings)
	}
	// GET /api/v1/usage/records?limit=: the newest page of the usage ledger.
	srcUsageRecords idSource = func(ctx context.Context, c *aiplex.Client, _ []string) ([]string, error) {
		page, err := c.QueryUsageRecords(ctx, aiplex.UsageRecordsQuery{Limit: quotaPageSize})
		if err != nil {
			return nil, err
		}
		return idsOf(page.Records, nil, func(r usageledger.Record) string { return r.ID })
	}
	// The provider ids the CLI knows: a fixed list, no request.
	srcProviders idSource = func(context.Context, *aiplex.Client, []string) ([]string, error) {
		return slices.Clone(aiplex.ModelProviderIDs), nil
	}
	// The quota inventory kinds (the list GetQuotaInventory accepts): a fixed
	// list, no request.
	srcInventoryKinds idSource = func(context.Context, *aiplex.Client, []string) ([]string, error) {
		return []string{"budgets", "loans", "attempts", "reconciliations", "audit", "terminal-work"}, nil
	}
	// The caller kinds: a fixed list, no request.
	srcSubjectKinds idSource = func(context.Context, *aiplex.Client, []string) ([]string, error) {
		return slices.Clone(aiplex.OverviewSubjectKinds), nil
	}
	// The resource kinds the CLI knows: a fixed list, no request.
	srcResourceKinds idSource = func(context.Context, *aiplex.Client, []string) ([]string, error) {
		kinds := make([]string, 0, len(resourceKinds))
		for _, k := range resourceKinds {
			kinds = append(kinds, string(k))
		}
		return kinds, nil
	}
	// GET <kind's resource route>/, the kind being the argument before.
	srcResourceIDs idSource = func(ctx context.Context, c *aiplex.Client, prev []string) ([]string, error) {
		if len(prev) == 0 {
			return nil, nil
		}
		kind, err := resourceKind(prev[0])
		if err != nil {
			return nil, err
		}
		return resourceIDs(ctx, c, kind)
	}
)

// completionTable maps a command path (without "aiplex ") to the source of
// each positional argument, in order. It is the only place id completion is
// registered: a new command that takes an id is one row here, or one entry in
// cannotCompleteIDs (completion_ids_test.go) saying why it cannot.
var completionTable = map[string][]idSource{
	"access-points get":         {srcAccessPoints},
	"access-points delete":      {srcAccessPoints},
	"access-points quota-plan":  {srcAccessPoints},
	"access-points purge-cache": {srcAccessPoints},
	"access-points routing":     {srcAccessPoints},
	"connect":                   {srcAccessPoints},

	"llm surfaces get":    {srcSurfaces},
	"llm surfaces delete": {srcSurfaces},
	"llm capacity":        {srcSurfaces},

	"mcp connections get":          {srcToolConnections},
	"mcp connections delete":       {srcToolConnections},
	"mcp connections test":         {srcToolConnections},
	"mcp connections call":         {srcToolConnections},
	"mcp connections rules add":    {srcToolConnections},
	"mcp connections rules list":   {srcToolConnections},
	"mcp connections rules remove": {srcToolConnections},
	"mcp connections rules set":    {srcToolConnections},
	"mcp connections rules test":   {srcToolConnections},
	"tools set-auth":               {srcToolConnections},
	"tool-auth start":              {srcToolConnections},
	"tool-auth status":             {srcToolConnections},
	"tool-auth revoke":             {srcToolConnections},
	"tool-auth register":           {srcToolConnections},
	"tool-auth complete":           {srcToolConnections},

	"a2a connections get":    {srcAgentConnections},
	"a2a connections delete": {srcAgentConnections},
	"a2a invoke":             {srcAgentConnections},
	"a2a card":               {srcInstances},
	"a2a delegations get":    {srcDelegations},
	"a2a delegations chain":  {srcDelegations},
	"a2a delegations update": {srcDelegations},

	"budgets get":    {srcBudgets},
	"budgets delete": {srcBudgets},
	"budgets status": {srcBudgets},
	"budgets update": {srcBudgets},

	"usage prices get":    {srcPrices},
	"usage prices delete": {srcPrices},

	"identity user-providers get":    {srcUserProviders},
	"identity user-providers delete": {srcUserProviders},
	"identity user-providers plan":   {srcUserProviders},

	"identity user-providers invalidate-membership": {srcUserProviders},

	"hooks policies get":    {srcHookPolicies},
	"hooks policies delete": {srcHookPolicies},
	"hooks policies test":   {srcHookPolicies},

	"clients get":    {srcClients},
	"clients delete": {srcClients},
	"clients plan":   {srcClients},

	"agents delete":          {srcAgents},
	"agents permissions":     {srcAgents},
	"agents identity list":   {srcAgents},
	"agents identity bind":   {srcAgents}, // the binding id after it is new
	"agents identity get":    {srcAgents, srcAgentIdentities},
	"agents identity delete": {srcAgents, srcAgentIdentities},
	"agents identity plan":   {srcAgents, srcAgentIdentities},

	"iam role-bindings get":    {srcRoleBindings},
	"iam role-bindings delete": {srcRoleBindings},

	"catalog get":    {srcTemplates},
	"deploy":         {srcTemplates},
	"get agent":      {srcAgents},
	"get delegation": {srcDelegations},
	"get instance":   {srcInstances},
	"logs":           {srcInstances},
	"undeploy":       {srcInstances},

	"runs inspect":     {srcRuns},
	"runs events":      {srcRuns},
	"runs effects":     {srcRuns},
	"runs obligations": {srcRuns},
	"runs budgets":     {srcRuns},
	"runs audit":       {srcRuns},
	"runs redrive":     {srcRuns},
	"runs reconcile":   {srcRuns},
	"runs cancel":      {srcRuns},
	"runs signal":      {srcRuns},
	"runs compensate":  {srcRuns},
	"runs compact":     {srcRuns},

	"modules inspect": {srcModules},

	"llm providers": {srcProviders},
	"who":           {srcSubjectKinds, srcSubjectIDs},

	"native governance plan": {srcNativeBindings},
	"native governance read": {srcNativeBindings},

	"agents user-scopes get": {srcPrincipals},
	"agents user-scopes set": {srcPrincipals},

	"usage get":      {srcUsageRecords},
	"usage feedback": {srcUsageRecords},

	"quota inventory":       {srcInventoryKinds},
	"quota attempt":         {srcQuotaInventory("attempts")},
	"quota balance":         {srcQuotaInventory("budgets")},
	"quota loan":            {srcQuotaInventory("loans")},
	"quota loan-return":     {srcQuotaInventory("loans")},
	"quota evidence":        {srcQuotaInventory("terminal-work")},
	"quota reconcile":       {srcQuotaInventory("reconciliations")},
	"quota reconcile-apply": {srcQuotaInventory("reconciliations")},

	"resources list":   {srcResourceKinds},
	"resources get":    {srcResourceKinds, srcResourceIDs},
	"resources delete": {srcResourceKinds, srcResourceIDs},
	"resources plan":   {srcResourceKinds, srcResourceIDs},
	"resources apply":  {srcResourceKinds, srcResourceIDs},
}

// contextCompletions complete the names of the CLI's own contexts, read from
// the local configuration file (no request).
var contextCompletions = []string{"ctx", "config use-context", "config delete-context", "config set-context"}

var accessPointLister lister = func(ctx context.Context, c *aiplex.Client) ([]string, error) {
	return srcAccessPoints(ctx, c, nil)
}

var priceLister lister = func(ctx context.Context, c *aiplex.Client) ([]string, error) {
	return srcPrices(ctx, c, nil)
}

// registerCompletions wires id completion into the command tree from
// completionTable.
func registerCompletions(root *cobra.Command) {
	registerLister("usage prices", priceLister)
	registerLister("identity user-providers", func(ctx context.Context, c *aiplex.Client) ([]string, error) {
		return srcUserProviders(ctx, c, nil)
	})
	byPath := map[string]*cobra.Command{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		byPath[strings.TrimPrefix(c.CommandPath(), "aiplex ")] = c
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	set := func(path string, f cobra.CompletionFunc) {
		if c, ok := byPath[path]; ok && c.ValidArgsFunction == nil && len(c.ValidArgs) == 0 {
			c.ValidArgsFunction = f
		}
	}
	for path, sources := range completionTable {
		set(path, completeArgs(sources...))
	}
	for _, p := range contextCompletions {
		set(p, completeContexts)
	}
	// Flags that name a resource.
	for path, c := range byPath {
		if f := c.Flags().Lookup("access-point"); f != nil && path != "" {
			_ = c.RegisterFlagCompletionFunc("access-point", completeWith(accessPointLister))
		}
		if f := c.Flags().Lookup("tool"); f != nil && strings.HasPrefix(path, "access-points") {
			_ = c.RegisterFlagCompletionFunc("tool", completeWith(listers["mcp connections"]))
		}
	}
	_ = root.RegisterFlagCompletionFunc("context", completeContexts)
	_ = root.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"table", "wide", "json", "yaml"}, cobra.ShellCompDirectiveNoFileComp
	})
}
