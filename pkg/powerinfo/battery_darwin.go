package powerinfo

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include "battery.h"
*/
import "C"

import (
	"fmt"
	"math"
)

// ReadBattery reads the latest driver sample directly, without asking powerd
// about unrelated system state while the native charging policy is reloaded.
func ReadBattery() (*Battery, error) {
	var snapshot C.batt_battery_snapshot
	if result := C.batt_read_battery(&snapshot); result != 0 {
		return nil, fmt.Errorf("read AppleSmartBattery: 0x%08x", uint32(result))
	}
	return &Battery{
		State:          BatteryState(snapshot.state),
		DesignCapacity: int(snapshot.designCapacity),
		MaxCapacity:    int(snapshot.maxCapacity),
		ChargeRate:     int(math.Round(float64(snapshot.voltage) * float64(snapshot.amperage) / 1000)),
		DesignVoltage:  float64(snapshot.voltage) / 1000,
	}, nil
}
