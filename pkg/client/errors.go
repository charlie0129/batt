package client

import "errors"

var (
	// ErrDaemonNotRunning is returned when the daemon is not running
	ErrDaemonNotRunning = errors.New("daemon not running")

	// ErrPermissionDenied is returned when the user does not have permission to perform the requested action
	ErrPermissionDenied = errors.New("permission denied")
)
