package daemon

import (
	"math"
	"testing"
)

func TestBatterySnapshotPower(t *testing.T) {
	tests := []struct {
		name                          string
		snapshot                      BatterySnapshot
		wantAdapter, wantBattery, sys float64
	}{
		{
			name:     "charging",
			snapshot: BatterySnapshot{Voltage: 12, Amperage: 2, InputVoltage: 20, InputAmperage: 3},
			// 60 W from the adapter, 24 W of it into the battery.
			wantAdapter: 60, wantBattery: 24, sys: 36,
		},
		{
			name:     "on battery",
			snapshot: BatterySnapshot{Voltage: 12, Amperage: -1.5},
			// Everything the Mac uses comes out of the battery.
			wantBattery: -18, sys: 18,
		},
		{
			name:     "on AC while macOS drains the battery to its limit",
			snapshot: BatterySnapshot{Voltage: 12.26, Amperage: -0.284, InputVoltage: 20.152, InputAmperage: 0.03},
			// The adapter and the battery both supply the Mac.
			wantAdapter: 0.60456, wantBattery: -3.48184, sys: 4.0864,
		},
		{
			name:     "adapter readings lag behind the battery",
			snapshot: BatterySnapshot{Voltage: 12, Amperage: 2, InputVoltage: 20, InputAmperage: 1},
			// The battery appears to take more than the adapter supplies.
			wantAdapter: 20, wantBattery: 24, sys: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.snapshot
			for _, c := range []struct {
				name      string
				got, want float64
			}{
				{"AdapterPower", s.AdapterPower(), tt.wantAdapter},
				{"BatteryPower", s.BatteryPower(), tt.wantBattery},
				{"SystemPower", s.SystemPower(), tt.sys},
			} {
				if math.Abs(c.got-c.want) > 1e-9 {
					t.Errorf("%s() = %v W, want %v W", c.name, c.got, c.want)
				}
			}
		})
	}
}

func TestBatterySnapshotHealth(t *testing.T) {
	tests := []struct {
		design, max int
		want        int
	}{
		{design: 6075, max: 5752, want: 95},
		{design: 8694, max: 8573, want: 99},
		{design: 6000, max: 5500, want: 92},
		{design: 5000, max: 5100, want: 102},
		{design: 0, max: 5752, want: 0},
	}
	for _, tt := range tests {
		s := BatterySnapshot{DesignCapacity: tt.design, MaxCapacity: tt.max}
		if got := s.Health(); got != tt.want {
			t.Errorf("Health() with %d of %d mAh = %d%%, want %d%%", tt.max, tt.design, got, tt.want)
		}
	}
}
