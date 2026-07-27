package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Songmu/skillsmith"

	mergeableplease "github.com/sushichan044/mergeable-please"
	"github.com/sushichan044/mergeable-please/internal/version"
)

// Process exit codes.
const (
	exitOK      = 0 // success / PR mergeable
	exitBlocked = 1 // PR is blocked (not yet mergeable) — an expected outcome
	exitError   = 2 // usage, configuration, or API error
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the CLI and returns the process exit code.
func run(args []string) int {
	if len(args) > 0 && args[0] == "skills" {
		s, err := skillsmith.New("mergeable-please", version.Semver(), mergeableplease.SkillsFS)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitError
		}
		if runErr := s.Run(context.Background(), args[1:]); runErr != nil {
			fmt.Fprintln(os.Stderr, runErr)
			return exitError
		}
		return exitOK
	}

	if err := Execute(os.Stdout); err != nil {
		// ErrBlocked is an expected outcome: the full diagnosis (including the
		// status line) was already rendered to stdout, so don't print again.
		if errors.Is(err, ErrBlocked) {
			return exitBlocked
		}
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}
	return exitOK
}
