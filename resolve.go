package mergeableplease

import (
	"context"

	"github.com/sushichan044/mergeable-please/internal/backend/github"
	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// resolvePR resolves the target PR from an optional positional argument.
//
// Resolution order:
//  1. If arg is non-empty: parse as number or GitHub URL via [github.ParsePRArg].
//     For bare numbers, owner/repo come from [github.PRResolver.CurrentRepo].
//  2. If arg is empty: delegate to [github.PRResolver.CurrentPR] (current branch).
func (a *App) resolvePR(ctx context.Context, arg string) (github.PR, error) {
	if a.resolver == nil {
		return github.PR{}, errMissingDep("Resolver")
	}

	if arg != "" {
		owner, repo, number, err := github.ParsePRArg(arg)
		if err != nil {
			return github.PR{}, err
		}

		if owner == "" || repo == "" {
			// Bare number: get owner/repo from the repo context, not from the
			// current-branch PR (which may not exist or may be a different PR).
			o, r, resolveErr := a.resolver.CurrentRepo(ctx)
			if resolveErr != nil {
				return github.PR{}, resolveErr
			}
			owner, repo = o, r
		}

		return github.PR{Owner: owner, Repo: repo, Number: number}, nil
	}

	owner, repo, number, err := a.resolver.CurrentPR(ctx)
	if err != nil {
		return github.PR{}, err
	}

	return github.PR{Owner: owner, Repo: repo, Number: number}, nil
}

// resolvePolicies returns the configured reviewer policies via the injected
// LoadPolicies dependency. Returns empty policies when no reviewers are
// configured — not an error. The request command validates non-empty policies
// separately.
//
// Policy loading is injected (not done here) so the public API stays decoupled
// from the config package: the binary wiring owns config loading + mapping.
func (a *App) resolvePolicies() ([]reviewer.Policy, error) {
	if a.loadPolicies == nil {
		return nil, errMissingDep("LoadPolicies")
	}

	// Return the loader error as-is: the binary's LoadPolicies owns the
	// user-facing context (config-load vs policy-mapping), preserving the
	// pre-refactor error text.
	return a.loadPolicies()
}
