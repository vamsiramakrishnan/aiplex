package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Exit codes. Scripts may rely on these; they are listed in `aiplex --help`
// (see exitCodesHelp).
const (
	exitOK          = 0
	exitError       = 1 // anything else, including server errors (5xx)
	exitUsage       = 2 // bad flags or arguments, or a specification the server rejected (400/422)
	exitAuth        = 3 // not signed in, token rejected (401), or not permitted (403)
	exitNotFound    = 4 // the named resource does not exist (404)
	exitConflict    = 5 // the resource changed or already exists (409/412/428)
	exitUnreachable = 6 // the API could not be reached or did not answer
)

const exitCodesHelp = `Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached`

// cliError is an error already phrased for a person: what happened, then
// hints (why, and the next command to run).
type cliError struct {
	msg   string
	hints []string
	code  int
	err   error // underlying cause, shown with --debug
}

func (e *cliError) Error() string { return e.msg }
func (e *cliError) Unwrap() error { return e.err }

func newCLIError(code int, msg string, hints ...string) *cliError {
	return &cliError{msg: msg, hints: hints, code: code}
}

// usageError is a problem with flags or arguments.
func usageError(msg string, hints ...string) error { return newCLIError(exitUsage, msg, hints...) }

// reportError prints err for a person and returns the exit code.
func reportError(w io.Writer, cmd *cobra.Command, args []string, err error) int {
	ce := explain(cmd, args, err)
	prefix := "Error:"
	if colorEnabled(os.Stderr) {
		prefix = "\x1b[1;31mError:\x1b[0m"
	}
	fmt.Fprintln(w, prefix, ce.msg)
	for _, h := range ce.hints {
		fmt.Fprintln(w, "  "+h)
	}
	if debug {
		fmt.Fprintf(w, "[DEBUG] %T: %v\n", rootCause(err), err)
		var apiErr *aiplex.Error
		if errors.As(err, &apiErr) {
			fmt.Fprintf(w, "[DEBUG] HTTP %d code=%s request_id=%s trace_id=%s details=%v\n", apiErr.StatusCode, apiErr.Code, apiErr.RequestID, apiErr.TraceID, apiErr.Details)
		}
	}
	return ce.code
}

func rootCause(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

var (
	cobraArgsRe     = regexp.MustCompile(`^(accepts|requires at least|requires at most|accepts between) .*arg\(s\), (only )?received \d+$`)
	cobraUnknownRe  = regexp.MustCompile(`^unknown command "([^"]*)" for "([^"]*)"`)
	cobraRequiredRe = regexp.MustCompile(`^required flag\(s\) (.*) not set$`)
)

// explain turns any error into a cliError.
func explain(cmd *cobra.Command, args []string, err error) *cliError {
	var ce *cliError
	if errors.As(err, &ce) {
		return ce
	}
	msg := err.Error()
	path := "aiplex"
	if cmd != nil {
		path = cmd.CommandPath()
	}

	// Cobra's own argument and flag errors.
	switch {
	case cobraUnknownRe.MatchString(msg):
		first, _, _ := strings.Cut(msg, "\n")
		hints := []string{}
		if i := strings.Index(msg, "Did you mean this?"); i >= 0 {
			for _, s := range strings.Fields(msg[i+len("Did you mean this?"):]) {
				hints = append(hints, "Did you mean: "+path+" "+s)
				break
			}
		}
		hints = append(hints, "See the commands with: "+path+" --help")
		return &cliError{msg: first, hints: hints, code: exitUsage, err: err}
	case cobraRequiredRe.MatchString(msg):
		return &cliError{msg: path + ": " + msg, hints: usageHints(cmd), code: exitUsage, err: err}
	case cobraArgsRe.MatchString(msg):
		return &cliError{msg: path + ": " + argsProblem(cmd, args, msg), hints: usageHints(cmd), code: exitUsage, err: err}
	case strings.HasPrefix(msg, "unknown flag") || strings.HasPrefix(msg, "unknown shorthand flag") || strings.HasPrefix(msg, "invalid argument") || strings.Contains(msg, "flag needs an argument"):
		return &cliError{msg: msg, hints: usageHints(cmd), code: exitUsage, err: err}
	}

	t := resolveTarget()
	local := isLoopbackURL(t.URL)

	// Network.
	var opErr *net.OpError
	if errors.Is(err, syscall.ECONNREFUSED) || (errors.As(err, &opErr) && opErr.Op == "dial") || strings.Contains(msg, "connection refused") {
		if local {
			return &cliError{msg: "nothing is answering at " + t.URL + " (from " + t.URLSource + ")", code: exitUnreachable, err: err, hints: []string{
				"Start a local AIPlex with: aiplex dev   (or in the background: aiplex dev --detach)",
				"Or point the CLI elsewhere with --url, AIPLEX_URL or: aiplex config use-context <name>",
			}}
		}
		return &cliError{msg: "cannot reach " + t.URL + " (from " + t.URLSource + ")", code: exitUnreachable, err: err, hints: []string{
			"Check the URL with: aiplex config show",
			"Check the API is up with: aiplex health",
		}}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return &cliError{msg: t.URL + " did not answer in time", code: exitUnreachable, err: err, hints: []string{"Check the API is healthy with: aiplex health"}}
	}

	var apiErr *aiplex.Error
	var inputErr *aiplex.InputError
	if errors.As(err, &inputErr) {
		// The SDK refused before sending (docs/cli.md: invalid input is exit 2).
		return &cliError{msg: strings.TrimSuffix(msg, inputErr.Error()) + inputErr.Message, code: exitUsage, err: err}
	}
	if !errors.As(err, &apiErr) {
		return &cliError{msg: msg, code: exitError, err: err}
	}
	action := strings.TrimSuffix(strings.TrimSuffix(msg, apiErr.Error()), ": ")
	what := func(m string) string {
		if action != "" && action != msg {
			return action + ": " + m
		}
		return m
	}
	switch apiErr.StatusCode {
	case 401:
		return explainUnauthenticated(t, local, apiErr, what)
	case 403:
		return explainForbidden(apiErr, what)
	case 404:
		return explainNotFound(cmd, args, apiErr, local, what)
	case 409, 412, 428:
		hints := []string{"Nothing was changed."}
		if get := siblingCommand(cmd, "get"); get != "" && len(args) > 0 {
			hints = append(hints, "See the current version with: "+get+" "+args[0])
		}
		hints = append(hints, "Run the command again to apply your change on top of the latest revision.")
		m := "it was changed by someone else since it was read, or it already exists"
		if apiErr.Code == "REVISION_REQUIRED" {
			m = "the server needs the current revision for this change"
		}
		return &cliError{msg: what(m), hints: hints, code: exitConflict, err: err}
	case 400, 422:
		hints := []string{}
		if f := apiErr.Field(); f != "" {
			hints = append(hints, "Field: "+f)
			if choices := fieldChoices(f); choices != "" {
				hints = append(hints, choices)
			}
		}
		if strings.Contains(apiErr.Message, "origin not approved") {
			hints = append(hints, "The API only calls upstream origins its network policy approves.")
			if local {
				hints = append(hints, "With aiplex dev, restart it with: aiplex dev --allow-origin <scheme://host[:port]>")
			} else {
				hints = append(hints, "Ask the operator to add the origin to the API's network policy (network.allowed_origins).")
			}
		}
		if strings.Contains(path, " apply") || strings.Contains(path, " put") || strings.Contains(path, " set") {
			hints = append(hints, "Nothing was changed. Fix the specification and run the command again.")
		}
		return &cliError{msg: what(apiErr.Message), hints: hints, code: exitUsage, err: err}
	case 429:
		return &cliError{msg: what("rate limited: " + apiErr.Message), hints: []string{"Wait a moment and try again."}, code: exitError, err: err}
	}
	if apiErr.StatusCode >= 500 {
		hints := []string{}
		if apiErr.RequestID != "" {
			hints = append(hints, "Request ID: "+apiErr.RequestID)
		}
		if apiErr.TraceID != "" {
			hints = append(hints, "Trace ID: "+apiErr.TraceID)
		}
		if apiErr.Code == "AGENT_CREDENTIAL_REJECTED" {
			// The runtime refused the gateway's credential for the connection,
			// not the caller's token.
			hints = append(hints, "Your token was accepted; the agent runtime refused the credential the API holds for this connection. Check its identity profile with: aiplex a2a connections get <id>")
			if local {
				hints = append(hints, "With aiplex dev, that credential is the mock agent's token in the API's environment; restart aiplex dev to get a matching pair.")
			}
		}
		if local {
			if p, e := devLogPath(); e == nil {
				hints = append(hints, "The API log is at "+p)
			}
		}
		m := apiErr.Message
		if m == "" || strings.HasPrefix(m, fmt.Sprint(apiErr.StatusCode)) {
			m = "the API failed (" + fmt.Sprint(apiErr.StatusCode) + ")"
		}
		return &cliError{msg: what(m), hints: hints, code: exitError, err: err}
	}
	return &cliError{msg: what(apiErr.Message), code: exitError, err: err}
}

func usageHints(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	hints := []string{"Usage: " + cmd.UseLine()}
	if ex := firstExample(cmd); ex != "" {
		hints = append(hints, "Example: "+ex)
	}
	return append(hints, "More: "+cmd.CommandPath()+" --help")
}

func firstExample(cmd *cobra.Command) string {
	for _, line := range strings.Split(cmd.Example, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "aiplex ") {
			return line
		}
	}
	return ""
}

func explainUnauthenticated(t target, local bool, apiErr *aiplex.Error, what func(string) string) *cliError {
	where := t.URL
	if t.Context != "" && strings.HasPrefix(t.URLSource, "context") {
		where = "context " + t.Context + " (" + t.URL + ")"
	}
	var msg string
	switch {
	case t.Token == "":
		msg = "not signed in to " + where
	case !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt):
		msg = "your token for " + where + " expired " + relTimeFrom(t.ExpiresAt, time.Now())
	default:
		if exp, ok := jwtExpiry(t.Token); ok && time.Now().After(exp) {
			msg = "your token (from " + t.TokenSource + ") expired " + relTimeFrom(exp, time.Now())
		} else {
			msg = "the API did not accept your token (from " + t.TokenSource + ")"
		}
	}
	hints := []string{}
	if local {
		hints = append(hints, "For a local API started with `aiplex dev`, run: eval \"$(aiplex dev env)\"",
			"A token from an earlier `aiplex dev` run is not valid after it restarts.")
	} else {
		login := "aiplex login"
		if t.Context != "" {
			login += " --context " + t.Context
		}
		hints = append(hints, "Sign in with: "+login)
	}
	if t.TokenSource == "AIPLEX_TOKEN" || t.TokenSource == "--token" {
		hints = append(hints, "The token came from "+t.TokenSource+"; unset it to use the stored context token.")
	}
	return &cliError{msg: what(msg), hints: hints, code: exitAuth, err: apiErr}
}

var requiredRolesRe = regexp.MustCompile(`required one of: ([a-z, ]+)`)

func explainForbidden(apiErr *aiplex.Error, what func(string) string) *cliError {
	msg := "permission denied"
	if m := requiredRolesRe.FindStringSubmatch(apiErr.Message); m != nil {
		roles := strings.Split(strings.ReplaceAll(m[1], " ", ""), ",")
		msg += ": this needs the " + joinOr(roles) + " role"
	} else if apiErr.Message != "" {
		msg += ": " + apiErr.Message
	}
	hints := []string{}
	if who := describeCaller(); who != "" {
		hints = append(hints, "You are "+who+".")
	}
	hints = append(hints, "See your roles and grants with: aiplex whoami --server",
		"An administrator grants roles through role bindings for your group, and model or tool access with: aiplex agents user-scopes set")
	return &cliError{msg: what(msg), hints: hints, code: exitAuth, err: apiErr}
}

// describeCaller asks the server who the caller is, briefly; "" on failure.
func describeCaller() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	id, err := newClient().GetIdentity(ctx)
	if err != nil {
		return ""
	}
	name := id.Identity.Email
	if name == "" {
		name = id.Identity.Subject
	}
	if name == "" {
		name = id.Identity.PrincipalID
	}
	roles := "no role"
	if len(id.Roles) > 0 {
		roles = "role " + strings.Join(id.Roles, ", ")
	}
	return name + " with " + roles
}

func joinOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// ---- not found -------------------------------------------------------------

// lister returns the ids of one kind of resource, for "did you mean".
type lister func(ctx context.Context, c *aiplex.Client) ([]string, error)

// listers maps a parent command path (without "aiplex ") to how its ids are
// listed. Commands under that parent that take an id get "did you mean".
var listers = map[string]lister{}

func registerLister(parent string, l lister) { listers[parent] = l }

func explainNotFound(cmd *cobra.Command, args []string, apiErr *aiplex.Error, local bool, what func(string) string) *cliError {
	kind, id := "", ""
	generic := strings.HasSuffix(apiErr.Message, "not found") && len(apiErr.Message) <= 40
	if cmd != nil && cmd.HasParent() && len(args) > 0 && generic {
		kind = kindName(cmd.Parent())
		id = args[0]
	}
	msg := what(apiErr.Message)
	hints := []string{}
	if kind != "" && id != "" {
		msg = fmt.Sprintf("%s %q not found", kind, id)
		parent := strings.TrimPrefix(cmd.Parent().CommandPath(), "aiplex ")
		if l, ok := listers[parent]; ok {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			ids, err := l(ctx, newClient())
			cancel()
			if err == nil {
				if near := nearest(id, ids); len(near) > 0 {
					hints = append(hints, "Did you mean: "+strings.Join(near, ", "))
				} else if len(ids) == 0 {
					hints = append(hints, "There are none yet.")
				}
			}
		}
		if list := siblingCommand(cmd, "list"); list != "" {
			hints = append(hints, "List them with: "+list)
		}
	}
	if kind == "" && apiErr.Code == "unknown" {
		// No JSON error body: the route itself is not mounted.
		return &cliError{msg: what("this API does not serve this command (the feature is not enabled on it, or the API is older than the CLI)"), code: exitNotFound, err: apiErr,
			hints: []string{"See what the API has enabled with: aiplex modules list"}}
	}
	if local && devRestartedSinceLastWrite(resolveTarget().URL) {
		hints = append(hints, "aiplex dev keeps state in memory and was restarted after you last created something here: anything created before that is gone.")
	}
	return &cliError{msg: msg, hints: hints, code: exitNotFound, err: apiErr}
}

// kindName turns a command group into a resource name: "access-points" ->
// "access point", "surfaces" -> "model surface".
func kindName(parent *cobra.Command) string {
	if k, ok := parent.Annotations["kind"]; ok {
		return k
	}
	name := strings.TrimSuffix(parent.Name(), "s")
	return strings.ReplaceAll(name, "-", " ")
}

// siblingCommand returns "aiplex <parent> <verb>" when the parent has it.
func siblingCommand(cmd *cobra.Command, verb string) string {
	if cmd == nil || !cmd.HasParent() {
		return ""
	}
	for _, c := range cmd.Parent().Commands() {
		if c.Name() == verb {
			return c.CommandPath()
		}
	}
	return ""
}

// nearest returns up to three ids close to want (edit distance or prefix).
func nearest(want string, ids []string) []string {
	type cand struct {
		id string
		d  int
	}
	var cands []cand
	limit := len(want)/3 + 1
	if limit > 3 {
		limit = 3
	}
	for _, id := range ids {
		d := levenshtein(strings.ToLower(want), strings.ToLower(id))
		if d <= limit || (len(want) >= 3 && (strings.HasPrefix(id, want) || strings.HasPrefix(want, id))) {
			cands = append(cands, cand{id, d})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].d < cands[j].d || (cands[i].d == cands[j].d && cands[i].id < cands[j].id)
	})
	out := []string{}
	for i := 0; i < len(cands) && i < 3; i++ {
		out = append(out, cands[i].id)
	}
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func devLogPath() (string, error) {
	p, err := devStatePath()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(p, "dev.json") + "dev-api.log", nil
}

// argsProblem rephrases cobra's "accepts 1 arg(s), received 0" using the
// argument names in the command's Use line.
func argsProblem(cmd *cobra.Command, args []string, msg string) string {
	if cmd == nil {
		return msg
	}
	var names []string
	for _, f := range strings.Fields(cmd.Use)[1:] {
		if strings.HasPrefix(f, "<") && strings.HasSuffix(f, ">") {
			names = append(names, f)
		}
	}
	if len(args) < len(names) {
		return "missing " + strings.Join(names[len(args):], " ")
	}
	if len(names) > 0 && len(args) > len(names) {
		return fmt.Sprintf("too many arguments: expected %s, got %q", strings.Join(names, " "), strings.Join(args, " "))
	}
	if len(names) == 0 && len(args) > 0 {
		return fmt.Sprintf("takes no arguments, got %q", strings.Join(args, " "))
	}
	return msg
}

// rejectUnknownSubcommands makes a command group fail (exit 2, with a
// suggestion) on an unknown subcommand instead of printing help and exiting 0.
func rejectUnknownSubcommands(c *cobra.Command) {
	for _, sub := range c.Commands() {
		rejectUnknownSubcommands(sub)
	}
	if !c.HasSubCommands() || c.Runnable() || !c.HasParent() {
		return
	}
	c.Args = cobra.ArbitraryArgs
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		hints := []string{}
		var names []string
		for _, sub := range cmd.Commands() {
			if !sub.Hidden {
				names = append(names, sub.Name())
			}
		}
		if s := nearest(args[0], names); len(s) > 0 {
			hints = append(hints, "Did you mean: "+cmd.CommandPath()+" "+s[0])
		}
		hints = append(hints, "See the commands with: "+cmd.CommandPath()+" --help")
		return &cliError{msg: fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath()), hints: hints, code: exitUsage}
	}
}

// fieldChoices lists the valid values for a reference field the server
// rejected, when the CLI can list them.
func fieldChoices(field string) string {
	var parent, label string
	switch {
	case strings.HasSuffix(field, "surface_ref"):
		parent, label = "llm surfaces", "Model surfaces"
	case strings.HasSuffix(field, "connection_ref"):
		parent, label = "mcp connections", "Tool connections"
	default:
		return ""
	}
	l, ok := listers[parent]
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ids, err := l(ctx, newClient())
	if err != nil {
		return ""
	}
	if len(ids) == 0 {
		return label + ": none yet (create one with: aiplex " + parent + " add)"
	}
	sort.Strings(ids)
	return label + ": " + strings.Join(ids, ", ")
}
