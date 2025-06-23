//go:build brew

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewInstallCommand .
func NewInstallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "install",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return errors.New("install command is not available on Homebrew-installed batt. Use `sudo brew services start batt` instead.")
		},
	}

	cmd.Flags().Bool("allow-non-root-access", false, "Allow non-root users to access batt daemon.")

	return cmd
}

// NewUninstallCommand .
func NewUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "uninstall",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return errors.New("uninstall command is not available on Homebrew-installed batt. Use `sudo brew services stop batt` instead.")
		},
	}
}
