package config

import (
	"log/slog"
	"time"
)

type Config interface {
	UpperLimit() int
	LowerLimit() int
	PreventIdleSleep() bool
	DisableChargingPreSleep() bool
	PreventSystemSleep() bool
	PreventSleepOnAdapterDisable() bool
	AllowNonRootAccess() bool
	AdapterMode() bool
	NoDrain() bool
	ControlMagSafeLED() ControlMagSafeMode
	CalibrationDischargeThreshold() int
	CalibrationHoldDurationMinutes() int
	Cron() string
	DisableUntil() time.Time
	PreDisableLimit() int
	AdapterDisableUntil() time.Time
	ChargeOnceTarget() int

	SetUpperLimit(int)
	SetLowerLimit(int)
	SetPreventIdleSleep(bool)
	SetDisableChargingPreSleep(bool)
	SetPreventSystemSleep(bool)
	SetPreventSleepOnAdapterDisable(bool)
	SetAllowNonRootAccess(bool)
	SetAdapterMode(bool)
	SetNoDrain(bool)
	SetControlMagSafeLED(ControlMagSafeMode)
	SetCron(string)
	SetCalibrationDischargeThreshold(int)
	SetCalibrationHoldDurationMinutes(int)
	SetDisableTimer(time.Time, int)
	ClearDisableTimer()
	SetAdapterDisableTimer(time.Time)
	ClearAdapterDisableTimer()
	SetChargeOnceTarget(int)
	ClearChargeOnceTarget()

	// LogValue renders the settings for a log line.
	LogValue() slog.Value

	// Load reads the configuration from the source.
	Load() error
	// Save saves the configuration to the source.
	Save() error
}
