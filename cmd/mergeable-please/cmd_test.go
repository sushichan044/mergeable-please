package main

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

// newApp is a convenience wrapper around mergeableplease.New.
func newApp(d mergeableplease.Deps) *mergeableplease.App {
	return mergeableplease.New(d)
}

// ---------------------------------------------------------------------------
// check command tests
// ---------------------------------------------------------------------------

func TestCheck_Satisfied_ExitsZero(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{Satisfied: true}, nil
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	var buf bytes.Buffer
	err := runCheck(context.Background(), runner{app: app, out: &buf}, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "status: satisfied")
}

func TestCheck_Blocked_ReturnsErrBlocked(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
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

	var buf bytes.Buffer
	err := runCheck(context.Background(), runner{app: app, out: &buf}, nil)
	require.ErrorIs(t, err, ErrBlocked)
	assert.Contains(t, buf.String(), "status: blocked")
}

func TestCheck_WithReviewerLoop_NotDone_ReturnsErrBlocked(t *testing.T) {
	t.Parallel()

	snapshot := reviewer.Snapshot{
		HeadCommitOID: "head123",
		Threads: []reviewer.Thread{
			{Reviewer: reviewer.Identity{Type: reviewer.ReviewerTypeGitHubCopilot}, Resolved: false},
		},
	}

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{}, nil
		},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runCheck(context.Background(), runner{app: app, out: &buf}, nil)
	require.ErrorIs(t, err, ErrBlocked)
}

func TestCheck_WithReviewerLoop_Done_ExitsZero(t *testing.T) {
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

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{}, nil
		},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runCheck(context.Background(), runner{app: app, out: &buf}, nil)
	require.NoError(t, err)
}

func TestCheck_Advisories_AlwaysShown_EvenWhenSatisfied(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
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

	var buf bytes.Buffer
	err := runCheck(context.Background(), runner{app: app, out: &buf}, nil)
	require.NoError(t, err, "advisories alone should not block")
	out := buf.String()
	assert.Contains(t, out, "status: satisfied")
	assert.Contains(t, out, "~ approval required (human)", "advisory must appear even when satisfied")
}

// ---------------------------------------------------------------------------
// request command tests
// ---------------------------------------------------------------------------

func TestRequest_FiresOnlyCanRerequest(t *testing.T) {
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

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 4},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		Triggerer:    triggerer,
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runRequest(context.Background(), runner{app: app, out: &buf}, "", nil)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "SKIP  user:alice")
	assert.Contains(t, out, "FIRED github-copilot")
	require.Len(t, exec.calls, 1)
	assert.Contains(t, strings.Join(exec.calls[0], " "), "@copilot")
}

func TestRequest_ReviewerFlag_TargetsExactlyOne(t *testing.T) {
	t.Parallel()

	exec := &captureExec{}
	triggerer := github.NewTriggererWithExec(exec.exec)

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 5},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return reviewer.Snapshot{HeadCommitOID: "headCommit"}, nil
		},
		Triggerer:    triggerer,
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runRequest(context.Background(), runner{app: app, out: &buf}, "user:alice", nil)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "FIRED user:alice")
	assert.NotContains(t, out, "github-copilot")
	require.Len(t, exec.calls, 1)
	assert.Contains(t, strings.Join(exec.calls[0], " "), "alice")
}

func TestRequest_BlockedReviewer_PrintsNoOpReason(t *testing.T) {
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

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 6},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		Triggerer:    triggerer,
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runRequest(context.Background(), runner{app: app, out: &buf}, "user:alice", nil)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "SKIP  user:alice")
	assert.Contains(t, out, "no new commit since last review")
	assert.Empty(t, exec.calls)
}

func TestRequest_NoReviewers_ReturnsError(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		Resolver:     &fakePRResolver{owner: "myorg", repo: "myrepo", number: 11},
		Triggerer:    github.NewTriggererWithExec((&captureExec{}).exec),
		LoadPolicies: policiesFor(emptyReviewersConfig()),
	})

	var buf bytes.Buffer
	err := runRequest(context.Background(), runner{app: app, out: &buf}, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reviewers configured")
}

func TestRequest_UnknownReviewerFlag_ReturnsError(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 9},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return reviewer.Snapshot{HeadCommitOID: "headCommit"}, nil
		},
		Triggerer:    github.NewTriggererWithExec((&captureExec{}).exec),
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runRequest(context.Background(), runner{app: app, out: &buf}, "user:nonexistent", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown reviewer")
}

func TestRequest_PartialFailure_PrintsCollectedOutcomesThenError(t *testing.T) {
	t.Parallel()

	at := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	aliceIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeUser, Name: "alice"}
	copilotIdentity := reviewer.Identity{Type: reviewer.ReviewerTypeGitHubCopilot}

	// alice has approved on head (goal met → SKIP), copilot has an unresolved
	// thread (eligible → fires). The trigger exec fails, so the copilot
	// re-request errors mid-iteration. The earlier SKIP must still be printed.
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

	exec := &captureExec{err: errors.New("gh exec failed")}
	triggerer := github.NewTriggererWithExec(exec.exec)

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "myorg", repo: "myrepo", number: 7},
		FetchSnapshot: func(_ context.Context, _ github.PR, _ []reviewer.Policy) (reviewer.Snapshot, error) {
			return snapshot, nil
		},
		Triggerer:    triggerer,
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runRequest(context.Background(), runner{app: app, out: &buf}, "", nil)
	require.Error(t, err, "a failed re-request must surface an error")
	assert.Contains(t, buf.String(), "SKIP  user:alice",
		"outcomes collected before the failure must still be printed")
}

// ---------------------------------------------------------------------------
// init command tests
// ---------------------------------------------------------------------------

func TestInit_PrintsCreatedPath(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		InitConfig: func() (string, error) { return "/repo/.mergeable-please.yml", nil },
	})

	var buf bytes.Buffer
	err := runInit(runner{app: app, out: &buf})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "/repo/.mergeable-please.yml")
}

func TestInit_PropagatesError(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		InitConfig: func() (string, error) { return "", config.ErrConfigExists },
	})

	var buf bytes.Buffer
	err := runInit(runner{app: app, out: &buf})
	require.Error(t, err)
	assert.ErrorIs(t, err, config.ErrConfigExists)
}

// ---------------------------------------------------------------------------
// view command tests
// ---------------------------------------------------------------------------

func TestView_ChecksDimension_NoStatusLine(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{
				Blockers: []core.Condition{
					{
						Kind:     core.ConditionCheckFailing,
						Severity: core.SeverityBlocker,
						Title:    "CI failing",
						Detail:   "lint",
					},
					{Kind: core.ConditionConflict, Severity: core.SeverityBlocker, Title: "Conflicts"},
				},
			}, nil
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	var buf bytes.Buffer
	err := runView(context.Background(), runner{app: app, out: &buf}, "checks", nil)
	require.NoError(t, err)
	out := buf.String()
	assert.NotContains(t, out, "status:", "dimension view must never emit a global status line")
	assert.Contains(t, out, "check-failing", "check condition must appear")
	assert.NotContains(t, out, "conflict", "conflict condition must be filtered out for checks dimension")
}

func TestView_ConflictsDimension_NoStatusLine(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		Resolver: &fakePRResolver{owner: "org", repo: "repo", number: 1},
		BundledEvaluate: func(_ context.Context, _ github.PR) (core.CheckResult, error) {
			return core.CheckResult{
				Blockers: []core.Condition{
					{Kind: core.ConditionCheckFailing, Severity: core.SeverityBlocker, Title: "CI failing"},
					{Kind: core.ConditionConflict, Severity: core.SeverityBlocker, Title: "Merge conflicts"},
				},
			}, nil
		},
		LoadPolicies: policiesFor(defaultConfig()),
	})

	var buf bytes.Buffer
	err := runView(context.Background(), runner{app: app, out: &buf}, "conflicts", nil)
	require.NoError(t, err)
	out := buf.String()
	assert.NotContains(t, out, "status:", "dimension view must never emit a global status line")
	assert.Contains(t, out, "conflict", "conflict condition must appear")
	assert.NotContains(t, out, "check-failing", "check condition must be filtered out for conflicts dimension")
}

func TestView_ReviewersDimension_NoReviewers_PrintsGuidance(t *testing.T) {
	t.Parallel()

	app := newApp(mergeableplease.Deps{
		Resolver:     &fakePRResolver{owner: "org", repo: "repo", number: 1},
		LoadPolicies: policiesFor(emptyReviewersConfig()),
	})

	var buf bytes.Buffer
	err := runView(context.Background(), runner{app: app, out: &buf}, "reviewers", nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No reviewers configured",
		"reviewers dimension must guide the user when no reviewers are configured")
}

func TestView_ReviewersDimension_ResolverError_TakesPrecedence(t *testing.T) {
	t.Parallel()

	// Resolver fails; the PR-resolution error must surface before any config or
	// snapshot work, matching the pre-refactor view ordering.
	app := newApp(mergeableplease.Deps{
		Resolver:     &fakePRResolver{err: errors.New("no PR for branch")},
		LoadPolicies: policiesFor(minimalConfig()),
	})

	var buf bytes.Buffer
	err := runView(context.Background(), runner{app: app, out: &buf}, "reviewers", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not resolve PR")
}
