package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
	"io"
	"os"
	"strings"
)

func clientsCmd() *cobra.Command {
	command := &cobra.Command{Use: "clients", Short: "Register developer clients and review connection plans"}
	command.AddCommand(&cobra.Command{Use: "profiles", Short: "List supported client profiles", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, e := newClient().ListClientProfiles(cmd.Context())
		if e != nil {
			return fmt.Errorf("list client profiles: %w", e)
		}
		if machineOutput() {
			spStructured(cmd, result)
			return nil
		}
		t := newTable(keyCol("PROFILE"), col("NAME"), col("MODES"), col("PROTOCOLS"), wideCol("SURFACE"), wideCol("SOURCES"))
		for _, p := range result {
			t.add(p.ID, p.Name, strings.Join(p.Modes, ","), strings.Join(p.Protocols, ","), p.Surface, strings.Join(p.Sources, " "))
		}
		t.render(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "\nConfiguration for one of them: aiplex connect <access-point> --client <profile>")
		return nil
	}}, &cobra.Command{Use: "list", Short: "List registered clients", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, e := newClient().ListClientRegistrations(cmd.Context())
		if e != nil {
			return fmt.Errorf("list client registrations: %w", e)
		}
		if machineOutput() {
			if result == nil {
				result = []aiplex.ClientRegistrationRecord{}
			}
			spStructured(cmd, result)
			return nil
		}
		if len(result) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No client registrations. Register one with: aiplex clients register --file <registration.json>")
			return nil
		}
		t := newTable(keyCol("ID"), col("PROFILE"), col("MODE"), col("PROTOCOL"), wideCol("REVISION"))
		for _, r := range result {
			var reg aiplex.ClientRegistration
			_ = json.Unmarshal(r.Spec, &reg)
			t.add(r.ID, reg.ProfileID, reg.Mode, reg.Protocol, fmt.Sprint(r.Revision))
		}
		t.render(cmd.OutOrStdout())
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
