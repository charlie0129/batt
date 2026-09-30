package compatibility

import "testing"

func TestSupportsLimit(t *testing.T) {
	caps := Capabilities{ChargingControl: true}
	if !caps.SupportsLimit(10) || !caps.SupportsLimit(63) || !caps.SupportsLimit(100) {
		t.Fatal("empty SupportedLimits must accept any limit")
	}

	native := Capabilities{ChargingControl: true, ChargeControlMode: ChargeControlNative, SupportedLimits: []int{80, 85, 90, 95, 100}}
	for limit, want := range map[int]bool{60: false, 79: false, 80: true, 85: true, 99: false, 100: true} {
		if got := native.SupportsLimit(limit); got != want {
			t.Errorf("SupportsLimit(%d) = %v, want %v", limit, got, want)
		}
	}
}

func TestNearestSupportedLimit(t *testing.T) {
	native := Capabilities{SupportedLimits: []int{80, 85, 90, 95, 100}}
	for limit, want := range map[int]int{60: 80, 80: 80, 81: 85, 96: 100, 100: 100} {
		if got := native.NearestSupportedLimit(limit); got != want {
			t.Errorf("NearestSupportedLimit(%d) = %d, want %d", limit, got, want)
		}
	}

	// A limit is never rounded down, even when nothing higher is offered.
	low := Capabilities{SupportedLimits: []int{50, 60}}
	if got := low.NearestSupportedLimit(70); got != 100 {
		t.Fatalf("NearestSupportedLimit(70) = %d, want 100", got)
	}
	if got := (Capabilities{}).NearestSupportedLimit(42); got != 42 {
		t.Fatalf("NearestSupportedLimit without a list = %d, want 42", got)
	}
}

func TestLowerLimitFeature(t *testing.T) {
	if !Permissive().Supports(FeatureLowerLimit) {
		t.Fatal("permissive capabilities must keep the lower limit")
	}
	for _, mode := range []ChargeControlMode{ChargeControlLegacy, ChargeControlFirmware} {
		if !(Capabilities{ChargingControl: true, ChargeControlMode: mode}).Supports(FeatureLowerLimit) {
			t.Errorf("%s mode must support the lower limit", mode)
		}
	}
	if (Capabilities{ChargingControl: true, ChargeControlMode: ChargeControlNative}).Supports(FeatureLowerLimit) {
		t.Fatal("native mode must not offer a lower limit")
	}
	if (Capabilities{ChargeControlMode: ChargeControlLegacy}).Supports(FeatureLowerLimit) {
		t.Fatal("lower limit requires charging control")
	}
}

func TestAdapterSleepPolicyCapability(t *testing.T) {
	for _, tt := range []struct {
		name         string
		capabilities Capabilities
		want         bool
	}{
		{"manual adapter", Capabilities{AdapterControl: true}, true},
		{"adapter charge limiting", Capabilities{ChargeControlMode: ChargeControlAdapter}, true},
		{"no adapter", Capabilities{ChargeControlMode: ChargeControlNative}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.capabilities.Supports(FeatureAdapterSleepPolicy); got != tt.want {
				t.Fatalf("Supports(adapter sleep policy) = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestFormatLimits(t *testing.T) {
	for _, tt := range []struct {
		limits []int
		want   string
	}{
		{nil, "10-100%"},
		{[]int{80}, "80%"},
		{[]int{80, 100}, "80% or 100%"},
		{[]int{80, 85, 90, 95, 100}, "80%, 85%, 90%, 95% or 100%"},
	} {
		if got := FormatLimits(tt.limits); got != tt.want {
			t.Errorf("FormatLimits(%v) = %q, want %q", tt.limits, got, tt.want)
		}
	}
}
