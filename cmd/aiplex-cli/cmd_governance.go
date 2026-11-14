package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func nativeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "native", Short: "Inspect and review configured native cloud governance"}
	cmd.AddCommand(nativeGovernanceCmd())
	return cmd
}

func nativeGovernanceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "governance", Short: "Read native resources and apply separately reviewed plans"}
	show := func(v any) {
		if output == "yaml" {
			printYAML(v)
		} else {
			printJSON(v)
		}
	}
	cmd.AddCommand(&cobra.Command{Use: "providers", Args: cobra.NoArgs, Short: "List configured native providers and their capabilities", RunE: func(cmd *cobra.Command, _ []string) error {
		out, e := newClient().ListNativeProviders(cmd.Context())
		if e == nil {
			show(out)
		}
		return e
	}})
	var bindingRevision uint64
	read := &cobra.Command{Use: "read <binding-id> <operation-json-file>", Args: cobra.ExactArgs(2), Short: "Read through a configured binding with an explicit revision", RunE: func(cmd *cobra.Command, args []string) error {
		operation, e := readNativeOperation(args[1])
		if e != nil {
			return e
		}
		out, e := newClient().ReadNativeBinding(cmd.Context(), args[0], aiplex.NativeReadRequest{Operation: operation, BindingRevision: bindingRevision})
		if e == nil {
			show(out)
		}
		return e
	}}
	read.Flags().Uint64Var(&bindingRevision, "binding-revision", 0, "Required current binding revision")
	cmd.AddCommand(read)
	var desiredID string
	var planBindingRevision uint64
	var ttl time.Duration
	plan := &cobra.Command{Use: "plan <binding-id>", Args: cobra.ExactArgs(1), Short: "Review a saved desired action without applying it", RunE: func(cmd *cobra.Command, args []string) error {
		if ttl < time.Second || ttl%time.Second != 0 {
			return fmt.Errorf("TTL must be a positive whole number of seconds")
		}
		out, e := newClient().PlanNativeBinding(cmd.Context(), args[0], aiplex.NativePlanRequest{DesiredID: desiredID, BindingRevision: planBindingRevision, TTLSeconds: int64(ttl / time.Second)})
		if e == nil {
			show(out)
		}
		return e
	}}
	plan.Flags().StringVar(&desiredID, "desired-id", "", "Required desired action ID from the saved binding")
	plan.Flags().Uint64Var(&planBindingRevision, "binding-revision", 0, "Required current binding revision")
	plan.Flags().DurationVar(&ttl, "ttl", 5*time.Minute, "Plan validity duration")
	cmd.AddCommand(plan)
	var revision uint64
	var digest string
	apply := &cobra.Command{Use: "apply <plan-id>", Args: cobra.ExactArgs(1), Short: "Apply a reviewed plan using its exact revision and digest", RunE: func(cmd *cobra.Command, args []string) error {
		out, e := newClient().ApplyNativePlan(cmd.Context(), args[0], aiplex.NativeApplyRequest{PlanRevision: revision, ApprovalDigest: digest})
		if e == nil {
			show(out)
		}
		return e
	}}
	apply.Flags().Uint64Var(&revision, "plan-revision", 0, "Required saved plan revision")
	apply.Flags().StringVar(&digest, "approval-digest", "", "Required digest of the reviewed plan")
	cmd.AddCommand(apply, &cobra.Command{Use: "get-plan <plan-id>", Short: "Show a saved governance plan", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		out, e := newClient().GetNativePlan(cmd.Context(), args[0])
		if e == nil {
			show(out)
		}
		return e
	}}, &cobra.Command{Use: "receipt <plan-id>", Short: "Show the receipt of an applied governance plan", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		out, e := newClient().GetNativeReceipt(cmd.Context(), args[0])
		if e == nil {
			show(out)
		}
		return e
	}})
	return cmd
}

func readNativeOperation(filename string) (nativeapi.Operation, error) {
	f, e := os.Open(filename)
	if e != nil {
		return nativeapi.Operation{}, fmt.Errorf("operation file could not be opened")
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, nativeapi.MaxBodyBytes+1))
	if e != nil || len(data) > nativeapi.MaxBodyBytes {
		return nativeapi.Operation{}, fmt.Errorf("operation file exceeds limit or could not be read")
	}
	if e = nativeapi.ValidatePublicPayload(data); e != nil {
		return nativeapi.Operation{}, e
	}
	var operation nativeapi.Operation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&operation) != nil {
		return nativeapi.Operation{}, fmt.Errorf("operation file must contain a valid operation")
	}
	return operation, nil
}
