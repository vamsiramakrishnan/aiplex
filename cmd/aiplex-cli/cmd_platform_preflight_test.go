package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/gcpsetup"
)

// preflightFake grants everything except the listed permissions.
type preflightFake struct{ deny map[string]bool }

func (f preflightFake) TestPermissions(_ context.Context, _ gcpsetup.ResourceKind, _ string, perms []string) ([]string, error) {
	var out []string
	for _, p := range perms {
		if !f.deny[p] {
			out = append(out, p)
		}
	}
	return out, nil
}
func (preflightFake) EnabledServices(context.Context, string) (map[string]bool, error) {
	s := map[string]bool{}
	for _, a := range gcpsetup.AllAPIs() {
		s[a] = true
	}
	return s, nil
}
func (preflightFake) BillingEnabled(context.Context, string) (bool, error) { return true, nil }
func (preflightFake) BucketExists(context.Context, string) (bool, error)   { return false, nil }

func usePreflightFake(t *testing.T, deny ...string) {
	t.Helper()
	old := preflightGoogle
	d := map[string]bool{}
	for _, p := range deny {
		d[p] = true
	}
	preflightGoogle = func(context.Context) (gcpsetup.Google, func(context.Context) string, error) {
		return preflightFake{deny: d}, func(context.Context) string { return "deployer@example.com" }, nil
	}
	t.Cleanup(func() { preflightGoogle = old })
}

func runPreflightCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := platformPreflightCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(append([]string{"--tfvars", filepath.Join(t.TempDir(), "absent.tfvars")}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func TestPlatformPreflightPassesAndFails(t *testing.T) {
	old := output
	output = "table"
	t.Cleanup(func() { output = old })

	usePreflightFake(t)
	out, err := runPreflightCmd(t, "--project", "p")
	if err != nil {
		t.Fatalf("expected pass, got %v\n%s", err, out)
	}
	for _, want := range []string{"Preflight passed.", "advisory", "workforce-identity", "skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	usePreflightFake(t, "container.clusters.create")
	out, err = runPreflightCmd(t, "--project", "p")
	var ce *cliError
	if !errors.As(err, &ce) || ce.code != exitAuth {
		t.Fatalf("missing permission must exit %d, got %v", exitAuth, err)
	}
	want := "gcloud projects add-iam-policy-binding p --member=user:deployer@example.com --role=roles/container.clusterAdmin --condition=None"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks the grant command %q:\n%s", want, out)
	}
	if strings.Contains(out, "roles/owner") || strings.Contains(out, "roles/editor") {
		t.Error("preflight must not suggest basic roles")
	}
}

func TestPlatformPreflightJSON(t *testing.T) {
	old := output
	output = "json"
	t.Cleanup(func() { output = old })
	usePreflightFake(t, "dns.managedZones.create")
	var err error
	stdout, _ := captureStdout(t, func() error { _, err = runPreflightCmd(t, "--project", "p"); return nil })
	if err == nil {
		t.Fatal("expected failure")
	}
	var doc struct {
		OK       bool     `json:"ok"`
		Account  string   `json:"account"`
		Grant    []string `json:"grant_commands"`
		Advisory string   `json:"advisory"`
		Features []struct {
			Feature  string `json:"feature"`
			State    string `json:"state"`
			Problems []struct {
				Name string `json:"name"`
				Role string `json:"narrowest_role"`
			} `json:"problems"`
		} `json:"features"`
	}
	if e := json.Unmarshal([]byte(stdout), &doc); e != nil {
		t.Fatalf("%v\n%s", e, stdout)
	}
	if doc.OK || doc.Account != "deployer@example.com" || len(doc.Grant) != 1 || doc.Advisory == "" {
		t.Fatalf("%+v", doc)
	}
	found := false
	for _, f := range doc.Features {
		if f.Feature == "dns" && f.State == "missing" && len(f.Problems) == 1 && f.Problems[0].Role == "roles/dns.editor" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dns problem not reported: %s", stdout)
	}
}

func TestPlatformPreflightPrintRoleMatchesCheckedInFile(t *testing.T) {
	out, err := runPreflightCmd(t, "--print-role")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../deploy/iam/aiplex-deployer-role.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(b) {
		t.Fatal("--print-role differs from deploy/iam/aiplex-deployer-role.yaml")
	}
	out, err = runPreflightCmd(t, "--print-bindings", "--project", "p")
	if err != nil || !strings.Contains(out, "gcloud projects add-iam-policy-binding p") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestApplyPreflightRefusesUnlessSkipped(t *testing.T) {
	usePreflightFake(t, "resourcemanager.projects.setIamPolicy")
	var buf bytes.Buffer
	err := requirePreflight(context.Background(), &buf, "p", false)
	var ce *cliError
	if !errors.As(err, &ce) {
		t.Fatalf("apply must refuse, got %v", err)
	}
	if !strings.Contains(strings.Join(ce.hints, "\n"), "--skip-preflight") {
		t.Errorf("refusal must name --skip-preflight: %v", ce.hints)
	}
	buf.Reset()
	if err := requirePreflight(context.Background(), &buf, "p", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "[WARN] --skip-preflight") {
		t.Errorf("skip must warn: %s", buf.String())
	}
}
