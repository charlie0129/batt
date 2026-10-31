package daemon

import (
	"errors"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

var firmwareIsCharging = func() (bool, error) {
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true})
	if err != nil {
		return false, err
	}
	if info == nil || info.IOKit == nil {
		return false, errors.New("no IOKit battery data available")
	}
	return info.IOKit.State.IsCharging, nil
}

// maintainFirmwareMagSafeLED reflects actual charging, independent of whether
// firmware is currently enforcing a charge limit.
func maintainFirmwareMagSafeLED() {
	if !getCapabilities().MagSafeLED {
		return
	}

	var target smc.MagSafeLedState
	switch conf.ControlMagSafeLED() {
	case config.ControlMagSafeModeEnabled:
		charging, err := firmwareIsCharging()
		if err != nil {
			logrus.WithError(err).Error("failed to read battery charging state for MagSafe LED")
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

	current, err := smcConn.Read(smc.MagSafeLedKey)
	if err != nil {
		logrus.WithError(err).Error("failed to read MagSafe LED state")
		return
	}
	if len(current.Bytes) != 1 {
		logrus.Error("invalid MagSafe LED state length")
		return
	}
	if current.Bytes[0] != byte(target) {
		if err := smcConn.SetMagSafeLedState(target); err != nil {
			logrus.WithError(err).Error("failed to set MagSafe LED state")
		}
	}
}
