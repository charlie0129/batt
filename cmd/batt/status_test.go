package main

import (
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/utils/ptr"
)

// statusConfig builds the client-side config view the status command works on.
// A chargeOnceTarget of 0 means no one-time charge is running.
func statusConfig(limit, lowerLimitDelta, chargeOnceTarget int) *config.File {
	raw := &config.RawFileConfig{
		Limit:           ptr.To(limit),
		LowerLimitDelta: ptr.To(lowerLimitDelta),
	}
	if chargeOnceTarget > 0 {
		raw.ChargeOnceTarget = ptr.To(chargeOnceTarget)
	}
	return config.NewFileFromConfig(raw, "")
}

// chargingBattery charges at 1000 mA with 5000 mAh of capacity, so one percent
// takes three minutes.
func chargingBattery() *powerinfo.Battery {
	return &powerinfo.Battery{
		State:          powerinfo.Charging,
		DesignCapacity: 5000,
		MaxCapacity:    5000,
		ChargeRate:     10000,
		DesignVoltage:  10,
	}
}

func TestComputeTimeToLimitFollowsTheOneTimeCharge(t *testing.T) {
	tests := []struct {
		name             string
		limit            int
		chargeOnceTarget int
		currentCharge    int
		want             *int
	}{
		{name: "towards the configured limit", limit: 80, currentCharge: 70, want: ptr.To(30)},
		{
			// Without this, the estimate named the 80% limit while the battery
			// was on its way to 100%.
			name: "towards a one-time charge to full", limit: 80, chargeOnceTarget: 100, currentCharge: 70, want: ptr.To(90),
		},
		{
			// A one-time charge to full runs past the configured limit, where
			// the limit-based estimate used to stop reporting anything.
			name: "past the configured limit", limit: 80, chargeOnceTarget: 100, currentCharge: 90, want: ptr.To(30),
		},
		{name: "one-time charge to the limit", limit: 80, chargeOnceTarget: 80, currentCharge: 70, want: ptr.To(30)},
		{name: "target already reached", limit: 80, chargeOnceTarget: 80, currentCharge: 80},
		{name: "batt is not limiting charging", limit: 100, currentCharge: 70},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &statusData{currentCharge: tt.currentCharge, batteryInfo: chargingBattery()}
			got := computeTimeToLimit(data, statusConfig(tt.limit, 2, tt.chargeOnceTarget))

			// The estimate truncates a percentage converted through floating
			// point, so allow the minute it can lose. The test is about which
			// target the estimate aims at.
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("computeTimeToLimit() = %d minutes, want none", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("computeTimeToLimit() = none, want %d minutes", *tt.want)
			case tt.want != nil && (*got < *tt.want-1 || *got > *tt.want):
				t.Fatalf("computeTimeToLimit() = %d minutes, want %d", *got, *tt.want)
			}
		})
	}
}

func TestChargeTarget(t *testing.T) {
	tests := []struct {
		name             string
		limit            int
		chargeOnceTarget int
		want             int
		wantOK           bool
	}{
		{name: "the configured limit", limit: 80, want: 80, wantOK: true},
		{name: "a one-time charge overrides it", limit: 80, chargeOnceTarget: 100, want: 100, wantOK: true},
		{name: "nothing to steer towards", limit: 100, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := chargeTarget(statusConfig(tt.limit, 2, tt.chargeOnceTarget))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("chargeTarget() = %d, %v, want %d, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestChargingNarrationDuringAOneTimeCharge(t *testing.T) {
	// 79% inside a 78-80% band: the charge sits above the lower limit, which is
	// exactly what the persistent explanation talks about. A one-time charge
	// ignores that band, so repeating the explanation would contradict it.
	data := &statusData{currentCharge: 79, pluggedIn: true, adapter: true, batteryInfo: chargingBattery()}

	persistent := chargingNarration(data, statusConfig(80, 2, 0))
	if !strings.Contains(persistent, "drops below the lower limit") {
		t.Fatalf("persistent narration lost its explanation: %q", persistent)
	}

	oneTime := chargingNarration(data, statusConfig(80, 2, 100))
	if strings.Contains(oneTime, "lower limit") {
		t.Fatalf("one-time narration still explains the persistent band: %q", oneTime)
	}
	if !strings.Contains(oneTime, "100%") {
		t.Fatalf("one-time narration does not name the target: %q", oneTime)
	}
}

func TestChargingNarration(t *testing.T) {
	tests := []struct {
		name             string
		charging         bool
		pluggedIn        bool
		adapter          bool
		currentCharge    int
		limit            int
		chargeOnceTarget int
		adapterControl   bool
		want             string
	}{
		{
			name: "charging while plugged in", charging: true, pluggedIn: true, adapter: true, currentCharge: 70, limit: 80,
			want: "Your Mac will charge.",
		},
		{
			name: "charging without a plug", charging: true, currentCharge: 70, limit: 80,
			want: "Your Mac will charge, but you are not plugged in yet.",
		},
		{
			name: "above the limit", pluggedIn: true, adapter: true, currentCharge: 85, limit: 80,
			want: "Your Mac will not charge, because your current charge is above the limit.",
		},
		{
			name: "inside the hysteresis gap", pluggedIn: true, adapter: true, currentCharge: 79, limit: 80,
			want: "Your Mac will not charge, because your current charge is above the lower limit. Charging will be allowed after current charge drops below the lower limit.",
		},
		{
			name: "below the lower limit with the adapter off", pluggedIn: true, currentCharge: 50, limit: 80, adapterControl: true,
			want: "Your Mac will not charge, because adapter is disabled.",
		},
		{
			name: "below the lower limit without adapter control", pluggedIn: true, currentCharge: 50, limit: 80,
			want: "Your Mac will not charge",
		},
		{
			name: "batt is not limiting charging", pluggedIn: true, adapter: true, currentCharge: 50, limit: 100,
			want: "",
		},
		{
			name: "a one-time charge has not started yet", pluggedIn: true, adapter: true, currentCharge: 79, limit: 80, chargeOnceTarget: 100,
			want: "Your Mac will charge to 100% once, starting with the next refresh.",
		},
		{
			name: "a one-time charge against a disabled adapter", pluggedIn: true, currentCharge: 50, limit: 80, chargeOnceTarget: 100, adapterControl: true,
			want: "Your Mac will not charge to its 100% one-time target, because adapter is disabled.",
		},
		{
			name: "a one-time charge without adapter control", pluggedIn: true, currentCharge: 50, limit: 80, chargeOnceTarget: 100,
			want: "Your Mac will charge to 100% once, starting with the next refresh.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &statusData{
				charging:      tt.charging,
				pluggedIn:     tt.pluggedIn,
				adapter:       tt.adapter,
				currentCharge: tt.currentCharge,
				batteryInfo:   chargingBattery(),
				capabilities:  compatibility.Capabilities{AdapterControl: tt.adapterControl},
			}
			if got := chargingNarration(data, statusConfig(tt.limit, 2, tt.chargeOnceTarget)); got != tt.want {
				t.Fatalf("chargingNarration() = %q, want %q", got, tt.want)
			}
		})
	}
}
