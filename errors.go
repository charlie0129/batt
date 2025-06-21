package main

import "errors"

var (
	ErrDaemonNotRunning = errors.New("daemon not running")

	ErrPermissionDenied = errors.New("permission denied")
)
