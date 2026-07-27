package mergeableplease

import (
	"github.com/sushichan044/mergeable-please/internal/backend"
	"github.com/sushichan044/mergeable-please/internal/backend/github"
	"github.com/sushichan044/mergeable-please/internal/core"
	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// CheckReport is the structured result of [App.Check].
// CLI is responsible for rendering and exit-code mapping:
// use len(Policies) > 0 to determine whether to show reviewer loop output.
type CheckReport struct {
	PR       github.PR
	Result   core.CheckResult
	Snapshot reviewer.Snapshot
	Policies []reviewer.Policy
}

// EvaluateReport is the result of [App.Evaluate].
// Unlike Check, Finalize is not called and the reviewer loop is not evaluated.
type EvaluateReport struct {
	PR     github.PR
	Result core.CheckResult
}

// BranchRulesReport is the result of [App.BranchRules].
type BranchRulesReport struct {
	PR    github.PR
	Rules []backend.BranchRule
}

// ReviewerReport is the result of [App.Reviewers].
type ReviewerReport struct {
	PR            github.PR
	LoopState     reviewer.LoopState
	Policies      []reviewer.Policy
	CommentsByKey map[string][]github.ThreadComment
	// NoReviewers is true when no reviewer policies are configured.
	// When true, LoopState and CommentsByKey are zero values.
	NoReviewers bool
}

// RequestOutcome records the result of one review re-request attempt.
type RequestOutcome struct {
	Identity    reviewer.Identity
	Key         string
	Fired       bool
	BlockReason string
}

// RequestReport is the result of [App.Request].
type RequestReport struct {
	PR       github.PR
	Outcomes []RequestOutcome
}

// InitReport is the result of [App.Init].
type InitReport struct {
	Path string
}
