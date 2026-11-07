package daemon

import (
	"log/slog"

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

// darwinBatteryInfo reads battery telemetry from the AppleSmartBattery
// IORegistry entry and the native charge-limit override through PowerUI.
type darwinBatteryInfo struct{}

func (darwinBatteryInfo) Snapshot() (BatterySnapshot, error) {
	b, err := readSmartBattery()
	if err != nil {
		return BatterySnapshot{}, err
	}
	fillMissingCapacities(&b, readBatteryDataCapacities)
	return b.snapshot(), nil
}

func (darwinBatteryInfo) FullChargeOverride() (bool, error) {
	return powerui.FullChargeOverride()
}

// fillMissingCapacities fills in the capacities missing from the top level of
// the AppleSmartBattery entry. macOS 27 keeps only percentages there and moved
// DesignCapacity and AppleRawMaxCapacity into the entry's BatteryData
// dictionary, which read returns. Earlier macOS versions report both at the
// top level, so read is not called there.
func fillMissingCapacities(b *smartBattery, read func() (batteryCapacities, error)) {
	if b.DesignCapacity > 0 && b.MaxCapacity > 0 {
		return
	}
	c, err := read()
	if err != nil {
		slog.Debug("battery capacities unavailable", "error", err)
		return
	}
	if b.DesignCapacity <= 0 {
		b.DesignCapacity = c.Design
	}
	if b.MaxCapacity <= 0 {
		b.MaxCapacity = c.Max
	}
}

// snapshot converts what the AppleSmartBattery entry reports to volts and
// amperes.
func (b smartBattery) snapshot() BatterySnapshot {
	return BatterySnapshot{
		Charging:       b.IsCharging,
		FullyCharged:   b.FullyCharged,
		Voltage:        float64(b.Voltage) / 1000,
		Amperage:       float64(b.Amperage) / 1000,
		CycleCount:     b.CycleCount,
		DesignCapacity: b.DesignCapacity,
		MaxCapacity:    b.MaxCapacity,
		// Power only flows from the adapter into the Mac. Ignore negative
		// readings rather than report the adapter taking power.
		InputVoltage:  float64(max(0, b.SystemVoltageIn)) / 1000,
		InputAmperage: float64(max(0, b.SystemCurrentIn)) / 1000,
	}
}
