package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/spf13/cobra"

	mergeableplease "github.com/sushichan044/mergeable-please"
	"github.com/sushichan044/mergeable-please/internal/backend"
	"github.com/sushichan044/mergeable-please/internal/backend/github"
	"github.com/sushichan044/mergeable-please/internal/config"
	"github.com/sushichan044/mergeable-please/internal/core"
	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// ErrBlocked is returned by the check command when the PR is not yet mergeable.
// main() translates this to exit code 1 (blocked), distinct from exit 2 (error).
var ErrBlocked = errors.New("PR is not yet mergeable")

// runner groups the App and output writer used by all CLI commands.
type runner struct {
	app *mergeableplease.App
	out io.Writer
}

// newRootCmd constructs the root cobra command with all subcommands wired.
func newRootCmd(r runner) *cobra.Command {
	root := &cobra.Command{
		Use:   "mergeable-please",
		Short: "Check and advance pull request merge readiness",
		Long: `mergeable-please checks whether a GitHub pull request is mergeable and,
when configured, loops AI reviewer interactions until all goals are met.

Running without a subcommand is equivalent to running 'check'.

No configuration file is required: the default settings check for conflicts,
required CI failures, and ruleset blockers. Reviewer loops are opt-in via
.mergeable-please.yml at the repository root.`,
		SilenceUsage: true,
		// main() owns error printing and exit-code mapping; don't let cobra
		// also print the error (which would duplicate the message).
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd.Context(), r, args)
		},
	}

	root.AddCommand(newCheckCmd(r))
	root.AddCommand(newRequestCmd(r))
	root.AddCommand(newViewCmd(r))
	root.AddCommand(newInitCmd(r))

	return root
}

// Execute builds the production App, constructs the root command, and runs it.
// Errors are returned to main for exit-code handling.
//
// Config loading and GitHub client creation are deferred until first use so
// that `--help`, `help`, and `init` work even without GitHub auth or a valid
// config file.
func Execute(w io.Writer) error {
	// Memoized config loader — loads once on first call, caches result.
	var (
		cfgOnce   sync.Once
		cachedCfg *config.Config
		cfgErr    error
	)
	loadConfig := func() (*config.Config, error) {
		cfgOnce.Do(func() {
			cachedCfg, cfgErr = config.Load()
		})
		// Return the raw error; resolvePolicies adds "could not load config"
		// context once (wrapping here too would duplicate it).
		return cachedCfg, cfgErr
	}

	// Memoized GitHub client provider — connects once on first call.
	var (
		clientOnce   sync.Once
		cachedClient *github.Client
		clientErr    error
	)
	getClient := func() (*github.Client, error) {
		clientOnce.Do(func() {
			cachedClient, clientErr = github.NewClient()
		})
		return cachedClient, clientErr
	}

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: github.GHPRResolver{},
		BundledEvaluate: func(ctx context.Context, pr github.PR) (core.CheckResult, error) {
			client, err := getClient()
			if err != nil {
				return core.CheckResult{}, err
			}
			be := github.NewGitHubBackendWithClient(client)
			return be.BundledEvaluate(ctx, backend.PRCoords{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number})
		},
		FetchSnapshot: func(ctx context.Context, pr github.PR, policies []reviewer.Policy) (reviewer.Snapshot, error) {
			client, err := getClient()
			if err != nil {
				return reviewer.Snapshot{}, err
			}
			return github.FetchSnapshot(ctx, client, pr, policies)
		},
		ThreadComments: func(ctx context.Context, pr github.PR, policies []reviewer.Policy) (map[string][]github.ThreadComment, error) {
			client, err := getClient()
			if err != nil {
				return nil, err
			}
			return github.ThreadComments(ctx, client, pr, policies)
		},
		FetchBranchRules: func(ctx context.Context, pr github.PR) ([]backend.BranchRule, error) {
			client, err := getClient()
			if err != nil {
				return nil, err
			}
			be := github.NewGitHubBackendWithClient(client)
			return be.FetchBranchRules(ctx, backend.PRCoords{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number})
		},
		Triggerer: github.NewTriggerer(),
		// config loading + policy mapping stay in the binary wiring so the
		// public App/Deps surface does not depend on internal/config. The error
		// context lives here (not in resolvePolicies) to preserve the original
		// user-facing messages.
		LoadPolicies: func() ([]reviewer.Policy, error) {
			cfg, err := loadConfig()
			if err != nil {
				return nil, fmt.Errorf("could not load config: %w", err)
			}
			policies, err := config.Policies(cfg)
			if err != nil {
				return nil, fmt.Errorf("could not resolve reviewer policies: %w", err)
			}
			return policies, nil
		},
		InitConfig: config.Init,
	})

	return newRootCmd(runner{app: app, out: w}).Execute()
}
