//go:build !brew

package main

import (
	"errors"
	"testing"
)

type fakeUninstallHardware struct {
	adapterEnabled bool
	chargeResets   int
	resetErr       error
	enableErr      error
}

func (*fakeUninstallHardware) IsChargingControlCapable() bool { return true }
func (h *fakeUninstallHardware) ResetChargeControl() error {
	h.chargeResets++
	return h.resetErr
}
func (*fakeUninstallHardware) IsAdapterControlCapable() bool { return true }
func (h *fakeUninstallHardware) EnableAdapter() error {
	if h.enableErr != nil {
		return h.enableErr
	}
	h.adapterEnabled = true
	return nil
}

func noSleepRecovery() error { return nil }

func TestUninstallNoResetStillRestoresAdapter(t *testing.T) {
	hardware := &fakeUninstallHardware{}
	if err := restoreUninstallState(hardware, false, noSleepRecovery); err != nil {
		t.Fatal(err)
	}
	if !hardware.adapterEnabled || hardware.chargeResets != 0 {
		t.Fatalf("no-reset must restore wall power without resetting charging: %+v", hardware)
	}

	hardware.enableErr = errors.New("SMC unavailable")
	if err := restoreUninstallState(hardware, false, noSleepRecovery); err == nil {
		t.Fatal("adapter failure must stop uninstall before sleep-state recovery")
	}
}

func TestUninstallRestoresAdapterEvenWhenChargeResetFails(t *testing.T) {
	hardware := &fakeUninstallHardware{resetErr: errors.New("charge keys gated")}
	if err := restoreUninstallState(hardware, true, noSleepRecovery); err == nil {
		t.Fatal("charge reset failure must be reported")
	}
	if !hardware.adapterEnabled {
		t.Fatal("restore wall power even when charge-limit reset fails")
	}
}

func TestUninstallRecoversSleepEvenWhenChargeResetFails(t *testing.T) {
	// Nothing restores SleepDisabled once the daemon is gone, and the README
	// tells users to delete /etc/batt* next. The recovery must not depend on
	// the charge-limit reset.
	hardware := &fakeUninstallHardware{resetErr: errors.New("charge keys gated")}
	recovered := 0

	err := restoreUninstallState(hardware, true, func() error {
		recovered++
		return nil
	})

	if err == nil {
		t.Fatal("charge reset failure must be reported")
	}
	if !hardware.adapterEnabled || recovered != 1 {
		t.Fatalf("wall power and sleep must both be restored: adapterEnabled=%v, recoveries=%d", hardware.adapterEnabled, recovered)
	}
}

func TestUninstallSkipsSleepRecoveryWhenAdapterCannotBeRestored(t *testing.T) {
	hardware := &fakeUninstallHardware{enableErr: errors.New("SMC unavailable")}
	recovered := 0

	err := restoreUninstallState(hardware, true, func() error {
		recovered++
		return nil
	})

	if err == nil {
		t.Fatal("adapter failure must be reported")
	}
	if recovered != 0 {
		t.Fatal("sleep must stay held while wall power is still cut")
	}
}

func TestUninstallReportsSleepRecoveryFailure(t *testing.T) {
	hardware := &fakeUninstallHardware{}

	err := restoreUninstallState(hardware, true, func() error { return errors.New("IOPM unavailable") })

	if err == nil {
		t.Fatal("sleep recovery failure must be reported")
	}
}
