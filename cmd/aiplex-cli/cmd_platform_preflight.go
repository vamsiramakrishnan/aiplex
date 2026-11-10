package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/internal/gcpsetup"
)

// preflightGoogle builds the Google client the preflight uses and a function
// that names the identity behind it. Tests replace it with a fake.
var preflightGoogle = func(ctx context.Context) (gcpsetup.Google, func(context.Context) string, error) {
	r, err := gcpsetup.NewREST(ctx)
	if err != nil {
		return nil, nil, err
	}
	return r, r.Account, nil
}

// preflightInput is what one preflight run checks.
type preflightInput struct {
	project      string
	organization string
	toggles      gcpsetup.Toggles
}

// runGCPPreflight checks the deployer's permissions, APIs and billing for the
// features the toggles switch on. account is the identity the credentials
// belong to ("" when unknown).
func runGCPPreflight(ctx context.Context, in preflightInput) (*gcpsetup.Report, string, error) {
	g, accountOf, err := preflightGoogle(ctx)
	if err != nil {
		return nil, "", err
	}
	org := in.organization
	if org == "" {
		org = in.toggles.OrganizationID
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	report, err := gcpsetup.Run(ctx, g, gcpsetup.Config{
		Project:      in.project,
		Organization: org,
		Enabled:      gcpsetup.EnabledFeatures(in.toggles),
	})
	if err != nil {
		return nil, "", err
	}
	account := accountOf(ctx)
	if account == "" {
		account = detectGCPAccount()
	}
	return report, account, nil
}

func platformPreflightCmd() *cobra.Command {
	var (
		project       string
		organization  string
		tfvarsPath    string
		printRole     bool
		printBindings bool
	)
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Check the Google Cloud permissions, APIs and billing the platform deploy needs",
		Long: `Checks, before anything is created, that the account in Application Default
Credentials (the credentials Terraform uses) holds every IAM permission the
AIPlex Terraform needs for the features your terraform.tfvars switches on,
that the APIs Terraform depends on are enabled, and that billing is enabled.

Permissions are tested with testIamPermissions on the project (and on the
organization for workforce identity pools, and on the Terraform state bucket
when it exists), in batches of at most 100. For each missing permission the
narrowest predefined role that grants it is shown, with the gcloud command
that grants it to you.

The check is advisory: organization policy, IAM deny policies and VPC Service
Controls can still block an apply this check passes. It has not been run
against a live project as part of AIPlex's own tests.

--print-role prints the optional custom deployer role (also checked in at
deploy/iam/aiplex-deployer-role.yaml); --print-bindings prints the predefined
roles per feature as gcloud commands. Neither calls Google.

Exit status is 3 when a required permission, API or billing is missing, and 1
when something could not be checked.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tfvarsPath == "" {
				tfvarsPath = filepath.Join(findTerraformDir(), "terraform.tfvars")
			}
			toggles, err := gcpsetup.ReadToggles(tfvarsPath)
			if err != nil {
				return usageError(fmt.Sprintf("cannot read %s: %v", tfvarsPath, err))
			}
			if organization != "" {
				toggles.OrganizationID = organization
			}
			if printRole {
				fmt.Fprint(cmd.OutOrStdout(), gcpsetup.DeployerRoleYAML())
				return nil
			}
			if project == "" {
				project = preflightProject()
			}
			if printBindings {
				p := project
				if p == "" {
					p = "PROJECT_ID"
				}
				fmt.Fprint(cmd.OutOrStdout(), gcpsetup.PredefinedBindings(p, toggles.OrganizationID,
					gcpsetup.Member(detectGCPAccount()), gcpsetup.EnabledFeatures(toggles)))
				return nil
			}
			if project == "" {
				return usageError("no project: pass --project or set one with aiplex init")
			}
			report, account, err := runGCPPreflight(cmd.Context(), preflightInput{project: project, toggles: toggles})
			if err != nil {
				return newCLIError(exitError, "preflight could not run: "+err.Error(),
					"Sign in for Terraform: gcloud auth application-default login")
			}
			if machineOutput() {
				out := struct {
					*gcpsetup.Report
					Account  string   `json:"account,omitempty"`
					Grant    []string `json:"grant_commands,omitempty"`
					Tfvars   string   `json:"tfvars"`
					TfvarsOK bool     `json:"tfvars_found"`
				}{report, account, gcpsetup.BindingCommands(report.MissingRoles(), gcpsetup.Member(account)), tfvarsPath, toggles.Found}
				if output == "yaml" {
					printYAML(out)
				} else {
					printJSON(out)
				}
			} else {
				if !toggles.Found {
					fmt.Fprintf(cmd.OutOrStdout(), "No %s; checking the default features.\n\n", tfvarsPath)
				}
				printPreflightReport(cmd.OutOrStdout(), report, account)
			}
			return preflightError(report)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "GCP project ID (default: the current context's project, then gcloud's)")
	cmd.Flags().StringVar(&organization, "organization", "", "Organization ID for workforce identity pools (default: organization_id in terraform.tfvars)")
	cmd.Flags().StringVar(&tfvarsPath, "tfvars", "", "terraform.tfvars that selects the features (default: deploy/terraform/terraform.tfvars)")
	cmd.Flags().BoolVar(&printRole, "print-role", false, "Print the custom deployer role definition (YAML for gcloud iam roles create) and exit")
	cmd.Flags().BoolVar(&printBindings, "print-bindings", false, "Print gcloud commands granting the predefined roles each enabled feature needs, and exit")
	return cmd
}

// preflightProject is the project from the current CLI context, then gcloud.
func preflightProject() string {
	if cfg, err := cliconfig.Load(); err == nil {
		if ctx, err := cfg.Current(); err == nil && ctx.Project != "" {
			return ctx.Project
		}
	}
	return detectGCPProject()
}

// preflightError turns a failed report into an exit status: 3 when something
// required is missing, 1 when something could not be checked.
func preflightError(r *gcpsetup.Report) error {
	if r.OK {
		return nil
	}
	code := exitAuth
	for _, f := range r.Features {
		if f.State == gcpsetup.StateUnknown {
			code = exitError
		}
	}
	return newCLIError(code, fmt.Sprintf("preflight failed: %s", strings.Join(r.Failures, "; ")),
		"Grant the roles listed above, or create the custom role: aiplex platform preflight --print-role",
		"Details per permission: aiplex platform preflight -o json")
}

func printPreflightReport(w io.Writer, r *gcpsetup.Report, account string) {
	fmt.Fprintf(w, "Project %s", r.Project)
	if account != "" {
		fmt.Fprintf(w, ", checked as %s", account)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)

	t := newTable(keyCol("FEATURE"), col("STATE"), col("CHECKED"), col("MISSING"), col("UNKNOWN"), col("UNVERIFIED"), col("TOGGLE").trunc(truncEnd))
	for _, f := range r.Features {
		missing, unknown := 0, 0
		for _, p := range f.Problems {
			switch p.State {
			case gcpsetup.StateMissing:
				missing++
			case gcpsetup.StateUnknown:
				unknown++
			}
		}
		state := string(f.State)
		checked := strconv.Itoa(f.Checked)
		if f.State == gcpsetup.StateSkipped {
			checked = "-"
		}
		t.add(string(f.ID), state, checked, strconv.Itoa(missing), strconv.Itoa(unknown), strconv.Itoa(f.Unverified), f.Toggle)
	}
	t.render(w)

	var problems [][]string
	for _, f := range r.Features {
		for _, p := range f.Problems {
			note := ""
			switch {
			case p.State == gcpsetup.StateUnknown:
				note = "cannot check: " + p.Reason
			case p.Unverified != "":
				note = "need unverified (warning only): " + p.Unverified
			}
			problems = append(problems, []string{p.Name, string(f.ID), string(p.State), p.Resource, p.Role, note})
		}
	}
	if len(problems) > 0 {
		fmt.Fprintln(w)
		pt := newTable(keyCol("PERMISSION"), col("FEATURE"), col("STATE"), idCol("ON"), col("NARROWEST ROLE"), col("NOTE").trunc(truncEnd))
		for _, row := range problems {
			pt.add(row...)
		}
		pt.render(w)
	}

	if cmds := gcpsetup.BindingCommands(r.MissingRoles(), gcpsetup.Member(account)); len(cmds) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Grant the missing roles (narrowest predefined role per permission):")
		for _, c := range cmds {
			fmt.Fprintln(w, "  "+c)
		}
		fmt.Fprintln(w, "  or create and grant the custom role: aiplex platform preflight --print-role")
	}

	var apiNotes []string
	var disabled []string
	for _, a := range r.APIs {
		switch a.State {
		case gcpsetup.StateDisabled:
			if a.Bootstrap {
				apiNotes = append(apiNotes, fmt.Sprintf("  [FAIL] %s disabled (must be enabled before Terraform runs)", a.Name))
			} else {
				apiNotes = append(apiNotes, fmt.Sprintf("  [info] %s disabled (Terraform enables it)", a.Name))
			}
			disabled = append(disabled, a.Name)
		case gcpsetup.StateUnknown:
			apiNotes = append(apiNotes, fmt.Sprintf("  [WARN] %s: cannot check", a.Name))
		}
	}
	fmt.Fprintln(w)
	if len(apiNotes) == 0 {
		fmt.Fprintf(w, "APIs: all %d enabled\n", len(r.APIs))
	} else {
		fmt.Fprintln(w, "APIs:")
		for _, n := range apiNotes {
			fmt.Fprintln(w, n)
		}
		if len(disabled) > 0 {
			fmt.Fprintf(w, "  enable now: gcloud services enable %s --project %s\n", strings.Join(disabled, " "), r.Project)
		}
	}
	switch r.Billing {
	case gcpsetup.StateOK:
		fmt.Fprintln(w, "Billing: enabled")
	case gcpsetup.StateMissing:
		fmt.Fprintln(w, "Billing: [FAIL] not enabled")
	default:
		fmt.Fprintf(w, "Billing: [WARN] cannot check (%s)\n", r.BillingReason)
	}
	for _, warn := range r.Warnings {
		fmt.Fprintln(w, "[WARN] "+warn)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Note: "+r.Advisory)
	if r.OK {
		fmt.Fprintln(w, "Preflight passed.")
	}
}

// requirePreflight runs the preflight before Terraform and refuses on failure
// unless skip is set.
func requirePreflight(ctx context.Context, w io.Writer, project string, skip bool) error {
	if skip {
		fmt.Fprintln(w, "  [WARN] --skip-preflight: not checking permissions, APIs or billing before Terraform")
		return nil
	}
	toggles, err := gcpsetup.ReadToggles(filepath.Join(findTerraformDir(), "terraform.tfvars"))
	if err != nil {
		return fmt.Errorf("read terraform.tfvars: %w", err)
	}
	report, account, err := runGCPPreflight(ctx, preflightInput{project: project, toggles: toggles})
	if err != nil {
		return newCLIError(exitError, "preflight could not run: "+err.Error(),
			"Sign in for Terraform: gcloud auth application-default login",
			"To deploy without the check: aiplex platform apply --skip-preflight")
	}
	if report.OK {
		fmt.Fprintf(w, "  [pass] Permissions, APIs and billing (%d features checked)\n", countChecked(report))
		for _, warn := range report.Warnings {
			fmt.Fprintln(w, "  [WARN] "+warn)
		}
		return nil
	}
	printPreflightReport(w, report, account)
	err = preflightError(report)
	if ce, ok := err.(*cliError); ok {
		ce.hints = append(ce.hints, "To deploy anyway: aiplex platform apply --skip-preflight")
	}
	return err
}

func countChecked(r *gcpsetup.Report) int {
	n := 0
	for _, f := range r.Features {
		if f.State != gcpsetup.StateSkipped {
			n++
		}
	}
	return n
}
