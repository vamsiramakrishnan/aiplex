package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func devTryCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "try",
		Short:   "Send one local model request and show its reply and usage record",
		Long:    "Send Hello through the access point of a running mock development stack. Read its token from the saved local context and look up the call in the usage ledger by request ID.",
		Example: "  aiplex dev --detach\n  aiplex dev try\n  aiplex dev stop",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := liveDevState()
			if err != nil {
				return err
			}
			if st == nil {
				return newCLIError(exitError, "no local AIPlex is running", "Start it with: aiplex dev --detach")
			}
			if st.Provider != "mock" || !isLoopbackURL(st.URL) {
				return usageError("dev try needs a local stack started with the mock provider", "For a real provider, see the client configuration: aiplex connect "+st.AccessPoint)
			}
			bearer, err := devStoredToken(st.Context)
			if err != nil {
				return err
			}
			client := aiplex.NewClient(st.URL)
			client.SetToken(bearer)
			model, _ := json.Marshal(st.Model)
			body := `{"model":` + string(model) + `,"messages":[{"role":"user","content":"Hello"}]}`
			endpoint := st.URL + "/ap/" + url.PathEscape(st.AccessPoint) + "/v1/chat/completions"
			req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, endpoint, strings.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+bearer)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.HTTPClient.Do(req)
			if err != nil {
				return fmt.Errorf("send local request: %w", err)
			}
			defer resp.Body.Close()
			requestID := resp.Header.Get("X-Aiplex-Request-Id")
			if err := aiplex.RefusedRedirect(req.URL, resp); err != nil {
				return err
			}
			if resp.StatusCode != http.StatusOK {
				return &aiplex.Error{StatusCode: resp.StatusCode, Code: resp.Header.Get(errorcode.Header), Message: "local model request failed", RequestID: requestID}
			}
			var reply struct {
				Choices []struct {
					Message struct{ Content string }
				}
			}
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply); err != nil || len(reply.Choices) == 0 {
				return fmt.Errorf("local model returned an invalid chat completion")
			}
			if requestID == "" {
				return fmt.Errorf("local model replied without an AIPlex request ID; cannot find its usage record")
			}

			record, err := devTryRecord(cmd.Context(), client, requestID)
			if err != nil {
				return newCLIError(exitError, fmt.Sprintf("look up the local model request: %v", err), "Look up the existing call: aiplex usage records --request-id "+requestID)
			}
			message := reply.Choices[0].Message.Content
			if !spStructured(cmd, map[string]any{"reply": message, "record": record}) {
				out := cmd.OutOrStdout()
				fmt.Fprintln(out, message)
				fmt.Fprintln(out)
				usageRecordTable([]usageledger.Record{record}).render(out)
				fmt.Fprintln(out, "\nInspect this call: aiplex usage get "+record.ID)
			}
			return nil
		},
	}
}

// Ledger writes complete asynchronously. Poll only the lookup; never replay the call.
func devTryRecord(parent context.Context, client *aiplex.Client, requestID string) (usageledger.Record, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	query := aiplex.UsageRecordsQuery{UsageFilter: aiplex.UsageFilter{RequestID: requestID, Source: usageledger.SourceManaged, Plane: usageledger.PlaneLLM}, Limit: 1}
	for {
		page := client.PageUsageRecords(query)
		if page.Next(ctx) {
			return page.Record(), nil
		}
		if err := page.Err(); err != nil {
			return usageledger.Record{}, err
		}
		select {
		case <-ctx.Done():
			return usageledger.Record{}, ctx.Err()
		case <-tick.C:
		}
	}
}
