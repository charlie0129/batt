package gui

import (
	"github.com/getlantern/systray"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/internal/client"
)

var apiClient *client.Client

func NewGUICommand(unixSocketPath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "gui",
		Short:  "Start the batt GUI",
		Hidden: true,
		Long: `Start the batt GUI.

This should be called by end users.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			apiClient = client.NewClient(unixSocketPath)
			systray.Run(onReady, onExit)
			return nil
		},
	}

	return cmd
}
