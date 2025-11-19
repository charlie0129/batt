// Package calibration defines the types used by the battery auto-calibration
// workflow. It contains:
//
//   - Phase: the discrete steps of the calibration state machine
//   - State: the persisted runtime state managed by the daemon
//   - Status: a synthesized view model returned by HTTP APIs and used by the GUI
//
// These types are shared across daemon, client and GUI code to avoid duplicate
// definitions and keep JSON contracts consistent.
package calibration
