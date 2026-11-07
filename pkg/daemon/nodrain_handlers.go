package daemon

import (
	"errors"
	"net/http"

	"github.com/charlie0129/batt/pkg/compatibility"
)

func (d *Daemon) getNoDrain(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.getNoDrainStatus())
}

func (d *Daemon) setNoDrain(w http.ResponseWriter, r *http.Request) {
	var enabled bool
	if err := decodeJSON(r, &enabled); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	caps := d.getCapabilities()
	if !caps.ChargingControl || caps.ChargeControlMode != compatibility.ChargeControlNative || !caps.NativeExactTargets {
		d.fail(w, http.StatusConflict, errors.New("no-drain requires native macOS charge control with exact targets; disable adapter mode first if it is enabled"))
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()
	previous := d.conf.NoDrain()
	d.conf.SetNoDrain(enabled)
	if err := d.conf.Save(); err != nil {
		d.conf.SetNoDrain(previous)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	if !d.maintainLoopForced() {
		// A deferred restart is a successful state transition: the preference is
		// saved and the loop will finish it when other apps stop preventing sleep.
		// For a real failure, retain the old setting so the next pass can retry.
		if d.getNoDrainStatus().Phase != noDrainError {
			d.conf.SetNoDrain(previous)
			if saveErr := d.conf.Save(); saveErr != nil {
				d.fail(w, http.StatusInternalServerError, saveErr)
				return
			}
		}
		d.fail(w, http.StatusInternalServerError, errors.New("no-drain setting was saved but could not be applied; the daemon will use regular native charging where possible"))
		return
	}

	state := d.getNoDrainStatus()
	message := "no-drain disabled; regular native charging is restored"
	if enabled {
		message = "no-drain enabled; powerd will hold the ceiling without draining above the limit"
	}
	switch state.Phase {
	case noDrainWaiting:
		message = "no-drain setting saved, but the powerd restart is deferred until other applications stop preventing sleep; regular native charging remains in effect where possible. " + state.Message
	case noDrainError:
		message = "no-drain could not be enabled, so it was turned off and regular native charging was restored. " + state.Message
	}
	writeJSON(w, http.StatusCreated, message)
}
