package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
	"io"
	"os"
)

func clientsCmd() *cobra.Command {
	command := &cobra.Command{Use: "clients", Short: "Register developer clients and review connection plans"}
	command.AddCommand(&cobra.Command{Use: "profiles", Short: "List supported client profiles", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, e := newClient().ListClientProfiles(cmd.Context())
		if e != nil {
			return e
		}
		printJSON(result)
		return nil
	}}, &cobra.Command{Use: "list", Short: "List registered clients", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, e := newClient().ListClientRegistrations(cmd.Context())
		if e != nil {
			return e
		}
		printJSON(result)
		return nil
	}}, &cobra.Command{Use: "get <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		result, e := newClient().GetClientRegistration(cmd.Context(), args[0])
		if e != nil {
			return e
		}
		printJSON(result)
		return nil
	}}, &cobra.Command{Use: "plan <id>", Short: "Render reviewed configuration fragments without applying them", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		result, e := newClient().PlanClientConnection(cmd.Context(), args[0])
		if e != nil {
			return e
		}
		printJSON(result)
		return nil
	}})
	var filename string
	var expected uint64
	put := &cobra.Command{Use: "register", Short: "Create or update a client registration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if filename == "" {
			return fmt.Errorf("--file required")
		}
		data, e := os.ReadFile(filename)
		if e != nil {
			return e
		}
		var registration aiplex.ClientRegistration
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&registration); e != nil {
			return e
		}
		if e = decoder.Decode(new(any)); e != io.EOF {
			return fmt.Errorf("registration file must contain one JSON object")
		}
		result, e := newClient().PutClientRegistration(cmd.Context(), registration, expected)
		if e != nil {
			return e
		}
		printJSON(result)
		return nil
	}}
	put.Flags().StringVarP(&filename, "file", "f", "", "Registration JSON file")
	put.Flags().Uint64Var(&expected, "expected-revision", 0, "Expected revision; zero creates a new registration")
	command.AddCommand(put)
	var deleteRevision uint64
	remove := &cobra.Command{Use: "delete <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return newClient().DeleteClientRegistration(cmd.Context(), args[0], deleteRevision)
	}}
	remove.Flags().Uint64Var(&deleteRevision, "expected-revision", 0, "Required current revision")
	command.AddCommand(remove)
	return command
}
