package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newInitCmd(r runner) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a default .mergeable-please.yml in the current repository",
		Long: `Create .mergeable-please.yml at the current repository root.

The file is a commented template. Edit it to configure reviewer loops or to
override defaults, then commit it so collaborators and CI use the same policy.

Running mergeable-please check without this file uses built-in defaults
(conflicts + required CI checks enabled, no reviewer loops).

This command refuses to overwrite an existing config.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runInit(r)
		},
	}
}

func runInit(r runner) error {
	report, err := r.app.Init()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(r.out, "Created %s\n", report.Path)
	return err
}
