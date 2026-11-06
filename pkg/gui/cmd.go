package gui

import (
	"context"
	"log/slog"
	"runtime/cgo"

	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/logging"
	"github.com/charlie0129/batt/pkg/version"
)

func NewGUICommand(groupID string) *cobra.Command {
	return &cobra.Command{
		Use:     "gui",
		Short:   "Start the batt GUI (debug)",
		GroupID: groupID,
		Hidden:  true,
		Long: `Start the batt GUI.

This command should not be called directly by the user. Users should use the .app bundle to start the GUI.`,
		Run: func(cmd *cobra.Command, _ []string) {
			unixSocketPath, err := cmd.Flags().GetString("daemon-socket")
			if err != nil {
				logging.Fatal("Failed to get daemon-socket flag", "error", err)
			}
			Run(unixSocketPath)
		},
	}
}

func Run(unixSocketPath string) {
	slog.Info("batt gui",
		"version", version.Version,
		"gitCommit", version.GitCommit,
	)

	ctx, cancel := context.WithCancel(context.Background())
	controller := &menuController{
		api:              client.NewClient(unixSocketPath),
		calibrationPhase: calibration.PhaseIdle,
		eventCancel:      cancel,
	}
	handle := cgo.NewHandle(controller)
	controller.menu = newNativeMenu(handle, version.Version)

	defer func() {
		slog.Info("Cleaning up resources")
		cancel()
		controller.menu.close()
		handle.Delete()
	}()

	controller.refreshCompatibility()
	go controller.subscribeEvents(ctx)
	runNativeApp()
}
