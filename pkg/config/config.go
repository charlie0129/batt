package config

import (
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/temperature"
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
	TemperatureMonitoringEnabled() bool
	TemperatureProtectionThresholdCelsius() int
	TrayIconStyle() TrayIconStyle
	TrayIconRefreshIntervalSeconds() int
	TemperatureReferences() temperature.References
	Cron() string

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
	SetTemperatureMonitoringEnabled(bool)
	SetTemperatureProtectionThresholdCelsius(int)
	SetTrayIconStyle(TrayIconStyle)
	SetTemperatureReference(temperature.Scenario, float64)

	LogrusFields() logrus.Fields

	// Load reads the configuration from the source.
	Load() error
	// Save saves the configuration to the source.
	Save() error
}
