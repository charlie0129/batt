package daemon

import (
	"sync"

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
type chargeKeySwitch struct{}

func (chargeKeySwitch) IsEnabled() (bool, error) { return smcConn.IsChargingEnabled() }
func (chargeKeySwitch) Enable() error            { return smcConn.EnableCharging() }
func (chargeKeySwitch) Disable() error           { return smcConn.DisableCharging() }

// adapterSwitch holds the charge limit by cutting and restoring wall power.
// Disable() runs the Mac from the battery so it discharges toward the lower
// limit; Enable() restores wall power so it charges toward the upper limit.
// This is the only charge-control mechanism left on macOS 27 firmware whose
// SMC charge keys require an Apple-private entitlement, and it accepts any
// limit, including below 80%.
type adapterSwitch struct{}

func (adapterSwitch) IsEnabled() (bool, error) { return smcConn.IsAdapterEnabled() }
func (adapterSwitch) Enable() error            { return smcConn.EnableAdapter() }
func (adapterSwitch) Disable() error           { return smcConn.DisableAdapter() }

// charger is the active-mode on/off primitive, selected at daemon startup from
// the detected charge-control mode. It defaults to the legacy charge keys so
// existing behavior and tests are unchanged until adapter mode is selected.
var charger chargeSwitch = chargeKeySwitch{}

// capabilitiesMu guards capabilities and charger, which are set as a pair at
// startup and whenever adapter mode is toggled at runtime. It is a leaf lock.
var capabilitiesMu sync.RWMutex

func getCapabilities() compatibility.Capabilities {
	capabilitiesMu.RLock()
	defer capabilitiesMu.RUnlock()
	return capabilities
}

func getCharger() chargeSwitch {
	capabilitiesMu.RLock()
	defer capabilitiesMu.RUnlock()
	return charger
}

// loadChargeControl returns the capabilities and charger as one consistent pair.
func loadChargeControl() (compatibility.Capabilities, chargeSwitch) {
	capabilitiesMu.RLock()
	defer capabilitiesMu.RUnlock()
	return capabilities, charger
}

// setChargeControl swaps capabilities and its matching charge switch together.
// It also holds maintainLoopInnerLock so a running maintain loop never sees a
// half-updated pair.
func setChargeControl(caps compatibility.Capabilities) {
	maintainLoopInnerLock.Lock()
	defer maintainLoopInnerLock.Unlock()
	storeChargeControl(caps, selectCharger(caps.ChargeControlMode))
}

// storeChargeControl swaps capabilities and charger together. The caller must
// hold maintainLoopInnerLock.
func storeChargeControl(caps compatibility.Capabilities, ch chargeSwitch) {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	capabilities = caps
	charger = ch
}

// selectCharger picks the charge switch for the detected mode.
func selectCharger(mode compatibility.ChargeControlMode) chargeSwitch {
	if mode == compatibility.ChargeControlAdapter {
		return adapterSwitch{}
	}
	return chargeKeySwitch{}
}

// usesActiveChargeControl reports whether batt itself runs the hysteresis loop
// (reading the charge and toggling the switch), as opposed to delegating the
// limit to Apple's firmware or the built-in macOS limit.
func usesActiveChargeControl() bool {
	return modeUsesActiveChargeControl(getCapabilities().ChargeControlMode)
}

func modeUsesActiveChargeControl(mode compatibility.ChargeControlMode) bool {
	switch mode {
	case compatibility.ChargeControlLegacy, compatibility.ChargeControlAdapter:
		return true
	default:
		return false
	}
}
