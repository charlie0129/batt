package daemon

import (
	"github.com/peterneutron/powerkit-go/pkg/powerkit"

	"github.com/charlie0129/batt/pkg/powerui"
	"github.com/charlie0129/batt/pkg/smc"
)

// darwinSystem is the real macOS implementation of System around an opened
// SMC connection.
func darwinSystem(conn *smc.AppleSMC) System {
	return System{
		SMC:         conn,
		NativeLimit: powerui.New(),
		Sleep:       iokitSleepSetting{},
		Assertions:  &iokitAssertions{},
		Notifier:    iokitSleepNotifier{},
		Battery:     darwinBatteryInfo{},
	}
}

// darwinBatteryInfo reads battery telemetry through IOKit (powerkit-go) and
// the native charge-limit override through PowerUI.
type darwinBatteryInfo struct{}

func (darwinBatteryInfo) SystemInfo() (*powerkit.SystemInfo, error) {
	return powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
}

func (darwinBatteryInfo) FullChargeOverride() (bool, error) {
	return powerui.FullChargeOverride()
}
