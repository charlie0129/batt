package main

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/daemon"
	"github.com/charlie0129/batt/pkg/version"
)

var (
	// alwaysAllowNonRootAccess indicates whether to always allow non-root users to access the batt daemon.
	alwaysAllowNonRootAccess = false
)

// NewDaemonCommand .
func NewDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "daemon",
		Hidden:  true,
		Short:   "Run batt daemon in the foreground",
		GroupID: gAdvanced,
		RunE: func(_ *cobra.Command, _ []string) error {
			logrus.WithFields(logrus.Fields{
				"version": version.Version,
				"commit":  version.GitCommit,
			}).Info("batt daemon starting")
			return daemon.Run(configPath, unixSocketPath, alwaysAllowNonRootAccess)
		},
	}

	f := cmd.Flags()

	f.BoolVar(&alwaysAllowNonRootAccess, "always-allow-non-root-access", false,
		"Always allow non-root users to access the daemon.")

	return cmd
}
