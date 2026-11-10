// Command consolee2e starts the same local stack as `aiplex dev` (in-memory
// API, mock model, mock MCP server, seeded access point) and additionally
// mints a no-role caller and a deployer, so the Console can be driven in a
// browser as each kind of caller (see console/README.md, "Browser checks").
//
// It prints one JSON object on stdout when ready and runs until interrupted.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

func main() {
	port := flag.Int("port", 18080, "API port on 127.0.0.1")
	apiBin := flag.String("api-bin", "bin/aiplex-api", "aiplex-api binary")
	origin := flag.String("console-origin", "http://127.0.0.1:19090", "browser origin allowed by CORS")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stack, err := devstack.Start(ctx, devstack.Options{APIBinary: *apiBin, Port: *port, ConsoleOrigins: []string{*origin}, Log: os.Stderr, Env: []string{"AIPLEX_AGENT_FEDERATION=enabled", "AIPLEX_USER_FEDERATION=enabled"}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer stack.Stop()
	if _, err := stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	extra, err := seedMore(ctx, stack)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed more:", err)
		os.Exit(1)
	}
	binding, _ := json.Marshal(map[string]any{"id": "deployers", "group": "deployers", "role": "deployer", "description": "Console e2e deployers"})
	req, _ := http.NewRequest("POST", stack.APIURL+"/api/v1/iam/role-bindings", bytes.NewReader(binding))
	req.Header.Set("Authorization", "Bearer "+stack.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		fmt.Fprintln(os.Stderr, "create deployer binding:", err, resp)
		os.Exit(1)
	}
	bob, _ := stack.Issuer.Mint(map[string]any{"sub": "bob", "email": "bob@localhost", "name": "Bob", "groups": []string{"eng"}}, 6*time.Hour)
	dana, _ := stack.Issuer.Mint(map[string]any{"sub": "dana", "email": "dana@localhost", "name": "Dana", "groups": []string{"deployers"}}, 6*time.Hour)
	out := map[string]any{
		"api_url": stack.APIURL, "admin_token": stack.Token, "norole_token": bob, "deployer_token": dana,
		"norole_principal":   auth.CanonicalPrincipalID(stack.IssuerURL, devstack.DevPoolID, "bob"),
		"deployer_principal": auth.CanonicalPrincipalID(stack.IssuerURL, devstack.DevPoolID, "dana"),
		"workload_token":     extra["workload_token"], "user_token": extra["user_token"],
		"models_url": stack.ModelsURL, "mcp_url": stack.MCPURL,
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
	<-ctx.Done()
}
