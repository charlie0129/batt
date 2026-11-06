package daemon

import (
	"errors"
	"log/slog"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

// systemIsCharging reports whether macOS says the battery is charging right
// now. Firmware and native modes cannot tell from batt's own switch state.
func (d *Daemon) systemIsCharging() (bool, error) {
	info, err := d.battery.SystemInfo()
	if err != nil {
		return false, err
	}
	if info == nil || info.IOKit == nil {
		return false, errors.New("no IOKit battery data available")
	}
	return info.IOKit.State.IsCharging, nil
}

// maintainManagedMagSafeLED reflects actual charging in firmware and native
// modes, independent of whether macOS is currently enforcing a charge limit.
func (d *Daemon) maintainManagedMagSafeLED() {
	if !d.getCapabilities().MagSafeLED {
		return
	}

	var target smc.MagSafeLedState
	switch d.conf.ControlMagSafeLED() {
	case config.ControlMagSafeModeEnabled:
		charging, err := d.systemIsCharging()
		if err != nil {
			slog.Error("failed to read battery charging state for MagSafe LED", "error", err)
			return
		}
		target = smc.LEDGreen
		if charging {
			target = smc.LEDOrange
		}
	case config.ControlMagSafeModeAlwaysOff:
		target = smc.LEDOff
	default:
		return
	}

	current, err := d.smc.Read(smc.MagSafeLedKey)
	if err != nil {
		slog.Error("failed to read MagSafe LED state", "error", err)
		return
	}
	if len(current.Bytes) != 1 {
		slog.Error("invalid MagSafe LED state length")
		return
	}
	if current.Bytes[0] != byte(target) {
		if err := d.smc.SetMagSafeLedState(target); err != nil {
			slog.Error("failed to set MagSafe LED state", "error", err)
		}
	}
}

// releaseMagSafeLEDOnExit returns the LED to system control. The SMC keeps
// the last state batt wrote after the daemon exits, so without this the LED
// stays green or off even when macOS charges again.
func (d *Daemon) releaseMagSafeLEDOnExit() error {
	d.loopMu.Lock()
	defer d.loopMu.Unlock()
	if !d.getCapabilities().MagSafeLED || d.conf.ControlMagSafeLED() == config.ControlMagSafeModeDisabled {
		return nil
	}
	return d.smc.SetMagSafeLedState(smc.LEDSystem)
}
