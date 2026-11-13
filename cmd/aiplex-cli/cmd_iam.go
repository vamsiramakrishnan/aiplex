package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func iamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "iam",
		Short: "Roles and role bindings: who is an admin, deployer or viewer",
		Long: `A role binding maps an identity-provider group to an AIPlex role (admin,
deployer, viewer or agent) and optionally to the default scopes of that role. It
can be restricted to one issuer and pool. Role bindings decide who can change
configuration, so every command except 'roles' needs the admin role.

  aiplex iam roles
  aiplex iam role-bindings list
  aiplex iam role-bindings apply -f binding.yaml
  aiplex iam role-bindings delete rb-1 --yes

Changing or deleting a binding is checked against its revision, so a stale
edit is refused (exit code 5) rather than overwriting a newer one.`,
		Example: "  aiplex iam roles\n  aiplex iam role-bindings list",
	}
	rb := &cobra.Command{
		Use:     "role-bindings",
		Aliases: []string{"role-binding", "bindings"},
		Short:   "Group-to-role mappings (admin)",
		Example: "  aiplex iam role-bindings list\n  aiplex iam role-bindings apply -f binding.yaml",
	}
	rb.AddCommand(iamBindingsListCmd(), iamBindingsGetCmd(), iamBindingsApplyCmd(), iamBindingsDeleteCmd())
	cmd.AddCommand(iamRolesCmd(), rb, iamValidatePrincipalCmd())
	return cmd
}

func iamRolesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "roles",
		Short:   "List the roles and the scopes each grants by default",
		Example: "  aiplex iam roles\n  aiplex iam roles -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			roles, err := newClient().ListRoles(cmd.Context())
			if err != nil {
				return fmt.Errorf("list roles: %w", err)
			}
			if spStructured(cmd, roles) {
				return nil
			}
			t := newTable(keyCol("ROLE"), col("DEFAULT SCOPES"))
			for _, r := range roles {
				t.add(r.Role, spJoin(r.DefaultScopes))
			}
			t.render(cmd.OutOrStdout())
			return nil
		},
	}
}

func iamBindingsListCmd() *cobra.Command {
	var group string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List role bindings (admin)",
		Example: "  aiplex iam role-bindings list\n  aiplex iam role-bindings list --group platform-eng -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := newClient().ListRoleBindings(cmd.Context(), group)
			if err != nil {
				return fmt.Errorf("list role bindings: %w", err)
			}
			if spStructured(cmd, list) {
				return nil
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No role bindings. Create one with: aiplex iam role-bindings apply -f binding.yaml")
				return nil
			}
			t := newTable(keyCol("ID"), col("GROUP"), col("ROLE"), idCol("ISSUER"), col("POOL"), col("SCOPES"), col("REV"))
			for _, b := range list {
				t.add(b.ID, b.Group, b.Role, spDash(b.Provider), spDash(b.PoolID), strconv.Itoa(len(b.Scopes)), strconv.FormatInt(b.Revision, 10))
			}
			t.render(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().StringVar(&group, "group", "", "Only bindings for this group")
	return cmd
}

func iamBindingsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show", "describe"},
		Short:   "Show one role binding",
		Example: "  aiplex iam role-bindings get local-admin\n  aiplex iam role-bindings get local-admin -o yaml > binding.yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := newClient().GetRoleBinding(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get role binding: %w", err)
			}
			if spStructured(cmd, b) {
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Role binding %s (revision %d)\n", b.ID, b.Revision)
			fmt.Fprintf(out, "  Group:       %s\n  Role:        %s\n", b.Group, b.Role)
			if b.Provider != "" {
				fmt.Fprintf(out, "  Issuer:      %s\n", b.Provider)
			}
			if b.PoolID != "" {
				fmt.Fprintf(out, "  Pool:        %s\n", b.PoolID)
			}
			if b.Description != "" {
				fmt.Fprintf(out, "  Description: %s\n", b.Description)
			}
			if len(b.Scopes) == 0 {
				fmt.Fprintln(out, "  Scopes:      none")
			}
			for i, s := range b.Scopes {
				label := "  Scopes:      "
				if i > 0 {
					label = "               "
				}
				fmt.Fprintf(out, "%s%s\n", label, s)
			}
			if b.CreatedBy != "" {
				fmt.Fprintf(out, "  Created by:  %s at %s\n", b.CreatedBy, b.CreatedAt.Format("2006-01-02 15:04:05Z07:00"))
			}
			return nil
		},
	}
}

// iamBindingFile is the file `apply` reads: the binding's fields as the API
// names them. Unknown fields are refused, as the server refuses them.
// created_at and created_by are server-owned and ignored by the server, so a
// file written by 'get -o yaml' still applies. revision, when present, is the
// revision the file was written from: apply refuses if it is not current.
type iamBindingFile struct {
	ID          string          `json:"id"`
	Group       string          `json:"group"`
	Role        string          `json:"role"`
	Provider    string          `json:"provider"`
	PoolID      string          `json:"pool_id"`
	Scopes      json.RawMessage `json:"scopes"` // missing and null stay distinguishable from []
	Description string          `json:"description"`
	Revision    *int64          `json:"revision"`
	CreatedAt   json.RawMessage `json:"created_at"`
	CreatedBy   json.RawMessage `json:"created_by"`
}

func iamDecodeBinding(raw []byte) (iamBindingFile, error) {
	var f iamBindingFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, usageError("invalid role binding file: " + err.Error())
	}
	if f.Revision != nil && *f.Revision < 0 {
		return f, usageError("invalid role binding file: revision cannot be negative")
	}
	return f, nil
}

// scopesStated reports whether the file gives scopes as a list (possibly
// empty), as opposed to omitting the key or writing null.
func (f iamBindingFile) scopesStated() bool {
	return len(f.Scopes) > 0 && string(bytes.TrimSpace(f.Scopes)) != "null"
}

func iamBindingsApplyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Create or replace a role binding from a JSON or YAML file (admin)",
		Long: `The file holds the binding:

  id: platform-admins          # optional; derived from the binding when omitted
  group: platform-eng          # the identity-provider group (required)
  role: admin                  # admin, deployer, viewer or agent (required)
  provider: https://idp.example.com   # optional: only this issuer
  pool_id: workforce           # optional: only this pool
  description: Platform engineers
  scopes: [mcp:tools:*]        # the scopes granted; see below

When the binding is created, omitting scopes (or writing null) gives it the
role's default scopes ('aiplex iam roles' lists them) and 'scopes: []' grants
none. With an id that exists, the binding is replaced at its current revision:
group, role, provider, pool_id and description are set to the file's values
(leave one out to clear it) and scopes must be stated, as a list or as [] for
none; a file that omits them is refused (exit 2) rather than keeping scopes the
file does not show. A file written by 'get -o yaml' applies as it is; its
revision is checked, and the apply is refused (exit 5) when the binding has
changed since. Without an id the binding is created, and applying the same file
again is refused as already existing.
Use '-f -' to read stdin.`,
		Example: "  aiplex iam role-bindings apply -f binding.yaml\n  aiplex iam role-bindings get platform-admins -o yaml | aiplex iam role-bindings apply -f -",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := spReadSpec(file, cmd.InOrStdin())
			if err != nil {
				return err
			}
			f, err := iamDecodeBinding(raw)
			if err != nil {
				return err
			}
			in := aiplex.RoleBindingInput{ID: f.ID, Group: f.Group, Role: f.Role, Provider: f.Provider, PoolID: f.PoolID, Description: f.Description}
			if f.scopesStated() {
				var scopes []string
				if err = json.Unmarshal(f.Scopes, &scopes); err != nil {
					return usageError("invalid role binding file: scopes must be a list of scope strings")
				}
				in.Scopes = append([]string{}, scopes...)
			}
			if err = in.Validate(); err != nil {
				return usageError(strings.TrimPrefix(err.Error(), "aiplex: "))
			}
			c := newClient()
			var saved *aiplex.RoleBinding
			verb := "Created"
			if f.ID != "" {
				existing, getErr := c.GetRoleBinding(cmd.Context(), f.ID)
				switch {
				case getErr == nil:
					if f.Revision != nil && *f.Revision != existing.Revision {
						return newCLIError(exitConflict, fmt.Sprintf("role binding %s is at revision %d but the file was written from revision %d", f.ID, existing.Revision, *f.Revision),
							"Nothing was changed.", "Run 'aiplex iam role-bindings get "+f.ID+" -o yaml', merge your edit and apply again")
					}
					if !f.scopesStated() {
						return usageError("replacing role binding "+f.ID+" needs its scopes stated; omitting them (or writing null) would silently keep the stored ones",
							"Add 'scopes: []' to grant none, or list the scopes", "'aiplex iam role-bindings get "+f.ID+" -o yaml' shows the current ones")
					}
					saved, err = c.UpdateRoleBinding(cmd.Context(), f.ID, in, existing.Revision)
					verb = "Updated"
				case spIsNotFound(getErr):
					if f.Revision != nil && *f.Revision != 0 {
						return newCLIError(exitConflict, fmt.Sprintf("the file names revision %d of role binding %s, which does not exist", *f.Revision, f.ID),
							"Nothing was changed.", "Remove the revision line to create it")
					}
					saved, err = c.CreateRoleBinding(cmd.Context(), in)
				default:
					return fmt.Errorf("read role binding: %w", getErr)
				}
			} else {
				if f.Revision != nil && *f.Revision != 0 {
					return usageError("the file has a revision but no id", "Add the binding's id to replace it, or remove the revision to create a new one")
				}
				saved, err = c.CreateRoleBinding(cmd.Context(), in)
			}
			if err != nil {
				return spExplain("apply role binding", err)
			}
			if spStructured(cmd, saved) {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s role binding %s (revision %d)\n  Group: %s\n  Role:  %s\n", verb, saved.ID, saved.Revision, saved.Group, saved.Role)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON or YAML file, or - for stdin")
	return cmd
}

func iamBindingsDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete a role binding (admin)",
		Long:    "People who held a role only through this binding lose it on their next request. Deleting the binding that makes you an administrator can lock you out; the bootstrap role bindings file is the way back in.",
		Example: "  aiplex iam role-bindings delete platform-admins --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			cur, err := c.GetRoleBinding(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("read role binding: %w", err)
			}
			if err = confirm(cmd, yes, "Delete role binding "+args[0]+"? Members of group "+cur.Group+" lose the "+cur.Role+" role it grants."); err != nil {
				return err
			}
			if err = c.DeleteRoleBinding(cmd.Context(), args[0], cur.Revision); err != nil {
				return spExplain("delete role binding", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted role binding %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	return cmd
}

func iamValidatePrincipalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate-principal <principal>",
		Short: "Check a workload or workforce identity principal string (admin)",
		Long: `Checks the syntax of a Google Cloud principal:// or principalset:// URI without
creating anything. Exits 1 when the principal is not valid.`,
		Example: "  aiplex iam validate-principal 'principalSet://iam.googleapis.com/locations/global/workforcePools/acme/*'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := newClient().ValidatePrincipal(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("validate principal: %w", err)
			}
			printed := spStructured(cmd, v)
			if !v.Valid {
				return newCLIError(exitError, "principal is not valid: "+strings.TrimSpace(v.Error))
			}
			if printed {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Valid: %s\n", v.Principal)
			return nil
		},
	}
}
