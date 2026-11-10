package main

import (
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// errorHints returns follow-up lines for common first-run failures: an API
// that is not running, a missing or rejected token, and state that vanished
// because a local API keeps it in memory.
func errorHints(err error) []string {
	if err == nil {
		return nil
	}
	target := resolveAPIURL()
	local := isLoopbackURL(target)
	var opErr *net.OpError
	if errors.Is(err, syscall.ECONNREFUSED) || (errors.As(err, &opErr) && opErr.Op == "dial") || strings.Contains(err.Error(), "connection refused") {
		if local {
			return []string{"Nothing is answering at " + target + ". Start a local AIPlex with: aiplex dev", "(or point the CLI elsewhere with --url, AIPLEX_URL or: aiplex config use-context <name>)"}
		}
		return []string{"Cannot reach " + target + ". Check the URL (aiplex config show) and that the API is running (aiplex health)."}
	}
	var apiErr *aiplex.Error
	if !errors.As(err, &apiErr) {
		return nil
	}
	switch apiErr.StatusCode {
	case 401:
		if local {
			return []string{"The API did not accept your token. For a local API started with `aiplex dev`, run: eval \"$(aiplex dev env)\"", "(a token from an earlier `aiplex dev` run is not valid after it restarts)"}
		}
		return []string{"Sign in again with: aiplex login"}
	case 403:
		return []string{"Your identity lacks the role or grant for this action. Check it with: aiplex whoami --server"}
	case 404:
		if local && (apiErr.Code == "NOT_FOUND" || apiErr.Code == "ACCESS_POINT_NOT_FOUND") {
			return []string{"A local API keeps state in memory: everything is discarded when it stops or restarts.", "Restarting `aiplex dev` recreates the seeded resources (access point \"local\")."}
		}
	}
	return nil
}
