package mergeableplease

import (
	"context"
	"fmt"

	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// Evaluate calls bundledEvaluate without running the reviewer loop or calling
// Finalize. Used by the view command for checks/conflicts/default modes.
func (a *App) Evaluate(ctx context.Context, prArg string) (EvaluateReport, error) {
	if a.bundledEvaluate == nil {
		return EvaluateReport{}, errMissingDep("BundledEvaluate")
	}

	pr, err := a.resolvePR(ctx, prArg)
	if err != nil {
		return EvaluateReport{}, fmt.Errorf("could not resolve PR: %w", err)
	}

	result, err := a.bundledEvaluate(ctx, pr)
	if err != nil {
		return EvaluateReport{}, fmt.Errorf("could not evaluate PR: %w", err)
	}

	return EvaluateReport{PR: pr, Result: result}, nil
}

// BranchRules fetches branch rules for the PR's base branch.
func (a *App) BranchRules(ctx context.Context, prArg string) (BranchRulesReport, error) {
	if a.fetchBranchRules == nil {
		return BranchRulesReport{}, errMissingDep("FetchBranchRules")
	}

	pr, err := a.resolvePR(ctx, prArg)
	if err != nil {
		return BranchRulesReport{}, fmt.Errorf("could not resolve PR: %w", err)
	}

	rules, err := a.fetchBranchRules(ctx, pr)
	if err != nil {
		return BranchRulesReport{}, fmt.Errorf("could not fetch branch rules: %w", err)
	}

	return BranchRulesReport{PR: pr, Rules: rules}, nil
}

// Reviewers fetches reviewer loop state and thread comments for the PR.
// ReviewerReport.NoReviewers is true when no policies are configured.
func (a *App) Reviewers(ctx context.Context, prArg string) (ReviewerReport, error) {
	// Resolve the PR before loading config so that, when both could fail, the
	// PR-resolution error takes precedence — matching the pre-refactor view
	// path, which resolved the PR before dispatching to the reviewers branch.
	pr, err := a.resolvePR(ctx, prArg)
	if err != nil {
		return ReviewerReport{}, fmt.Errorf("could not resolve PR: %w", err)
	}

	policies, err := a.resolvePolicies()
	if err != nil {
		return ReviewerReport{}, err
	}

	if len(policies) == 0 {
		return ReviewerReport{PR: pr, Policies: policies, NoReviewers: true}, nil
	}

	if a.fetchSnapshot == nil {
		return ReviewerReport{}, errMissingDep("FetchSnapshot")
	}
	if a.threadComments == nil {
		return ReviewerReport{}, errMissingDep("ThreadComments")
	}

	snapshot, err := a.fetchSnapshot(ctx, pr, policies)
	if err != nil {
		return ReviewerReport{}, fmt.Errorf("could not fetch reviewer snapshot: %w", err)
	}

	allCommentsByKey, err := a.threadComments(ctx, pr, policies)
	if err != nil {
		return ReviewerReport{}, fmt.Errorf("could not fetch thread comments: %w", err)
	}

	loopState := reviewer.EvaluateLoop(policies, snapshot)

	return ReviewerReport{
		PR:            pr,
		LoopState:     loopState,
		Policies:      policies,
		CommentsByKey: allCommentsByKey,
	}, nil
}
