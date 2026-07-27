package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExitCodes verifies the exit-code constants are the values the spec requires.
// Changing any of these would be a breaking change for callers that parse the exit code.
func TestExitCodes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, exitOK, "exitOK must be 0")
	assert.Equal(t, 1, exitBlocked, "exitBlocked must be 1")
	assert.Equal(t, 2, exitError, "exitError must be 2")
}

// TestRun_SkillsArg_DoesNotPanic verifies the skills dispatch branch
// does not panic and returns a valid exit code.
func TestRun_SkillsArg_DoesNotPanic(t *testing.T) {
	t.Parallel()

	code := run([]string{"skills"})
	// skills with no sub-command prints usage; either exitOK or exitError is acceptable.
	assert.Contains(t, []int{exitOK, exitError}, code, "skills dispatch must return a valid exit code")
}
