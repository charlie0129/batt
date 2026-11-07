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
	// Applying the policy and re-registering PowerUIAgent are asynchronous on
	// macOS. Keep the requested setting even if that registration needs another
	// pass; rolling it back here could re-enable the very policy being removed.
	d.maintainLoopForced()

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
