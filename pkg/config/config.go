package config

type Config interface {
	UpperLimit() int
	LowerLimit() int
	PreventIdleSleep() bool
	DisableChargingPreSleep() bool
	AllowNonRootAccess() bool
	ControlMagSafeLED() bool

	SetUpperLimit(int)
	SetLowerLimit(int)
	SetPreventIdleSleep(bool)
	SetDisableChargingPreSleep(bool)
	SetAllowNonRootAccess(bool)
	SetControlMagSafeLED(bool)

	// Load reads the configuration from the source.
	Load() error
	// Save saves the configuration to the source.
	Save() error
}
