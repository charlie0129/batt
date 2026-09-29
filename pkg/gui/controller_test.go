package gui

import (
	"testing"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
)

func TestCalibrationAndTemporaryDisableMenuExclusion(t *testing.T) {
	tests := []struct {
		name                    string
		phase                   calibration.Phase
		disableScheduled        bool
		adapterDisableScheduled bool
		wantCalibration         bool
		wantDisableSubmenu      bool
		wantChargeLimits        bool
	}{
		{name: "both available while idle", phase: calibration.PhaseIdle, wantCalibration: true, wantDisableSubmenu: true, wantChargeLimits: true},
		{name: "schedule blocks calibration", phase: calibration.PhaseIdle, disableScheduled: true, wantDisableSubmenu: true, wantChargeLimits: true},
		{name: "adapter schedule blocks calibration", phase: calibration.PhaseIdle, adapterDisableScheduled: true, wantCalibration: false, wantDisableSubmenu: true, wantChargeLimits: true},
		{name: "calibration blocks temporary disable", phase: calibration.PhaseCharge},
		{name: "failed calibration awaiting cancellation blocks temporary disable", phase: calibration.PhaseError},
		{name: "persisted conflict keeps countdown accessible", phase: calibration.PhaseCharge, disableScheduled: true, wantDisableSubmenu: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canStartCalibration(tt.phase, tt.disableScheduled, tt.adapterDisableScheduled); got != tt.wantCalibration {
				t.Errorf("canStartCalibration() = %v, want %v", got, tt.wantCalibration)
			}
			if got := canOpenDisableLimitMenu(tt.phase, tt.disableScheduled); got != tt.wantDisableSubmenu {
				t.Errorf("canOpenDisableLimitMenu() = %v, want %v", got, tt.wantDisableSubmenu)
			}
			if got := canSetChargeLimit(tt.phase); got != tt.wantChargeLimits {
				t.Errorf("canSetChargeLimit() = %v, want %v", got, tt.wantChargeLimits)
			}
			wantForceDischargeSubmenu := tt.phase == calibration.PhaseIdle || tt.adapterDisableScheduled
			if got := canOpenForceDischargeMenu(tt.phase, tt.adapterDisableScheduled); got != wantForceDischargeSubmenu {
				t.Errorf("canOpenForceDischargeMenu() = %v, want %v", got, wantForceDischargeSubmenu)
			}
		})
	}
}

func TestChargeOnceMenuAvailability(t *testing.T) {
	tests := []struct {
		name                    string
		phase                   calibration.Phase
		disableScheduled        bool
		adapterDisableScheduled bool
		chargeOnceTarget        int
		upperLimit              int
		currentCharge           int
		chargeControlMode       compatibility.ChargeControlMode
		adapterControl          bool
		adapterKnown            bool
		adapterEnabled          bool
		wantLimit               bool
		wantFull                bool
	}{
		{
			name: "inside the hysteresis gap", upperLimit: 70, currentCharge: 58,
			wantLimit: true, wantFull: true,
		},
		{
			// Already at the limit: charging to it would do nothing, but the
			// battery can still be topped up to 100%.
			name: "already at the limit", upperLimit: 70, currentCharge: 70,
			wantLimit: false, wantFull: true,
		},
		{
			name: "already full", upperLimit: 70, currentCharge: 100,
			wantLimit: false, wantFull: false,
		},
		{
			name: "batt is not limiting charging", upperLimit: 100, currentCharge: 58,
			wantLimit: false, wantFull: false,
		},
		{
			// Before the first successful config fetch the menu knows no limit.
			name: "limit not known yet", upperLimit: 0, currentCharge: 0,
			wantLimit: false, wantFull: false,
		},
		{
			name: "one-time charge already running", upperLimit: 70, currentCharge: 58,
			chargeOnceTarget: 100, wantLimit: false, wantFull: false,
		},
		{
			name: "calibration owns the charge limit", phase: calibration.PhaseCharge,
			upperLimit: 70, currentCharge: 58, wantLimit: false, wantFull: false,
		},
		{
			name: "temporary disable is scheduled", disableScheduled: true,
			upperLimit: 70, currentCharge: 58, wantLimit: false, wantFull: false,
		},
		{
			// Force discharge cuts the power the one-time charge needs, so the
			// daemon rejects it. The menu must not offer it either.
			name: "force discharge is scheduled", adapterDisableScheduled: true,
			upperLimit: 70, currentCharge: 58, wantLimit: false, wantFull: false,
		},
		{
			name: "adapter control is supported and enabled", upperLimit: 80, currentCharge: 78,
			adapterControl: true, adapterKnown: true, adapterEnabled: true, wantLimit: true, wantFull: true,
		},
		{
			name: "adapter control is supported and disabled", upperLimit: 80, currentCharge: 78,
			adapterControl: true, adapterKnown: true, wantLimit: false, wantFull: false,
		},
		{
			name: "adapter control state is not known", upperLimit: 80, currentCharge: 78,
			adapterControl: true, wantLimit: false, wantFull: false,
		},
		{
			name: "firmware target is reached at target minus one", upperLimit: 80, currentCharge: 79,
			chargeControlMode: compatibility.ChargeControlFirmware, wantLimit: false, wantFull: true,
		},
		{
			name: "native macOS cannot force charging to the limit", upperLimit: 80, currentCharge: 75,
			chargeControlMode: compatibility.ChargeControlNative, wantLimit: false, wantFull: true,
		},
		{
			name: "legacy target is not reached at target minus one", upperLimit: 80, currentCharge: 79,
			chargeControlMode: compatibility.ChargeControlLegacy, wantLimit: true, wantFull: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.phase
			if phase == "" {
				phase = calibration.PhaseIdle
			}

			got := canChargeOnceToLimit(
				phase, tt.disableScheduled, tt.adapterDisableScheduled, tt.chargeOnceTarget,
				tt.upperLimit, tt.currentCharge, tt.chargeControlMode,
				tt.adapterControl, tt.adapterKnown, tt.adapterEnabled,
			)
			if got != tt.wantLimit {
				t.Errorf("canChargeOnceToLimit() = %v, want %v", got, tt.wantLimit)
			}
			got = canChargeOnceToFull(
				phase, tt.disableScheduled, tt.adapterDisableScheduled, tt.chargeOnceTarget,
				tt.upperLimit, tt.currentCharge, tt.adapterControl, tt.adapterKnown, tt.adapterEnabled,
			)
			if got != tt.wantFull {
				t.Errorf("canChargeOnceToFull() = %v, want %v", got, tt.wantFull)
			}
		})
	}
}

func TestForceDischargeMenuAvailability(t *testing.T) {
	tests := []struct {
		name                    string
		phase                   calibration.Phase
		adapterDisableScheduled bool
		chargeOnceTarget        int
		want                    bool
	}{
		{name: "available while idle", phase: calibration.PhaseIdle, want: true},
		{name: "calibration owns the adapter", phase: calibration.PhaseCharge},
		{name: "already scheduled", phase: calibration.PhaseIdle, adapterDisableScheduled: true},
		{
			// The daemon rejects cutting power while a one-time charge runs,
			// because the charge would never finish.
			name: "one-time charge is running", phase: calibration.PhaseIdle, chargeOnceTarget: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canStartForceDischarge(tt.phase, tt.adapterDisableScheduled, tt.chargeOnceTarget); got != tt.want {
				t.Errorf("canStartForceDischarge() = %v, want %v", got, tt.want)
			}
		})
	}
}
