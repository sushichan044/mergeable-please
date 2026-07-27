package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/sushichan044/mergeable-please/internal/output"
)

func newCheckCmd(r runner) *cobra.Command {
	return &cobra.Command{
		Use:   "check [pr]",
		Short: "Check merge readiness of a pull request",
		Long: `Check whether a pull request is mergeable and show any blockers or advisories.

[pr] is optional. Accepted forms:
  - omitted: uses the current branch's open PR
  - bare number (e.g. 42): uses the current repo's PR #42
  - GitHub URL (e.g. https://github.com/owner/repo/pull/42)

Exit codes:
  0  PR is mergeable (all blockers resolved, reviewer loop terminal if configured)
  1  PR has unresolved blockers
  2  Usage, configuration, or API error`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd.Context(), r, args)
		},
	}
}

func runCheck(ctx context.Context, r runner, args []string) error {
	var prArg string
	if len(args) > 0 {
		prArg = args[0]
	}

	report, err := r.app.Check(ctx, prArg)
	if err != nil {
		return err
	}

	// Build concise loop view from the already-fetched snapshot when reviewers
	// are configured. ThreadComments is NOT called here: check output shows
	// per-reviewer counts instead of comment bodies, keeping output token-efficient.
	var loopView *output.LoopView
	if len(report.Policies) > 0 {
		lv := buildConciseLoopView(*report.Result.ReviewerLoop, report.Snapshot, report.Policies)
		loopView = &lv
	}

	if renderErr := output.RenderCheckResult(r.out, report.Result, loopView, report.PR.Target()); renderErr != nil {
		return renderErr
	}

	if !report.Result.Satisfied {
		return ErrBlocked
	}
	return nil
}
