package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newRequestCmd(r runner) *cobra.Command {
	var reviewerFlag string

	cmd := &cobra.Command{
		Use:   "request [pr]",
		Short: "Fire review re-requests for eligible reviewers",
		Long: `Fire review re-requests for eligible reviewers on a pull request.

[pr] is optional. Accepted forms:
  - omitted: uses the current branch's open PR
  - bare number (e.g. 42): uses the current repo's PR #42
  - GitHub URL (e.g. https://github.com/owner/repo/pull/42)

By default, every configured reviewer is considered: eligible ones are
re-requested (FIRED) and ineligible ones are reported as SKIP with the reason.
Use --reviewer type:name to target a single reviewer.

Requires reviewers to be configured in .mergeable-please.yml.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRequest(cmd.Context(), r, reviewerFlag, args)
		},
	}

	cmd.Flags().StringVar(
		&reviewerFlag, "reviewer", "",
		`target a single reviewer by identity, e.g. "user:alice" or "github-copilot"`,
	)

	return cmd
}

func runRequest(ctx context.Context, r runner, reviewerFlag string, args []string) error {
	var prArg string
	if len(args) > 0 {
		prArg = args[0]
	}

	report, reqErr := r.app.Request(ctx, prArg, reviewerFlag)

	// Print every collected outcome — even when Request failed mid-iteration —
	// so per-reviewer progress stays visible, then surface the error.
	for _, outcome := range report.Outcomes {
		if !outcome.Fired {
			if _, wErr := fmt.Fprintf(r.out, "SKIP  %s — %s\n", outcome.Key, outcome.BlockReason); wErr != nil {
				return wErr
			}
		} else {
			if _, wErr := fmt.Fprintf(r.out, "FIRED %s\n", outcome.Key); wErr != nil {
				return wErr
			}
		}
	}

	return reqErr
}
