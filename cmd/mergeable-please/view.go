package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sushichan044/mergeable-please/internal/backend/github"
	"github.com/sushichan044/mergeable-please/internal/core"
	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
	"github.com/sushichan044/mergeable-please/internal/output"
)

func newViewCmd(r runner) *cobra.Command {
	var conditionFlag string

	cmd := &cobra.Command{
		Use:   "view [pr]",
		Short: "Show detailed information for a specific merge condition",
		Long: `Show detailed information for a specific merge-readiness condition.

[pr] is optional. Accepted forms:
  - omitted: uses the current branch's open PR
  - bare number (e.g. 42): uses the current repo's PR #42
  - GitHub URL (e.g. https://github.com/owner/repo/pull/42)

--condition choices:
  checks     Show failing/pending required CI checks
  conflicts  Show conflict status
  rules      Show configured branch ruleset rules (REST API call)
  reviewers  Show reviewer loop state and thread comments`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runView(cmd.Context(), r, conditionFlag, args)
		},
	}

	cmd.Flags().StringVar(
		&conditionFlag, "condition", "",
		`condition to drill into: "conflicts", "checks", "rules", or "reviewers"`,
	)

	return cmd
}

func runView(ctx context.Context, r runner, condition string, args []string) error {
	var prArg string
	if len(args) > 0 {
		prArg = args[0]
	}

	switch condition {
	case "rules":
		return runViewRules(ctx, r, prArg)
	case "reviewers":
		return runViewReviewers(ctx, r, prArg)
	case "checks":
		// Show only check-related conditions; omit global status (reviewer loop not evaluated).
		report, err := r.app.Evaluate(ctx, prArg)
		if err != nil {
			return err
		}
		blockers := filterConditionsByKind(
			report.Result.Blockers,
			core.ConditionCheckFailing,
			core.ConditionCheckPending,
		)
		return output.RenderDimensionView(r.out, blockers, nil, report.PR.Target())
	case "conflicts":
		// Show only conflict-related conditions; omit global status.
		report, err := r.app.Evaluate(ctx, prArg)
		if err != nil {
			return err
		}
		blockers := filterConditionsByKind(report.Result.Blockers, core.ConditionConflict, core.ConditionBehindBase)
		return output.RenderDimensionView(r.out, blockers, nil, report.PR.Target())
	case "":
		// Full dimension view: all conditions, but no global status verdict.
		report, err := r.app.Evaluate(ctx, prArg)
		if err != nil {
			return err
		}
		return output.RenderDimensionView(r.out, report.Result.Blockers, report.Result.Advisories, report.PR.Target())
	default:
		return fmt.Errorf("unknown --condition %q: must be conflicts, checks, rules, or reviewers", condition)
	}
}

func runViewRules(ctx context.Context, r runner, prArg string) error {
	// fetchBranchRules is always wired in production via Execute(); nil is test-only.
	report, err := r.app.BranchRules(ctx, prArg)
	if err != nil {
		return err
	}

	// Use = (not :=) throughout to avoid shadowing the outer err declaration.
	if _, err = fmt.Fprintf(r.out, "target: %s\n", report.PR.Target()); err != nil {
		return err
	}

	if len(report.Rules) == 0 {
		_, err = fmt.Fprintln(r.out, "No branch rules configured.")
		return err
	}

	if _, err = fmt.Fprintf(r.out, "Branch rules (%d):\n", len(report.Rules)); err != nil {
		return err
	}

	for _, rule := range report.Rules {
		if _, err = fmt.Fprintf(r.out, "  - type: %s\n", rule.Type); err != nil {
			return err
		}
	}
	return nil
}

func runViewReviewers(ctx context.Context, r runner, prArg string) error {
	report, err := r.app.Reviewers(ctx, prArg)
	if err != nil {
		return err
	}

	if report.NoReviewers {
		_, err = fmt.Fprintln(r.out,
			"No reviewers configured. Add reviewers to .mergeable-please.yml to enable the reviewer loop.")
		return err
	}

	view := buildLoopView(report.LoopState, report.Policies, report.CommentsByKey, report.PR)
	return output.Render(r.out, view, report.PR.Target())
}

// reviewBodyDrillIn returns a gh command that prints a single review's body
// via the REST API. Returns "" when no review id is available.
func reviewBodyDrillIn(pr github.PR, reviewID string) string {
	if reviewID == "" {
		return ""
	}
	return fmt.Sprintf("gh api repos/%s/%s/pulls/%d/reviews/%s --jq .body",
		pr.Owner, pr.Repo, pr.Number, reviewID)
}

// buildLoopView maps a reviewer.LoopState into an output.LoopView.
func buildLoopView(
	state reviewer.LoopState,
	policies []reviewer.Policy,
	allCommentsByKey map[string][]github.ThreadComment,
	pr github.PR,
) output.LoopView {
	policyByIdentity := make(map[reviewer.Identity]reviewer.Policy, len(policies))
	for _, p := range policies {
		policyByIdentity[p.Identity] = p
	}

	reviewerViews := make([]output.ReviewerView, 0, len(state.Reviewers))
	for _, rs := range state.Reviewers {
		p := policyByIdentity[rs.Identity]
		key := github.IdentityKey(rs.Identity)

		var unresolvedComments []output.CommentView
		for _, c := range allCommentsByKey[key] {
			if c.Resolved {
				continue
			}
			unresolvedComments = append(unresolvedComments, output.CommentView{
				Author: c.Author,
				Body:   c.Body,
				URL:    c.URL,
				At:     c.CreatedAt,
			})
		}

		reviewerViews = append(reviewerViews, output.ReviewerView{
			Identity:           rs.Identity,
			Goal:               p.Goal,
			Phase:              rs.Phase,
			RallyCount:         rs.RallyCount,
			MaxRallies:         p.MaxRallies,
			GoalMet:            rs.GoalMet,
			CanRerequest:       rs.CanRerequest,
			BlockReason:        rs.BlockReason,
			UnresolvedComments: unresolvedComments,
			// Full mode: emit the body drill-in command so the agent can read
			// the review body (findings not tied to any inline thread) on demand.
			ChangesRequested:      rs.ChangesRequested,
			LatestReviewState:     rs.LatestReviewState,
			LatestReviewCommitOID: rs.LatestReviewCommitOID,
			ReviewBodyPresent:     rs.LatestReviewBodyPresent,
			ReviewBodyDrillInCmd:  reviewBodyDrillIn(pr, rs.LatestReviewID),
		})
	}

	return output.LoopView{
		Reviewers: reviewerViews,
		Done:      state.Done,
	}
}

// filterConditionsByKind returns only conditions whose Kind is in the allow-list.
func filterConditionsByKind(conditions []core.Condition, kinds ...core.ConditionKind) []core.Condition {
	allow := make(map[core.ConditionKind]bool, len(kinds))
	for _, k := range kinds {
		allow[k] = true
	}
	filtered := make([]core.Condition, 0, len(conditions))
	for _, c := range conditions {
		if allow[c.Kind] {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// buildConciseLoopView builds a LoopView for the check path.
// It uses only snapshot.Threads to count unresolved comments per reviewer —
// no ThreadComments round-trip needed, keeping check fast and output lean.
func buildConciseLoopView(
	state reviewer.LoopState,
	snapshot reviewer.Snapshot,
	policies []reviewer.Policy,
) output.LoopView {
	// Count unresolved threads per identity key from the already-fetched snapshot.
	unresolvedCounts := make(map[string]int)
	for _, t := range snapshot.Threads {
		if !t.Resolved {
			key := github.IdentityKey(t.Reviewer)
			unresolvedCounts[key]++
		}
	}

	policyByIdentity := make(map[reviewer.Identity]reviewer.Policy, len(policies))
	for _, p := range policies {
		policyByIdentity[p.Identity] = p
	}

	reviewerViews := make([]output.ReviewerView, 0, len(state.Reviewers))
	for _, rs := range state.Reviewers {
		p := policyByIdentity[rs.Identity]
		key := github.IdentityKey(rs.Identity)

		reviewerViews = append(reviewerViews, output.ReviewerView{
			Identity:        rs.Identity,
			Goal:            p.Goal,
			Phase:           rs.Phase,
			RallyCount:      rs.RallyCount,
			MaxRallies:      p.MaxRallies,
			GoalMet:         rs.GoalMet,
			CanRerequest:    rs.CanRerequest,
			BlockReason:     rs.BlockReason,
			UnresolvedCount: unresolvedCounts[key],
			// Concise mode: surface that a review body exists, but point at the
			// view command rather than emitting the body drill-in here.
			ChangesRequested:      rs.ChangesRequested,
			LatestReviewState:     rs.LatestReviewState,
			LatestReviewCommitOID: rs.LatestReviewCommitOID,
			ReviewBodyPresent:     rs.LatestReviewBodyPresent,
		})
	}

	return output.LoopView{
		Reviewers: reviewerViews,
		Done:      state.Done,
	}
}
