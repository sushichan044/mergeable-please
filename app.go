package mergeableplease

import (
	"context"
	"fmt"

	"github.com/sushichan044/mergeable-please/internal/backend"
	"github.com/sushichan044/mergeable-please/internal/backend/github"
	"github.com/sushichan044/mergeable-please/internal/core"
	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// App is the high-level API for mergeable-please. It orchestrates PR
// evaluation, reviewer loops, review requests, and config initialization.
// Rendering and exit-code mapping are the caller's responsibility.
type App struct {
	resolver         github.PRResolver
	bundledEvaluate  func(ctx context.Context, pr github.PR) (core.CheckResult, error)
	fetchSnapshot    func(ctx context.Context, pr github.PR, policies []reviewer.Policy) (reviewer.Snapshot, error)
	threadComments   func(ctx context.Context, pr github.PR, policies []reviewer.Policy) (map[string][]github.ThreadComment, error)
	fetchBranchRules func(ctx context.Context, pr github.PR) ([]backend.BranchRule, error)
	triggerer        *github.Triggerer
	loadPolicies     func() ([]reviewer.Policy, error)
	initConfig       func() (string, error)
}

// Deps holds the injectable dependencies for [New]. Named Deps (not Config)
// to avoid collision with internal/config.Config at call sites that import both packages.
type Deps struct {
	Resolver         github.PRResolver
	BundledEvaluate  func(ctx context.Context, pr github.PR) (core.CheckResult, error)
	FetchSnapshot    func(ctx context.Context, pr github.PR, policies []reviewer.Policy) (reviewer.Snapshot, error)
	ThreadComments   func(ctx context.Context, pr github.PR, policies []reviewer.Policy) (map[string][]github.ThreadComment, error)
	FetchBranchRules func(ctx context.Context, pr github.PR) ([]backend.BranchRule, error)
	Triggerer        *github.Triggerer
	LoadPolicies     func() ([]reviewer.Policy, error)
	InitConfig       func() (string, error)
}

// errMissingDep reports that a required dependency was not injected. App is a
// public API, so a missing dependency surfaces as an actionable error here
// rather than a nil-function panic deeper in the call stack. Guards live at the
// point of use (not in New) so an App built for a subset of operations — e.g.
// only Init — stays valid.
func errMissingDep(name string) error {
	return fmt.Errorf("mergeableplease: required dependency %s is not configured", name)
}

// New constructs an App from the provided dependencies.
func New(d Deps) *App {
	return &App{
		resolver:         d.Resolver,
		bundledEvaluate:  d.BundledEvaluate,
		fetchSnapshot:    d.FetchSnapshot,
		threadComments:   d.ThreadComments,
		fetchBranchRules: d.FetchBranchRules,
		triggerer:        d.Triggerer,
		loadPolicies:     d.LoadPolicies,
		initConfig:       d.InitConfig,
	}
}
