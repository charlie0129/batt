package daemon

import (
	"github.com/charlie0129/batt/pkg/compatibility"
)

// chargeSwitch is the on/off primitive the active maintain loop toggles to hold
// the battery between the lower and upper limits. Two implementations exist:
// the legacy SMC charge-enable keys, and — when those keys are gated by Apple
// (macOS 27 beta 4+ firmware) — cutting wall power via the adapter key, which
// is not entitlement-gated. "Enabled" always means "the battery is allowed to
// charge".
type chargeSwitch interface {
	IsEnabled() (bool, error)
	Enable() error
	Disable() error
}

// chargeKeySwitch toggles charging directly with the legacy CH0B/CH0C/CHTE keys.
type chargeKeySwitch struct{ d *Daemon }

func (s chargeKeySwitch) IsEnabled() (bool, error) { return s.d.smc.IsChargingEnabled() }
func (s chargeKeySwitch) Enable() error            { return s.d.smc.EnableCharging() }
func (s chargeKeySwitch) Disable() error           { return s.d.smc.DisableCharging() }

// adapterSwitch holds the charge limit by cutting and restoring wall power.
// Disable() runs the Mac from the battery so it discharges toward the lower
// limit; Enable() restores wall power so it charges toward the upper limit.
// This is the only charge-control mechanism left on macOS 27 firmware whose
// SMC charge keys require an Apple-private entitlement, and it accepts any
// limit, including below 80%.
type adapterSwitch struct{ d *Daemon }

func (s adapterSwitch) IsEnabled() (bool, error) { return s.d.smc.IsAdapterEnabled() }
func (s adapterSwitch) Enable() error            { return s.d.enableAdapter() }
func (s adapterSwitch) Disable() error           { return s.d.disableAdapter() }

func (d *Daemon) getCapabilities() compatibility.Capabilities {
	d.chargeControlMu.RLock()
	defer d.chargeControlMu.RUnlock()
	return d.capabilities
}

func (d *Daemon) getCharger() chargeSwitch {
	d.chargeControlMu.RLock()
	defer d.chargeControlMu.RUnlock()
	return d.charger
}

// loadChargeControl returns the capabilities and charger as one consistent pair.
func (d *Daemon) loadChargeControl() (compatibility.Capabilities, chargeSwitch) {
	d.chargeControlMu.RLock()
	defer d.chargeControlMu.RUnlock()
	return d.capabilities, d.charger
}

// setChargeControl swaps capabilities and its matching charge switch together.
// It also holds loopMu so a running maintain loop never sees a half-updated
// pair.
func (d *Daemon) setChargeControl(caps compatibility.Capabilities) {
	d.loopMu.Lock()
	defer d.loopMu.Unlock()
	d.storeChargeControl(caps, d.selectCharger(caps.ChargeControlMode))
}

// storeChargeControl swaps capabilities and charger together. The caller must
// hold loopMu.
func (d *Daemon) storeChargeControl(caps compatibility.Capabilities, ch chargeSwitch) {
	d.chargeControlMu.Lock()
	defer d.chargeControlMu.Unlock()
	d.capabilities = caps
	d.charger = ch
}

// selectCharger picks the charge switch for the detected mode.
func (d *Daemon) selectCharger(mode compatibility.ChargeControlMode) chargeSwitch {
	if mode == compatibility.ChargeControlAdapter {
		return adapterSwitch{d}
	}
	return chargeKeySwitch{d}
}

// usesActiveChargeControl reports whether batt itself runs the hysteresis loop
// (reading the charge and toggling the switch), as opposed to delegating the
// limit to Apple's firmware or the built-in macOS limit.
func (d *Daemon) usesActiveChargeControl() bool {
	return modeUsesActiveChargeControl(d.getCapabilities().ChargeControlMode)
}

func modeUsesActiveChargeControl(mode compatibility.ChargeControlMode) bool {
	switch mode {
	case compatibility.ChargeControlLegacy, compatibility.ChargeControlAdapter:
		return true
	default:
		return false
	}
}
