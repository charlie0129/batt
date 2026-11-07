package daemon

import (
	"log/slog"
	"math"

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
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
	if err == nil && info != nil && info.IOKit != nil {
		fillMissingCapacities(info.IOKit, readBatteryDataCapacities)
	}
	return info, err
}

// batteryCapacities are battery capacities in mAh. 0 means unknown.
type batteryCapacities struct {
	Design int // DesignCapacity
	Max    int // AppleRawMaxCapacity, what the battery holds when full today
}

// fillMissingCapacities fills in the capacities powerkit-go could not read.
// powerkit-go reads DesignCapacity and AppleRawMaxCapacity from the top level
// of the AppleSmartBattery IORegistry entry. macOS 27 keeps only percentages
// there and moved the mAh values into the entry's BatteryData dictionary,
// which read returns. Without the fallback powerkit-go reports both as 0, and
// health as 0%.
//
// Only the fields batt reads are filled in. Earlier macOS versions report both
// capacities at the top level, so read is not called there.
func fillMissingCapacities(d *powerkit.IOKitData, read func() (batteryCapacities, error)) {
	b := &d.Battery
	if b.DesignCapacity > 0 && b.MaxCapacity > 0 {
		return
	}
	c, err := read()
	if err != nil {
		slog.Debug("battery capacities are missing from IOKit", "error", err)
		return
	}
	if b.DesignCapacity <= 0 {
		b.DesignCapacity = c.Design
	}
	if b.MaxCapacity <= 0 {
		b.MaxCapacity = c.Max
	}
	// powerkit-go computed health from the missing values. Redo it the way
	// powerkit-go does.
	if b.DesignCapacity > 0 {
		d.Calculations.HealthByMaxCapacity = int(math.Round(float64(b.MaxCapacity) / float64(b.DesignCapacity) * 100))
	}
}

func (darwinBatteryInfo) FullChargeOverride() (bool, error) {
	return powerui.FullChargeOverride()
}
