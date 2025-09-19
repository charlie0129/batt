package config

import (
	"github.com/sirupsen/logrus"
)

type Config interface {
	UpperLimit() int
	LowerLimit() int
	PreventIdleSleep() bool
	DisableChargingPreSleep() bool
	PreventSystemSleep() bool
	AllowNonRootAccess() bool
	ControlMagSafeLED() ControlMagSafeMode

	SetUpperLimit(int)
	SetLowerLimit(int)
	SetPreventIdleSleep(bool)
	SetDisableChargingPreSleep(bool)
	SetPreventSystemSleep(bool)
	SetAllowNonRootAccess(bool)
	SetControlMagSafeLED(ControlMagSafeMode)

	LogrusFields() logrus.Fields

	// Load reads the configuration from the source.
	Load() error
	// Save saves the configuration to the source.
	Save() error
}
