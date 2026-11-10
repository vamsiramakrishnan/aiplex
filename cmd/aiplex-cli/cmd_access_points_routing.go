package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func spAPRoutingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "routing <id>",
		Short: "Show the live routing state of an access point's models (operators)",
		Long: `Shows each routed model of the access point: its strategy and, per backend,
the circuit breaker (closed, open or half_open), consecutive failures and the
last outcomes, plus the response-cache occupancy.

The state belongs to the API process that answered: breakers and the cache are
kept in memory per process, so another replica may show different state, and a
restart starts every breaker closed and the cache empty.`,
		Example: "  aiplex access-points routing ha\n  aiplex access-points routing ha -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			view, err := newClient().GetAccessPointRouting(cmd.Context(), args[0])
			if err != nil {
				return spExplain("read routing state", err)
			}
			if spStructured(cmd, view) {
				return nil
			}
			out := cmd.OutOrStdout()
			if len(view.Models) == 0 {
				fmt.Fprintf(out, "Access point %s has no routed models (no backends, routing or cache).\n", view.AccessPointID)
				return nil
			}
			t := newTable(keyCol("MODEL"), col("STRATEGY"), col("BACKEND"), col("BREAKER"), col("FAILURES"), wideCol("HALF-OPEN AT"), col("RECENT").trunc(truncMiddle))
			for _, m := range view.Models {
				for _, b := range m.Backends {
					recent := make([]string, 0, len(b.Breaker.Recent))
					for _, r := range b.Breaker.Recent {
						cell := r.Outcome
						if r.UpstreamStatus != 0 {
							cell += "(" + strconv.Itoa(r.UpstreamStatus) + ")"
						}
						recent = append(recent, cell)
					}
					retry := "—"
					if b.Breaker.RetryAt != nil {
						retry = b.Breaker.RetryAt.Local().Format(time.TimeOnly)
					}
					t.add(m.Name, m.Strategy, b.SurfaceRef+"/"+b.NativeModel, b.Breaker.State, strconv.Itoa(b.Breaker.ConsecutiveFailures), retry, spDash(strings.Join(recent, ",")))
				}
			}
			t.render(out)
			if view.CacheStore != nil {
				fmt.Fprintf(out, "\nResponse cache (this process): %d entries, %d of %d bytes\n", view.CacheStore.Entries, view.CacheStore.Bytes, view.CacheStore.MaxBytes)
			}
			fmt.Fprintln(out, "\nState is per API process and in memory: other replicas keep their own.")
			return nil
		},
	}
}

func spAPPurgeCacheCmd() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:     "purge-cache <id> [--model <name>]",
		Short:   "Remove cached answers of an access point (admins)",
		Long:    "Removes the response-cache entries of the access point, or of one model, in the API process that answers. Other replicas keep their own cache.",
		Example: "  aiplex access-points purge-cache ha\n  aiplex access-points purge-cache ha --model gpt",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := newClient().PurgeAccessPointCache(cmd.Context(), args[0], model)
			if err != nil {
				return spExplain("purge cache", err)
			}
			if spStructured(cmd, map[string]any{"access_point_id": args[0], "model": model, "purged": n}) {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %d cached answers of access point %s (this API process).\n", n, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Only this model's cached answers")
	return cmd
}
