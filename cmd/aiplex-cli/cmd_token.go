package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// tokenCmd mints a fresh local-mode token using the same Ed25519 key that
// `aiplex up` uses. Useful for granting a narrower-than-wildcard set of
// caps to an external script or testing how revocation feels.
//
//	aiplex token --sub alice@local --caps cap://tool/search@v1:call,cap://memory/notes@v1
func tokenCmd() *cobra.Command {
	var (
		subject string
		capList []string
		spiffe  string
		ttl     time.Duration
		dataDir string
	)
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Mint a local-mode JWT carrying a structured caps claim",
		Long: `Mints a JWT signed by the same Ed25519 key 'aiplex up' uses.
The token carries the structured 'caps' claim — same shape OPA reads in
production. Use it to narrow what an external script or SDK call can do
relative to the wildcard token 'aiplex up' provisions by default.

Cap entries use the form  cap://kind/name@version[:action1,action2].

Example:
  aiplex token --sub alice@local \
    --caps cap://tool/search_curriculum@v1:call \
    --caps cap://memory/students/alice/profile@v1:read,write \
    --ttl 1h`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, _ := os.UserHomeDir()
				dataDir = filepath.Join(home, ".aiplex")
			}
			signer, err := auth.NewLocalSigner(filepath.Join(dataDir, "local.key"), "aiplex://local")
			if err != nil {
				return err
			}
			sdkCaps := parseCapsFlag(capList)
			caps := make(capability.CapSet, 0, len(sdkCaps))
			for _, c := range sdkCaps {
				caps = append(caps, capability.Cap{URI: c.URI, Actions: c.Actions})
			}
			tok, err := signer.Mint(subject, caps, spiffe, ttl)
			if err != nil {
				return err
			}
			fmt.Println(tok)
			return nil
		},
	}
	cmd.Flags().StringVar(&subject, "sub", "local@local", "Subject (the human delegating)")
	cmd.Flags().StringSliceVar(&capList, "caps", nil, "Capabilities to grant (cap://kind/name@v[:actions])")
	cmd.Flags().StringVar(&spiffe, "act", "", "SPIFFE ID of the agent acting on behalf of subject (RFC 8693 act claim)")
	cmd.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, "Token lifetime")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Where to find local.key (default ~/.aiplex)")

	cmd.MarkFlagRequired("caps")
	return cmd
}

var _ = strings.TrimSpace // keep import alive (parseCapsFlag is in cmd_agents.go)
