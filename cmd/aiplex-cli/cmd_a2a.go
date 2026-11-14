package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func a2aCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "a2a",
		Short: "A2APlex — invoke agents, manage connections and read delegation history",
		Long: `A2APlex reaches agents two ways. Deployed A2A agents are listed with 'agents'
and 'card'. Cloud agents (Google, AWS, Foundry, Copilot Studio) are reached
through an agent connection, which pins the agent's endpoint and the identity
reference used to call it:

  aiplex resources apply agent-connection bedrock -f connection.yaml
  aiplex a2a connections list
  aiplex a2a invoke bedrock --operation invoke --payload '{"input":"hello"}'

Delegations are reported history: a record says an agent reported handing a task
to another, not that the call was authorized or that the reporter is who it says.`,
		Example: `  aiplex a2a agents
  aiplex a2a connections list
  aiplex a2a delegations list --agent research-agent`,
	}

	cmd.AddCommand(a2aAgentsCmd(), a2aDelegateCmd(), a2aCardCmd(), a2aInvokeCmd(), a2aDelegationsCmd(), a2aConnectionsCmd())
	return cmd
}

func a2aAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "List running A2A agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			cards, err := c.ListAgentCards(context.Background())
			if err != nil {
				return fmt.Errorf("list a2a agents: %w", err)
			}

			if output == "json" {
				printJSON(cards)
				return nil
			}

			headers := []string{"INSTANCE", "NAME", "URL", "STATUS"}
			var rows [][]string
			for _, card := range cards {
				name := fmt.Sprintf("%v", card["name"])
				url := fmt.Sprintf("%v", card["url"])
				status := fmt.Sprintf("%v", card["status"])
				instID := fmt.Sprintf("%v", card["instance_id"])
				rows = append(rows, []string{instID, name, url, status})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d A2A agent(s)\n", len(cards))
			if len(cards) == 0 {
				emptyInstancesHint(cmd.ErrOrStderr())
			}
			return nil
		},
	}
}

func a2aDelegateCmd() *cobra.Command {
	var (
		caller   string
		callee   string
		taskType string
		userID   string
	)

	cmd := &cobra.Command{
		Use:   "delegate",
		Short: "Record a new agent-to-agent delegation",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()

			d, err := c.RecordDelegation(context.Background(), &aiplex.RecordDelegationRequest{
				CallerAgentID: caller,
				CalleeAgentID: callee,
				TaskType:      taskType,
				UserID:        userID,
			})
			if err != nil {
				return fmt.Errorf("record delegation: %w", err)
			}

			if output == "json" {
				printJSON(d)
				return nil
			}

			fmt.Printf("Delegation: %s\n", d.ID)
			fmt.Printf("  %s → %s [%s]\n", d.CallerAgentID, d.CalleeAgentID, d.TaskType)
			fmt.Printf("  Status: %s\n", d.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&caller, "caller", "", "Caller agent ID (required)")
	cmd.Flags().StringVar(&callee, "callee", "", "Callee agent ID (required)")
	cmd.Flags().StringVarP(&taskType, "task", "t", "", "Task type (required)")
	cmd.Flags().StringVarP(&userID, "user", "u", "", "User ID")
	cmd.MarkFlagRequired("caller")
	cmd.MarkFlagRequired("callee")
	cmd.MarkFlagRequired("task")

	return cmd
}

func a2aCardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "card <instance-id>",
		Short: "Get the Agent Card for a deployed A2A agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			card, err := c.GetAgentCard(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get agent card: %w", err)
			}

			if output == "json" {
				printJSON(card)
				return nil
			}

			fmt.Printf("Agent Card: %s\n", card.Name)
			fmt.Printf("  URL:     %s\n", card.URL)
			fmt.Printf("  Version: %s\n", card.Version)
			fmt.Println("  Task Types:")
			for _, tt := range card.TaskTypes {
				fmt.Printf("    - %s: %s\n", tt.Type, tt.Description)
			}
			if len(card.AuthSchemes) > 0 {
				fmt.Println("  Auth Schemes:")
				for _, as := range card.AuthSchemes {
					fmt.Printf("    - %s\n", as.Scheme)
				}
			}
			return nil
		},
	}
}

const delegationCaveat = "Delegations are reported history, not proof: a record does not show that the call was authorized or that the reporting agent is who it says."

// a2aErr turns a refusal the SDK made before sending (a malformed ID, a value
// out of range) into a usage error, and wraps everything else with the action.
func a2aErr(action string, err error) error {
	var in *aiplex.InputError
	if errors.As(err, &in) {
		return usageError(in.Message)
	}
	return fmt.Errorf("%s: %w", action, err)
}

const maxInvokeBytes = 1 << 20

// a2aPayload reads the invocation payload from --payload or --payload-file.
// The content is never put in an error message.
func a2aPayload(cmd *cobra.Command, inline, file string) (json.RawMessage, error) {
	inlineSet, fileSet := cmd.Flags().Changed("payload"), cmd.Flags().Changed("payload-file")
	switch {
	case inlineSet && fileSet:
		return nil, usageError("give the payload with --payload or --payload-file, not both")
	case !inlineSet && !fileSet:
		return nil, nil
	}
	var raw []byte
	if inlineSet {
		raw = []byte(inline)
	} else {
		var err error
		if file == "-" {
			raw, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxInvokeBytes+1))
		} else {
			var f *os.File
			if f, err = os.Open(file); err == nil {
				raw, err = io.ReadAll(io.LimitReader(f, maxInvokeBytes+1))
				_ = f.Close()
			}
		}
		if err != nil {
			return nil, usageError("cannot read the payload from " + file + ": " + shortIOError(err))
		}
	}
	if len(raw) > maxInvokeBytes {
		return nil, usageError("the payload is larger than the 1 MiB the API accepts")
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, usageError("the payload is empty", "Leave --payload out to send no payload, or give a JSON value")
	}
	if !json.Valid(raw) {
		return nil, usageError("the payload is not valid JSON")
	}
	return json.RawMessage(raw), nil
}

func shortIOError(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

func a2aInvokeCmd() *cobra.Command {
	var f struct {
		operation, session, payload, payloadFile, contentType, accept, qualifier, watermark string
	}
	cmd := &cobra.Command{
		Use:   "invoke <connection> --operation <operation> [--payload '<json>' | --payload-file <file>|-]",
		Short: "Invoke the cloud agent behind an agent connection",
		Long: `Sends one invocation through the gateway to the agent an agent connection
points at, with the connection's identity reference. The reply is the provider's
own and is written to stdout as it arrives, so a streamed (server-sent events)
reply appears incrementally. The payload is sent as given and is not logged.

The operation is the provider's: 'invoke' for Google Agent Runtime and AWS
AgentCore Runtime, 'agent-responses' for Foundry agents, 'start-conversation'
and others for Copilot Studio. The caller needs the grant
a2a:agent:<connection>:<operation>; without it the call is refused (exit 3).

With -o json (or yaml) the whole reply is read first and printed as one object
with the HTTP status, content type, request id and body.`,
		Example: `  aiplex a2a invoke bedrock --operation invoke --payload '{"input":"hello"}'
  aiplex a2a invoke vertex --operation invoke --session s-1 --payload-file request.json
  echo '{"input":"hi"}' | aiplex a2a invoke bedrock --operation invoke --payload-file - -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.operation == "" {
				return usageError("--operation is required", "The operation depends on the provider; see: aiplex a2a invoke --help")
			}
			payload, err := a2aPayload(cmd, f.payload, f.payloadFile)
			if err != nil {
				return err
			}
			resp, err := newClient().InvokeAgent(cmd.Context(), args[0], aiplex.InvokeAgentRequest{
				Operation: f.operation, SessionID: f.session, Payload: payload, ContentType: f.contentType,
				Accept: f.accept, Qualifier: f.qualifier, Watermark: f.watermark,
			})
			if err != nil {
				return a2aErr("invoke agent "+args[0], err)
			}
			defer resp.Body.Close()
			out := cmd.OutOrStdout()
			if !machineOutput() {
				if _, err = io.Copy(out, resp.Body); err != nil {
					return fmt.Errorf("the reply from %s was cut off: %w", args[0], err)
				}
				return nil
			}
			const maxBuffered = 32 << 20
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxBuffered+1))
			if err != nil {
				return fmt.Errorf("the reply from %s was cut off: %w", args[0], err)
			}
			if len(body) > maxBuffered {
				return fmt.Errorf("the reply from %s is larger than 32 MiB; read it without -o json to stream it", args[0])
			}
			reply := struct {
				Status      int    `json:"status"`
				ContentType string `json:"content_type,omitempty"`
				RequestID   string `json:"request_id,omitempty"`
				Body        any    `json:"body"`
			}{resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("X-Request-Id"), string(body)}
			if json.Valid(body) {
				reply.Body = json.RawMessage(body)
			}
			spStructured(cmd, reply)
			return nil
		},
	}
	cmd.Flags().StringVar(&f.operation, "operation", "", "The provider's operation, such as invoke (required)")
	cmd.Flags().StringVar(&f.session, "session", "", "Provider session ID to continue")
	cmd.Flags().StringVar(&f.payload, "payload", "", "JSON payload, sent as given")
	cmd.Flags().StringVar(&f.payloadFile, "payload-file", "", "Read the JSON payload from a file, or - for stdin")
	cmd.Flags().StringVar(&f.contentType, "content-type", "", "Content type of the payload, when the provider needs one")
	cmd.Flags().StringVar(&f.accept, "accept", "", "Reply type to ask the provider for")
	cmd.Flags().StringVar(&f.qualifier, "qualifier", "", "Provider qualifier, such as an AgentCore runtime version")
	cmd.Flags().StringVar(&f.watermark, "watermark", "", "Provider watermark to resume a conversation from")
	return cmd
}

func a2aDelegationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "delegations",
		Aliases:     []string{"delegation", "del"},
		Short:       "List, read and update the delegation history",
		Annotations: map[string]string{"kind": "delegation"},
		Long:        "Delegations are reported history. " + delegationCaveat,
		Example: `  aiplex a2a delegations list --agent research-agent --limit 20
  aiplex a2a delegations chain d-1`,
	}
	cmd.AddCommand(a2aDelegationsListCmd(), a2aDelegationsGetCmd(), a2aDelegationsChainCmd(), a2aDelegationsUpdateCmd())
	return cmd
}

func delegationDuration(d aiplex.Delegation) string {
	if d.CompletedAt == nil {
		return "unknown" // not finished: the duration is not known yet
	}
	return fmt.Sprintf("%dms", d.DurationMs)
}

func a2aDelegationsListCmd() *cobra.Command {
	var agent string
	var limit int
	cmd := &cobra.Command{
		Use:     "list [--agent <id>] [--limit N]",
		Aliases: []string{"ls"},
		Short:   "List recent delegations, optionally for one agent",
		Example: "  aiplex a2a delegations list\n  aiplex a2a delegations list --agent research-agent --limit 20 -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := aiplex.ListDelegationsOptions{AgentID: agent}
			if cmd.Flags().Changed("limit") {
				if limit < 1 || limit > 1000 {
					return usageError("--limit must be between 1 and 1000")
				}
				opts.Limit = limit
			}
			list, err := newClient().ListDelegationsWithOptions(cmd.Context(), opts)
			if err != nil {
				return a2aErr("list delegations", err)
			}
			if list == nil {
				list = []aiplex.Delegation{}
			}
			if spStructured(cmd, list) {
				return nil
			}
			out := cmd.OutOrStdout()
			if len(list) == 0 {
				if agent != "" {
					fmt.Fprintf(out, "No delegations for agent %s.\n", agent)
				} else {
					fmt.Fprintln(out, "No delegations.")
				}
				return nil
			}
			t := newTable(keyCol("ID"), idCol("CALLER"), idCol("CALLEE"), col("TASK"), col("STATUS"), col("DURATION"), wideCol("REPORTED BY"), wideCol("STARTED"))
			for _, d := range list {
				t.add(d.ID, d.CallerAgentID, d.CalleeAgentID, d.TaskType, d.Status, delegationDuration(d), spDash(d.ReportedBy), d.StartedAt.Format("2006-01-02 15:04:05"))
			}
			t.render(out)
			fmt.Fprintf(out, "\n%d delegation(s)", len(list))
			shown := opts.Limit
			if shown == 0 {
				shown = 50 // the API's default page
			}
			if len(list) >= shown {
				fmt.Fprintf(out, "; this is the most recent page, raise --limit (up to 1000) to see more")
			}
			fmt.Fprintf(out, "\n%s\n", delegationCaveat)
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Only delegations where this agent is the caller or callee")
	cmd.Flags().IntVar(&limit, "limit", 0, "How many to list (1-1000; the API's default is 50)")
	return cmd
}

// writeDelegation prints one delegation. A value the API did not return is
// shown as "-", never as zero.
func writeDelegation(w io.Writer, d *aiplex.Delegation) {
	fmt.Fprintf(w, "Delegation: %s\n", d.ID)
	fmt.Fprintf(w, "  Caller:   %s\n", spDash(d.CallerAgentID))
	fmt.Fprintf(w, "  Callee:   %s\n", spDash(d.CalleeAgentID))
	fmt.Fprintf(w, "  Task:     %s\n", spDash(d.TaskType))
	fmt.Fprintf(w, "  Status:   %s\n", spDash(d.Status))
	fmt.Fprintf(w, "  User:     %s\n", spDash(d.UserID))
	fmt.Fprintf(w, "  Started:  %s\n", d.StartedAt.Format("2006-01-02 15:04:05"))
	if d.CompletedAt != nil {
		fmt.Fprintf(w, "  Ended:    %s\n", d.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(w, "  Duration: %s\n", delegationDuration(*d))
	if d.Error != "" {
		fmt.Fprintf(w, "  Error:    %s\n", d.Error)
	}
	fmt.Fprintf(w, "  Reported by: %s\n", spDash(d.ReportedBy))
	if d.TraceID != "" {
		fmt.Fprintf(w, "  Trace:    %s\n", d.TraceID)
	}
	fmt.Fprintf(w, "%s\n", delegationCaveat)
}

func writeDelegationChain(w io.Writer, ch *aiplex.DelegationChain, depth int) {
	indent := strings.Repeat("  ", depth)
	root := ch.RootDelegation
	fmt.Fprintf(w, "%s%s -> %s [%s] (%s)\n", indent, root.CallerAgentID, root.CalleeAgentID, root.TaskType, root.Status)
	for _, child := range ch.Children {
		fmt.Fprintf(w, "%s  \\_ %s -> %s [%s] (%s)\n", indent, child.CallerAgentID, child.CalleeAgentID, child.TaskType, child.Status)
	}
	fmt.Fprintf(w, "%sDepth: %d; %s\n", indent, ch.Depth, chainSpanTime(ch))
	fmt.Fprintf(w, "%s%s\n", indent, delegationCaveat)
}

func a2aDelegationsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show"},
		Short:   "Show one delegation",
		Example: "  aiplex a2a delegations get d-1\n  aiplex a2a delegations get d-1 -o yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := newClient().GetDelegation(cmd.Context(), args[0])
			if err != nil {
				return a2aErr("get delegation", err)
			}
			if spStructured(cmd, d) {
				return nil
			}
			writeDelegation(cmd.OutOrStdout(), d)
			fmt.Fprintf(cmd.OutOrStdout(), "Chain: aiplex a2a delegations chain %s\n", args[0])
			return nil
		},
	}
}

func a2aDelegationsChainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chain <id>",
		Short: "Show a delegation and every delegation made under it",
		Long: `Shows the delegation and all its descendants, parents before children. The
API refuses a chain it cannot return whole (more than 10,000 spans, or a loop)
instead of returning part of it. ` + delegationCaveat,
		Example: "  aiplex a2a delegations chain d-1 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := newClient().GetDelegationChain(cmd.Context(), args[0])
			if err != nil {
				return a2aErr("get delegation chain", err)
			}
			if spStructured(cmd, ch) {
				return nil
			}
			writeDelegationChain(cmd.OutOrStdout(), ch, 0)
			return nil
		},
	}
}

func a2aDelegationsUpdateCmd() *cobra.Command {
	var status, errText string
	var clearError bool
	cmd := &cobra.Command{
		Use:   "update <id> --status <pending|running|completed|failed> [--error <text> | --clear-error]",
		Short: "Move a reported delegation to a new status",
		Long: `Sets the status of a delegation record. Pending or running records may move to
a later state; a completed or failed record is final (exit 5 if you try to change
it). Completing or failing a record also stamps its end time and duration.

The update replaces the record's error text. A record that already has one is
therefore refused until you say what to do with it: --error <text> to set new
text, or --clear-error to remove it. ` + delegationCaveat,
		Example: "  aiplex a2a delegations update d-1 --status completed\n  aiplex a2a delegations update d-2 --status failed --error 'upstream timeout'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if status == "" {
				return usageError("--status is required", "One of: pending, running, completed, failed")
			}
			if cmd.Flags().Changed("error") && clearError {
				return usageError("use --error to set the error text or --clear-error to remove it, not both")
			}
			req := &aiplex.UpdateDelegationRequest{Status: status, Error: errText}
			if err := req.Validate(); err != nil {
				return a2aErr("update delegation", err)
			}
			c := newClient()
			current, err := c.GetDelegation(cmd.Context(), args[0])
			if err != nil {
				return a2aErr("read delegation", err)
			}
			if current.Error != "" && !cmd.Flags().Changed("error") && !clearError {
				return usageError("delegation "+args[0]+" has an error text ("+truncateRule(current.Error, 80)+") that this update would remove",
					"Replace it with: aiplex a2a delegations update "+args[0]+" --status "+status+" --error TEXT",
					"Remove it with: aiplex a2a delegations update "+args[0]+" --status "+status+" --clear-error")
			}
			updated, err := c.UpdateDelegation(cmd.Context(), args[0], req)
			if err != nil {
				return a2aErr("update delegation", err)
			}
			if spStructured(cmd, updated) {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated delegation %s: %s -> %s\n", args[0], spDash(current.Status), spDash(updated.Status))
			if updated.Error != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Error: %s\n", updated.Error)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Show it with: aiplex a2a delegations get %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "New status: pending, running, completed or failed (required)")
	cmd.Flags().StringVar(&errText, "error", "", "Error text to store with the record")
	cmd.Flags().BoolVar(&clearError, "clear-error", false, "Remove the stored error text")
	return cmd
}

// agentConnView is the part of an agent-connection spec the guided commands
// show. It holds references only, never credentials.
type agentConnView struct {
	Provider             string `json:"provider"`
	Endpoint             string `json:"endpoint"`
	ResourceID           string `json:"resource_id"`
	IdentityRef          string `json:"identity_ref"`
	NetworkProfileRef    string `json:"network_profile_ref"`
	GovernanceBindingRef string `json:"governance_binding_ref"`
	GovernanceMode       string `json:"governance_mode"`
	Region               string `json:"region"`
	Project              string `json:"project"`
	AgentID              string `json:"agent_id"`
	AliasID              string `json:"alias_id"`
}

func decodeAgentConnView(r resources.Record) agentConnView {
	var v agentConnView
	_ = json.Unmarshal(r.Spec, &v)
	return v
}

func unknownIfEmpty(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func a2aConnectionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "connections",
		Aliases:     []string{"connection", "conn"},
		Short:       "List, inspect and delete agent connections",
		Annotations: map[string]string{"kind": "agent connection"},
		Long: `An agent connection pins a cloud agent's endpoint and the identity reference
used to call it. Create or replace one with the generic resource command, then
invoke it with 'aiplex a2a invoke':

  aiplex resources apply agent-connection bedrock -f connection.yaml`,
		Example: "  aiplex a2a connections list\n  aiplex a2a connections get bedrock",
	}
	cmd.AddCommand(a2aConnListCmd(), a2aConnGetCmd(), a2aConnDeleteCmd())
	registerLister("a2a connections", func(ctx context.Context, c *aiplex.Client) ([]string, error) {
		return resourceIDs(ctx, c, aiplex.AgentConnections)
	})
	return cmd
}

func a2aConnListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List agent connections",
		Example: "  aiplex a2a connections list\n  aiplex a2a connections list -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := newClient().ListResources(cmd.Context(), aiplex.AgentConnections)
			if err != nil {
				return a2aErr("list agent connections", err)
			}
			if list == nil {
				list = []resources.Record{}
			}
			if spStructured(cmd, list) {
				return nil
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No agent connections. Create one with: aiplex resources apply agent-connection ID -f connection.yaml")
				return nil
			}
			t := newTable(keyCol("ID"), col("PROVIDER"), idCol("ENDPOINT"), col("IDENTITY"), wideCol("MODE"), wideCol("REVISION"))
			for _, r := range list {
				v := decodeAgentConnView(r)
				t.add(r.ID, unknownIfEmpty(v.Provider), unknownIfEmpty(v.Endpoint), unknownIfEmpty(v.IdentityRef), unknownIfEmpty(v.GovernanceMode), fmt.Sprint(r.Revision))
			}
			t.render(cmd.OutOrStdout())
			return nil
		},
	}
}

func a2aConnGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show", "describe"},
		Short:   "Show one agent connection",
		Example: "  aiplex a2a connections get bedrock\n  aiplex a2a connections get bedrock -o yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newClient().GetResource(cmd.Context(), aiplex.AgentConnections, args[0])
			if err != nil {
				return a2aErr("get agent connection", err)
			}
			if spStructured(cmd, r) {
				return nil
			}
			v := decodeAgentConnView(*r)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Agent connection %s (revision %d)\n", r.ID, r.Revision)
			fmt.Fprintf(out, "  Provider: %s\n  Endpoint: %s\n  Resource: %s\n  Identity: %s (a reference; the credential is not stored here)\n",
				unknownIfEmpty(v.Provider), unknownIfEmpty(v.Endpoint), unknownIfEmpty(v.ResourceID), unknownIfEmpty(v.IdentityRef))
			for _, kv := range [][2]string{{"Region", v.Region}, {"Project", v.Project}, {"Agent", v.AgentID}, {"Alias", v.AliasID},
				{"Network", v.NetworkProfileRef}, {"Governance", v.GovernanceBindingRef}, {"Mode", v.GovernanceMode}} {
				if kv[1] != "" {
					fmt.Fprintf(out, "  %-9s %s\n", kv[0]+":", kv[1])
				}
			}
			return nil
		},
	}
}

func a2aConnDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete an agent connection",
		Example: "  aiplex a2a connections delete bedrock --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			current, err := c.GetResource(cmd.Context(), aiplex.AgentConnections, args[0])
			if err != nil {
				return a2aErr("read agent connection", err)
			}
			if err = confirm(cmd, yes, fmt.Sprintf("Delete agent connection %s? Invocations through it stop working; delegation history is kept.", args[0])); err != nil {
				return err
			}
			if err = c.DeleteResource(cmd.Context(), aiplex.AgentConnections, args[0], current.Revision); err != nil {
				return a2aErr("delete agent connection", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted agent connection %s.\n", args[0])
			return nil
		},
	}
	addYesFlag(cmd, &yes)
	return cmd
}

// chainSpanTime describes the time recorded across a chain. The API's own
// total adds only the durations it holds, and an unfinished span holds none,
// so a total of 0 can mean "nothing finished". The figure is rebuilt from the
// spans, and the spans it leaves out are counted.
func chainSpanTime(ch *aiplex.DelegationChain) string {
	spans := append([]aiplex.Delegation{ch.RootDelegation}, ch.Children...)
	var total int64
	known := 0
	for _, d := range spans {
		if d.CompletedAt != nil {
			known++
			total += d.DurationMs
		}
	}
	missing := len(spans) - known
	const note = "nested spans are summed, so it is not elapsed time"
	switch {
	case missing == 0:
		return fmt.Sprintf("recorded span time %dms (%s)", total, note)
	case known == 0:
		return fmt.Sprintf("recorded span time unknown (%d of %d spans unfinished or without a duration)", missing, len(spans))
	}
	return fmt.Sprintf("recorded span time %dms for %d of %d spans (%d unfinished or without a duration are not counted; %s)", total, known, len(spans), missing, note)
}
