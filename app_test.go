package mergeableplease_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mergeableplease "github.com/sushichan044/mergeable-please"
	"github.com/sushichan044/mergeable-please/internal/backend/github"
	"github.com/sushichan044/mergeable-please/internal/config"
	"github.com/sushichan044/mergeable-please/internal/core"
	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakePRResolver struct {
	owner  string
	repo   string
	number int
	err    error
}

func (f *fakePRResolver) CurrentPR(_ context.Context) (string, string, int, error) {
	return f.owner, f.repo, f.number, f.err
}

func (f *fakePRResolver) CurrentRepo(_ context.Context) (string, string, error) {
	return f.owner, f.repo, f.err
}

type captureExec struct {
	calls [][]string
	err   error
}

func (c *captureExec) exec(args ...string) (bytes.Buffer, bytes.Buffer, error) {
	c.calls = append(c.calls, args)
	return bytes.Buffer{}, bytes.Buffer{}, c.err
}

// ---------------------------------------------------------------------------
// Config helpers
// ---------------------------------------------------------------------------

func configWithReviewers(yamlContent string) *config.Config {
	cfg, err := config.Parse([]byte(yamlContent))
	if err != nil {
		panic("configWithReviewers: " + err.Error())
	}
	return cfg
}

func defaultConfig() *config.Config {
	return configWithReviewers("")
}

func minimalConfig() *config.Config {
	return configWithReviewers(`
github:
  reviewers:
    - type: user
      name: alice
      goal:
        approved: true
      max-rallies: 3
    - type: github-copilot
      goal:
        reviewed-clean: true
      max-rallies: 5
`)
}

func emptyReviewersConfig() *config.Config {
	return configWithReviewers(`
github:
  reviewers: []
`)
}

// policiesFor builds a LoadPolicies dependency from a parsed config, mirroring
// the binary's config-to-policies wiring.
func policiesFor(cfg *config.Config) func() ([]reviewer.Policy, error) {
	return func() ([]reviewer.Policy, error) { return config.Policies(cfg) }
}

// ---------------------------------------------------------------------------
// Check tests
// ---------------------------------------------------------------------------

func TestApp_Check_Satisfied(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{}, nil // no blockers → satisfied after Finalize
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	report, err := app.Check(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, report.Result.Satisfied)
}

func TestApp_Check_Blocked_ResultNotSatisfied(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{
				Blockers: []core.Condition{
					{Kind: core.ConditionConflict, Severity: core.SeverityBlocker, Title: "Merge conflicts"},
				},
			}, nil
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	report, err := app.Check(context.Background(), "")
	require.NoError(t, err) // App.Check never returns ErrBlocked — that is CLI's job
	assert.False(t, report.Result.Satisfied)
	require.Len(t, report.Result.Blockers, 1)
	assert.Equal(t, core.ConditionConflict, report.Result.Blockers[0].Kind)
}

func TestApp_Check_ReviewerLoop_NotDone_NotSatisfied(t *testing.T) {
	t.Parallel()

	snapshot := reviewer.Snapshot{
		HeadCommitOID: "head123",
		Threads: []reviewer.Thread{
			{Reviewer: reviewer.Identity{Type: reviewer.ReviewerTypeGitHubCopilot}, Resolved: false},
		},
	}

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{}, nil
		},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		LoadPolicies: policiesFor(minimalConfig()),
	})

	report, err := app.Check(context.Background(), "")
	require.NoError(t, err)
	assert.False(t, report.Result.Satisfied)
	require.NotNil(t, report.Result.ReviewerLoop)
	assert.False(t, report.Result.ReviewerLoop.Done)
	assert.Len(t, report.Policies, 2)
}

func TestApp_Check_ReviewerLoop_Done_Satisfied(t *testing.T) {
	t.Parallel()

	aliceIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeUser, Name: "alice"}
	copilotIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeGitHubCopilot}

	snapshot := reviewer.Snapshot{
		HeadCommitOID: "head123",
		Reviews: []reviewer.Review{
			{Reviewer: aliceIdentity, State: reviewer.ReviewStateApproved, CommitOID: "head123"},
			{
				Reviewer:           copilotIdentity,
				State:              reviewer.ReviewStateCommented,
				CommitOID:          "head123",
				InlineCommentCount: 0,
			},
		},
	}

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{}, nil
		},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		LoadPolicies: policiesFor(minimalConfig()),
	})

	report, err := app.Check(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, report.Result.Satisfied)
	require.NotNil(t, report.Result.ReviewerLoop)
	assert.True(t, report.Result.ReviewerLoop.Done)
}

func TestApp_Check_Advisories_DontBlock(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{
				Advisories: []core.Condition{
					{
						Kind:     core.ConditionApprovalRequired,
						Severity: core.SeverityAdvisory,
						Title:    "Human approval required",
					},
				},
			}, nil
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	report, err := app.Check(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, report.Result.Satisfied, "advisories alone must not block")
	require.Len(t, report.Result.Advisories, 1)
}

// ---------------------------------------------------------------------------
// PR resolution tests
// ---------------------------------------------------------------------------

func TestApp_Check_BareNumber_UsesCurrentRepo(t *testing.T) {
	t.Parallel()

	resolver := &fakePRResolver{owner: "org", repo: "rep"}
	var capturedPR github.PR

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: resolver,
		BundledEvaluate: func(_ context.Context, pr github.PR) (core.CheckResult, error) {
			capturedPR = pr
			return core.CheckResult{}, nil
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	_, err := app.Check(context.Background(), "42")
	require.NoError(t, err)
	assert.Equal(t, "org", capturedPR.Owner, "owner must come from CurrentRepo")
	assert.Equal(t, "rep", capturedPR.Repo, "repo must come from CurrentRepo")
	assert.Equal(t, 42, capturedPR.Number, "number must come from the argument")
}

func TestApp_Check_URL_ParsedDirectly(t *testing.T) {
	t.Parallel()

	resolver := &fakePRResolver{err: errors.New("should not be called")}
	var capturedPR github.PR

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: resolver,
		BundledEvaluate: func(_ context.Context, pr github.PR) (core.CheckResult, error) {
			capturedPR = pr
			return core.CheckResult{}, nil
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	_, err := app.Check(context.Background(), "https://github.com/myorg/myrepo/pull/7")
	require.NoError(t, err)
	assert.Equal(t, "myorg", capturedPR.Owner)
	assert.Equal(t, "myrepo", capturedPR.Repo)
	assert.Equal(t, 7, capturedPR.Number)
}

// ---------------------------------------------------------------------------
// Request tests
// ---------------------------------------------------------------------------

func TestApp_Request_FiresOnlyCanRerequest(t *testing.T) {
	t.Parallel()

	at := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	aliceIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeUser, Name: "alice"}
	copilotIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeGitHubCopilot}

	snapshot := reviewer.Snapshot{
		HeadCommitOID: "headCommit",
		Triggers: []reviewer.TriggerAction{
			{Reviewer: aliceIdentity, At: at.Add(-time.Hour)},
		},
		Reviews: []reviewer.Review{
			{Reviewer: aliceIdentity, State: reviewer.ReviewStateApproved, CommitOID: "headCommit", At: at},
		},
		Threads: []reviewer.Thread{
			{Reviewer: copilotIdentity, Resolved: false},
		},
	}

	exec := &captureExec{}
	triggerer := github.NewTriggererWithExec(exec.exec)

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 4},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		Triggerer:    triggerer,
		LoadPolicies: policiesFor(minimalConfig()),
	})

	report, err := app.Request(context.Background(), "", "")
	require.NoError(t, err)
	require.Len(t, report.Outcomes, 2)

	// alice: approved on head — goal met, CanRerequest should be false
	assert.Equal(t, "user:alice", report.Outcomes[0].Key)
	assert.False(t, report.Outcomes[0].Fired)
	assert.NotEmpty(t, report.Outcomes[0].BlockReason)

	// copilot: unresolved thread — not done, CanRerequest should be true
	assert.Equal(t, "github-copilot", report.Outcomes[1].Key)
	assert.True(t, report.Outcomes[1].Fired)

	require.Len(t, exec.calls, 1)
	assert.Contains(t, strings.Join(exec.calls[0], " "), "@copilot")
}

func TestApp_Request_ReviewerFlag_TargetsExactlyOne(t *testing.T) {
	t.Parallel()

	exec := &captureExec{}
	triggerer := github.NewTriggererWithExec(exec.exec)

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 5},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return reviewer.Snapshot{HeadCommitOID: "headCommit"}, nil
		},
		Triggerer:    triggerer,
		LoadPolicies: policiesFor(minimalConfig()),
	})

	report, err := app.Request(context.Background(), "", "user:alice")
	require.NoError(t, err)
	require.Len(t, report.Outcomes, 1)
	assert.Equal(t, "user:alice", report.Outcomes[0].Key)
	assert.True(t, report.Outcomes[0].Fired)

	require.Len(t, exec.calls, 1)
	assert.Contains(t, strings.Join(exec.calls[0], " "), "alice")
}

func TestApp_Request_Blocked_ReturnsSkipOutcome(t *testing.T) {
	t.Parallel()

	at := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	aliceIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeUser, Name: "alice"}

	snapshot := reviewer.Snapshot{
		HeadCommitOID: "headCommit",
		Triggers: []reviewer.TriggerAction{
			{Reviewer: aliceIdentity, At: at.Add(-time.Hour)},
		},
		Reviews: []reviewer.Review{
			{Reviewer: aliceIdentity, State: reviewer.ReviewStateChangesRequested, CommitOID: "headCommit", At: at},
		},
	}

	exec := &captureExec{}
	triggerer := github.NewTriggererWithExec(exec.exec)

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 6},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		Triggerer:    triggerer,
		LoadPolicies: policiesFor(minimalConfig()),
	})

	report, err := app.Request(context.Background(), "", "user:alice")
	require.NoError(t, err)
	require.Len(t, report.Outcomes, 1)
	assert.Equal(t, "user:alice", report.Outcomes[0].Key)
	assert.False(t, report.Outcomes[0].Fired)
	assert.Contains(t, report.Outcomes[0].BlockReason, "no new commit since last review")
	assert.Empty(t, exec.calls)
}

func TestApp_Request_NoReviewers_ReturnsError(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver:     &fakePRResolver{owner: "myorg", repo: "myrepo", number: 11},
		Triggerer:    github.NewTriggererWithExec((&captureExec{}).exec),
		LoadPolicies: policiesFor(emptyReviewersConfig()),
	})

	_, err := app.Request(context.Background(), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reviewers configured")
}

func TestApp_Request_PartialFailure_ReturnsCollectedOutcomesWithError(t *testing.T) {
	t.Parallel()

	at := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	aliceIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeUser, Name: "alice"}
	copilotIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeGitHubCopilot}

	// alice approved on head (goal met → SKIP, collected first), copilot has an
	// unresolved thread (eligible → fires). The trigger exec fails, so the copilot
	// re-request errors mid-iteration. The earlier SKIP outcome must still be returned.
	snapshot := reviewer.Snapshot{
		HeadCommitOID: "headCommit",
		Triggers: []reviewer.TriggerAction{
			{Reviewer: aliceIdentity, At: at.Add(-time.Hour)},
		},
		Reviews: []reviewer.Review{
			{Reviewer: aliceIdentity, State: reviewer.ReviewStateApproved, CommitOID: "headCommit", At: at},
		},
		Threads: []reviewer.Thread{
			{Reviewer: copilotIdentity, Resolved: false},
		},
	}

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 8},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		Triggerer:    github.NewTriggererWithExec((&captureExec{err: errors.New("gh exec failed")}).exec),
		LoadPolicies: policiesFor(minimalConfig()),
	})

	report, err := app.Request(context.Background(), "", "")
	require.Error(t, err, "a failed re-request must surface an error")
	require.NotEmpty(t, report.Outcomes, "outcomes collected before the failure must be returned")
	assert.Equal(t, "user:alice", report.Outcomes[0].Key)
	assert.False(t, report.Outcomes[0].Fired)
}

func TestApp_Reviewers_ResolverError_TakesPrecedence(t *testing.T) {
	t.Parallel()

	// Both PR resolution and config loading would run, but the PR-resolution
	// error must surface first — matching the pre-refactor view ordering.
	app := mergeableplease.New(mergeableplease.Deps{
		Resolver:     &fakePRResolver{err: errors.New("no PR for branch")},
		LoadPolicies: policiesFor(minimalConfig()),
	})

	_, err := app.Reviewers(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not resolve PR",
		"resolver failure must take precedence over config loading")
}

// ---------------------------------------------------------------------------
// Init tests
// ---------------------------------------------------------------------------

func TestApp_Init_ReturnsPath(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		InitConfig: func() (string, error) { return "/repo/.mergeable-please.yml", nil },
	})

	report, err := app.Init()
	require.NoError(t, err)
	assert.Equal(t, "/repo/.mergeable-please.yml", report.Path)
}

func TestApp_Init_PropagatesError(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		InitConfig: func() (string, error) { return "", config.ErrConfigExists },
	})

	_, err := app.Init()
	require.Error(t, err)
	assert.ErrorIs(t, err, config.ErrConfigExists)
}

// ---------------------------------------------------------------------------
// Missing-dependency guards
//
// App is a public API: a required dependency that was never injected must
// surface as an actionable error from the method that needs it, not a
// nil-function panic deeper in the call stack.
// ---------------------------------------------------------------------------

func TestApp_Check_MissingBundledEvaluate_ReturnsError(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver:     &fakePRResolver{owner: "org", repo: "repo", number: 1},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	_, err := app.Check(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BundledEvaluate")
}

func TestApp_BranchRules_MissingFetchBranchRules_ReturnsError(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
	})

	_, err := app.BranchRules(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FetchBranchRules")
}

func TestApp_Reviewers_MissingFetchSnapshot_ReturnsError(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{
		Resolver:     &fakePRResolver{owner: "org", repo: "repo", number: 1},
		LoadPolicies: policiesFor(minimalConfig()),
	})

	_, err := app.Reviewers(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FetchSnapshot")
}

func TestApp_Init_MissingInitConfig_ReturnsError(t *testing.T) {
	t.Parallel()

	app := mergeableplease.New(mergeableplease.Deps{})

	_, err := app.Init()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "InitConfig")
}
