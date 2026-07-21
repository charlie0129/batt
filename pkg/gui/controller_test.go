package gui

import (
	"testing"

	"github.com/charlie0129/batt/pkg/calibration"
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
