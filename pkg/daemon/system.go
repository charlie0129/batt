package daemon

import (
	"github.com/charlie0129/gosmc"
	"github.com/peterneutron/powerkit-go/pkg/powerkit"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/powerui"
	"github.com/charlie0129/batt/pkg/smc"
)

// This file declares what the daemon needs from the Mac it runs on. Run wires
// the real SMC and IOKit implementations (see darwin.go and the iokit_*.go
// files); tests drive the daemon with a simulated firmware and fakes instead.

// SMC is the part of the Apple SMC the daemon drives. *smc.AppleSMC implements
// it. Which keys the firmware exposes decides the charge-control mode, so a
// test chooses a Mac by choosing the keys of its simulated SMC.
type SMC interface {
	Close() error
	Read(key string) (gosmc.Value, error)
	ChargeControlMode() compatibility.ChargeControlMode
	IsAdapterControlCapable() bool
	CheckMagSafeExistence() bool
	GetBatteryCharge() (int, error)
	IsPluggedIn() (bool, error)
	IsChargingEnabled() (bool, error)
	EnableCharging() error
	DisableCharging() error
	IsAdapterEnabled() (bool, error)
	EnableAdapter() error
	DisableAdapter() error
	GetFirmwareChargeLimit() (smc.FirmwareChargeLimit, error)
	EnsureFirmwareChargeLimit(lower, upper int) (bool, error)
	EnsureFirmwareChargeLimitDisabled() (bool, error)
	ResetChargeControl() error
	GetMagSafeLedState() (smc.MagSafeLedState, error)
	SetMagSafeLedState(state smc.MagSafeLedState) error
	DisableMagSafeLed() error
	SetMagSafeCharging(charging bool) error
}

// SleepSetting is the persistent SleepDisabled system power setting, the one
// `pmset -a disablesleep` changes. Unlike a power assertion it also suppresses
// lid-close sleep, and it outlives the process.
type SleepSetting interface {
	Disabled() (bool, error)
	SetDisabled(disabled bool) error
}

// SleepAssertions are the process-scoped IOPM power assertions the daemon
// takes. macOS drops them when the process exits.
type SleepAssertions interface {
	// PreventSleepOnAC keeps the system in dark wake while charging on AC.
	// Taking it twice is a no-op.
	PreventSleepOnAC() error
	// AllowSleepOnAC releases PreventSleepOnAC. Releasing twice is a no-op.
	AllowSleepOnAC() error
	// PreventCalibrationSleep prevents idle sleep on battery and AC while a
	// calibration runs. Forced sleep, such as closing the lid, still happens.
	PreventCalibrationSleep() error
	// AllowCalibrationSleep releases PreventCalibrationSleep.
	AllowCalibrationSleep() error
}

// SleepHandler receives the system power transitions the daemon reacts to.
type SleepHandler interface {
	// CanSystemSleep is asked before idle sleep. Returning false vetoes it.
	// macOS does not ask before forced sleep, such as closing the lid.
	CanSystemSleep() bool
	// SystemWillSleep runs right before any sleep, forced sleep included.
	SystemWillSleep()
	// SystemHasPoweredOn runs once the system has finished waking up.
	SystemHasPoweredOn()
}

// SleepNotifier delivers system power transitions to a SleepHandler.
type SleepNotifier interface {
	// Listen blocks and delivers notifications to h until Stop is called.
	Listen(h SleepHandler) error
	Stop()
}

// BatteryInfo reads battery telemetry the SMC does not expose.
type BatteryInfo interface {
	// SystemInfo returns an IOKit snapshot of the battery and the adapter.
	SystemInfo() (*powerkit.SystemInfo, error)
	// FullChargeOverride reports whether macOS is charging to full once,
	// pausing its own charge limit.
	FullChargeOverride() (bool, error)
}

// System is everything the daemon needs from the machine. Every field must be
// set.
type System struct {
	SMC         SMC
	NativeLimit powerui.Controller
	Sleep       SleepSetting
	Assertions  SleepAssertions
	Notifier    SleepNotifier
	Battery     BatteryInfo
}
