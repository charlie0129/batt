package config

import (
	"time"

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
	CalibrationDischargeThreshold() int
	CalibrationHoldDurationMinutes() int
	Cron() string
	DisableUntil() time.Time
	PreDisableLimit() int
	AdapterDisableUntil() time.Time

	SetUpperLimit(int)
	SetLowerLimit(int)
	SetPreventIdleSleep(bool)
	SetDisableChargingPreSleep(bool)
	SetPreventSystemSleep(bool)
	SetAllowNonRootAccess(bool)
	SetControlMagSafeLED(ControlMagSafeMode)
	SetCron(string)
	SetCalibrationDischargeThreshold(int)
	SetCalibrationHoldDurationMinutes(int)
	SetDisableTimer(time.Time, int)
	ClearDisableTimer()
	SetAdapterDisableTimer(time.Time)
	ClearAdapterDisableTimer()

	LogrusFields() logrus.Fields

	// Load reads the configuration from the source.
	Load() error
	// Save saves the configuration to the source.
	Save() error
}
