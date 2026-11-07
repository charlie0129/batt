package daemon

import (
	"errors"
	"testing"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"
)

func TestFillMissingCapacities(t *testing.T) {
	// The BatteryData capacities of a MacBook Pro on macOS 27.2.
	macOS27 := batteryCapacities{Design: 6075, Max: 5752}

	tests := []struct {
		name string
		// battery and health are what powerkit-go reported.
		battery powerkit.IOKitBattery
		health  int
		// read is what the BatteryData dictionary holds.
		read    batteryCapacities
		readErr error

		wantRead   bool
		wantDesign int
		wantMax    int
		wantHealth int
	}{
		{
			name:     "macOS 27 moved both capacities into BatteryData",
			read:     macOS27,
			wantRead: true, wantDesign: 6075, wantMax: 5752, wantHealth: 95,
		},
		{
			name:    "earlier macOS reports both at the top level",
			battery: powerkit.IOKitBattery{DesignCapacity: 8694, MaxCapacity: 8573},
			health:  99,
			read:    macOS27,
			// BatteryData is not consulted, so nothing changes.
			wantDesign: 8694, wantMax: 8573, wantHealth: 99,
		},
		{
			name:     "only a missing capacity is filled in",
			battery:  powerkit.IOKitBattery{DesignCapacity: 6000},
			read:     macOS27,
			wantRead: true, wantDesign: 6000, wantMax: 5752, wantHealth: 96,
		},
		{
			name:     "BatteryData without the capacities",
			wantRead: true,
		},
		{
			name:     "BatteryData unavailable",
			read:     macOS27,
			readErr:  errors.New("AppleSmartBattery has no BatteryData dictionary"),
			wantRead: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &powerkit.IOKitData{
				Battery:      tt.battery,
				Calculations: powerkit.IOKitCalculations{HealthByMaxCapacity: tt.health},
			}
			read := false
			fillMissingCapacities(d, func() (batteryCapacities, error) {
				read = true
				return tt.read, tt.readErr
			})

			if read != tt.wantRead {
				t.Errorf("BatteryData read = %v, want %v", read, tt.wantRead)
			}
			if got := d.Battery.DesignCapacity; got != tt.wantDesign {
				t.Errorf("DesignCapacity = %d, want %d", got, tt.wantDesign)
			}
			if got := d.Battery.MaxCapacity; got != tt.wantMax {
				t.Errorf("MaxCapacity = %d, want %d", got, tt.wantMax)
			}
			if got := d.Calculations.HealthByMaxCapacity; got != tt.wantHealth {
				t.Errorf("HealthByMaxCapacity = %d, want %d", got, tt.wantHealth)
			}
		})
	}
}

// TestDarwinBatteryInfoReportsCapacities reads the battery of the Mac running
// the test. It skips on Macs without one, such as CI runners.
func TestDarwinBatteryInfoReportsCapacities(t *testing.T) {
	if c, err := readBatteryDataCapacities(); err != nil || c.Design <= 0 {
		t.Skipf("no battery data on this Mac (design capacity %d mAh, error %v)", c.Design, err)
	}

	info, err := darwinBatteryInfo{}.SystemInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.IOKit == nil {
		t.Fatal("no IOKit data")
	}
	b, health := info.IOKit.Battery, info.IOKit.Calculations.HealthByMaxCapacity
	if b.DesignCapacity <= 0 || b.MaxCapacity <= 0 || health <= 0 {
		t.Fatalf("design capacity %d mAh, full-charge capacity %d mAh, health %d%%; want all positive",
			b.DesignCapacity, b.MaxCapacity, health)
	}
	t.Logf("design capacity %d mAh, full-charge capacity %d mAh, health %d%%", b.DesignCapacity, b.MaxCapacity, health)
}
