package daemon

import (
	"fmt"
	"sync"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"
)

type nativeBatteryState struct {
	charge   int
	charging bool
	plugged  bool
}

// Read actual charging, not the entitlement-gated SMC charge-enable switch.
var readNativeBatteryState = func() (nativeBatteryState, error) {
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
	if err != nil {
		return nativeBatteryState{}, err
	}
	if info == nil || info.IOKit == nil {
		return nativeBatteryState{}, fmt.Errorf("no native battery telemetry available")
	}
	return nativeBatteryState{
		charge: info.IOKit.Battery.CurrentCharge, charging: info.IOKit.State.IsCharging,
		plugged: info.IOKit.State.IsConnected,
	}, nil
}

// Release can also be requested by a mode transition outside the maintain loop.
var (
	nativeBandMu sync.Mutex
	nativeBand   nativeBandState
)

func resetNativeBand() {
	nativeBandMu.Lock()
	defer nativeBandMu.Unlock()
	nativeBand = nativeBandState{}
}

type nativeBandState struct {
	lower, upper int
	initialized  bool
	charging     bool
	hold         int
}

// target maps batt's hysteresis to PowerUI's single target. While charging,
// request the upper limit. While paused, hold the observed charge rather than
// leaving a higher ceiling that could top up inside the band. Never request a
// target below the observed charge just to reach the lower threshold.
// This requests a pause; it cannot bypass Apple's delay or full-charge override.
func (s *nativeBandState) target(lower, upper int, battery nativeBatteryState) (int, error) {
	if upper < 10 || upper >= 100 || lower < 0 || lower > upper ||
		battery.charge < 0 || battery.charge > 100 {
		return 0, fmt.Errorf("invalid native charge band or telemetry: lower=%d upper=%d charge=%d", lower, upper, battery.charge)
	}
	wasCharging := s.initialized && s.charging
	if !s.initialized || s.lower != lower || s.upper != upper {
		// On restart or a changed band, preserve a real charge already in
		// progress. A paused battery inside the band must stay paused.
		*s = nativeBandState{lower: lower, upper: upper, initialized: true, charging: battery.charging}
	}
	switch {
	case !battery.plugged, battery.charge >= upper:
		s.charging = false
	case battery.charge < lower:
		s.charging = true
	}
	if s.charging {
		s.hold = 0
		return upper, nil
	}
	target := max(1, battery.charge)
	if wasCharging && battery.charge >= upper {
		target = upper
	}
	// Never chase an overshoot upwards while waiting for macOS to stop.
	// Natural downward drift may lower the pause target, but a delayed
	// charging sample must not raise the ceiling and prolong that charge.
	if s.hold == 0 || target < s.hold {
		s.hold = target
	}
	return s.hold, nil
}

func ensureNativeChargeBand(lower, upper int) (bool, error) {
	if upper >= 100 {
		return ensureNativeChargeLimitDisabled()
	}
	battery, err := readNativeBatteryState()
	if err != nil {
		return false, fmt.Errorf("read native battery state: %w", err)
	}
	nativeBandMu.Lock()
	target, err := nativeBand.target(lower, upper, battery)
	charging := nativeBand.charging
	nativeBandMu.Unlock()
	if err != nil {
		return false, err
	}
	// Keep the charging intent across delayed/failed writes. Otherwise a
	// battery rising into the band could lose a pending charge request.
	if charging || target >= 100 {
		return ensureNativeChargeLimit(target)
	}
	current, enabled, err := nativeLimit.Limit()
	if err != nil || (enabled && current == target) {
		return false, err
	}
	if err := nativeLimit.HoldAt(target); err != nil {
		return false, err
	}
	return true, nil
}
