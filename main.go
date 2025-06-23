package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/charlie0129/batt/internal/client"
	"github.com/sirupsen/logrus"
)

func setupLogger() error {
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return fmt.Errorf("failed to parse log level: %v", err)
	}
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return nil
}

func handleCmdError(err error) {
	if errors.Is(err, client.ErrDaemonNotRunning) {
		fmt.Fprintln(os.Stderr, "\nError: batt daemon is not running")
		fmt.Fprintln(os.Stderr, "Is the daemon running? Have you installed it?")
	} else if errors.Is(err, client.ErrPermissionDenied) {
		fmt.Fprintln(os.Stderr, "\nError: Permission Denied")
		fmt.Fprintln(os.Stderr, "  - Try running the command again with 'sudo'")
		fmt.Fprintln(os.Stderr, "  - Or reinstall the daemon with the '--allow-non-root-access' flag to grant permissions to your user")
	}
}

func main() {
	cmd := NewCommand()
	if err := cmd.Execute(); err != nil {
		handleCmdError(err)
		os.Exit(1)
	}
}
