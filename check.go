package mergeableplease

import (
	"context"
	"fmt"

	"github.com/sushichan044/mergeable-please/internal/core/reviewer"
)

// Check evaluates PR merge readiness and returns a structured [CheckReport].
// The reviewer loop is attached when policies are configured.
// [core.CheckResult.Finalize] is called; use CheckReport.Result.Satisfied for the verdict.
func (a *App) Check(ctx context.Context, prArg string) (CheckReport, error) {
	if a.bundledEvaluate == nil {
		return CheckReport{}, errMissingDep("BundledEvaluate")
	}

	pr, err := a.resolvePR(ctx, prArg)
	if err != nil {
		return CheckReport{}, fmt.Errorf("could not resolve PR: %w", err)
	}

	result, err := a.bundledEvaluate(ctx, pr)
	if err != nil {
		return CheckReport{}, fmt.Errorf("could not evaluate PR: %w", err)
	}

	policies, err := a.resolvePolicies()
	if err != nil {
		return CheckReport{}, err
	}

	var snapshot reviewer.Snapshot
	if len(policies) > 0 {
		if a.fetchSnapshot == nil {
			return CheckReport{}, errMissingDep("FetchSnapshot")
		}
		snapshot, err = a.fetchSnapshot(ctx, pr, policies)
		if err != nil {
			return CheckReport{}, fmt.Errorf("could not fetch reviewer snapshot: %w", err)
		}
		loopState := reviewer.EvaluateLoop(policies, snapshot)
		result.ReviewerLoop = &loopState
	}

	result.Finalize()

	return CheckReport{
		PR:       pr,
		Result:   result,
		Snapshot: snapshot,
		Policies: policies,
	}, nil
}
