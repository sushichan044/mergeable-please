package mergeableplease

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sushichan044/mergeable-please/internal/backend/github"
	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// Request fires review re-requests for eligible reviewers and returns a structured [RequestReport].
// reviewerFlag limits re-requests to a single reviewer ("type:name" or "type");
// pass "" to target all configured reviewers.
//
// On a mid-iteration failure the returned RequestReport still carries the
// outcomes collected before the error, so callers can render partial progress.
func (a *App) Request(ctx context.Context, prArg, reviewerFlag string) (RequestReport, error) {
	policies, err := a.resolvePolicies()
	if err != nil {
		return RequestReport{}, err
	}
	if len(policies) == 0 {
		return RequestReport{}, errors.New("no reviewers configured in .mergeable-please.yml")
	}

	if a.fetchSnapshot == nil {
		return RequestReport{}, errMissingDep("FetchSnapshot")
	}
	if a.triggerer == nil {
		return RequestReport{}, errMissingDep("Triggerer")
	}

	pr, err := a.resolvePR(ctx, prArg)
	if err != nil {
		return RequestReport{}, fmt.Errorf("could not resolve PR: %w", err)
	}

	snapshot, err := a.fetchSnapshot(ctx, pr, policies)
	if err != nil {
		return RequestReport{}, fmt.Errorf("could not fetch PR state: %w", err)
	}

	loopState := reviewer.EvaluateLoop(policies, snapshot)
	targets := selectTargets(loopState.Reviewers, policies, reviewerFlag)

	if reviewerFlag != "" && len(targets) == 0 {
		return RequestReport{}, fmt.Errorf("unknown reviewer %q", reviewerFlag)
	}

	// fireRequests may return outcomes collected before a mid-iteration failure.
	// Surface them alongside the error so the CLI can still render partial
	// progress, matching the pre-refactor streaming output.
	outcomes, err := a.fireRequests(pr, targets)
	return RequestReport{PR: pr, Outcomes: outcomes}, err
}

// reviewerTarget pairs a reviewer.State with its reviewer.Policy.
type reviewerTarget struct {
	state  reviewer.State
	policy reviewer.Policy
}

// selectTargets filters reviewer states to those matching the optional flag.
func selectTargets(
	states []reviewer.State,
	policies []reviewer.Policy,
	reviewerFlag string,
) []reviewerTarget {
	policyByIdentity := make(map[reviewer.Identity]reviewer.Policy, len(policies))
	for _, p := range policies {
		policyByIdentity[p.Identity] = p
	}

	var targets []reviewerTarget
	for _, rs := range states {
		if reviewerFlag != "" && !matchesFlag(rs.Identity, reviewerFlag) {
			continue
		}
		targets = append(targets, reviewerTarget{
			state:  rs,
			policy: policyByIdentity[rs.Identity],
		})
	}
	return targets
}

// matchesFlag reports whether the identity's key equals the flag value. The key
// is "type:name" for named reviewers (e.g. "user:alice") and bare "type" only
// for nameless identities (e.g. "github-copilot"); passing "user" never matches
// a named user.
func matchesFlag(id reviewer.Identity, flag string) bool {
	return strings.EqualFold(github.IdentityKey(id), flag)
}

// fireRequests fires re-requests for each target and returns the outcomes.
func (a *App) fireRequests(pr github.PR, targets []reviewerTarget) ([]RequestOutcome, error) {
	outcomes := make([]RequestOutcome, 0, len(targets))
	for _, t := range targets {
		key := github.IdentityKey(t.state.Identity)

		if !t.state.CanRerequest {
			outcomes = append(outcomes, RequestOutcome{
				Identity:    t.state.Identity,
				Key:         key,
				Fired:       false,
				BlockReason: t.state.BlockReason,
			})
			continue
		}

		if err := a.triggerer.RequestReview(pr, t.policy); err != nil {
			// Return the outcomes gathered so far so the caller can render the
			// reviewers already processed before this failure.
			return outcomes, fmt.Errorf("failed to request review from %s: %w", key, err)
		}

		outcomes = append(outcomes, RequestOutcome{
			Identity: t.state.Identity,
			Key:      key,
			Fired:    true,
		})
	}
	return outcomes, nil
}
