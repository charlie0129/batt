package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/charlie0129/batt/pkg/config"
)

// This file is the daemon's reaction to system sleep and wake. The cgo bridge
// that receives the IOKit notifications is in iokit_sleepnotify.go; everything
// here is plain Go so it can be tested against a simulated Mac.

// daemonSleepHandler is the SleepHandler the notifier calls.
type daemonSleepHandler struct{ d *Daemon }

func (h daemonSleepHandler) CanSystemSleep() bool { return h.d.canSystemSleep() }
func (h daemonSleepHandler) SystemWillSleep()     { h.d.systemWillSleep() }
func (h daemonSleepHandler) SystemHasPoweredOn()  { h.d.systemHasPoweredOn() }

// canSystemSleep decides whether idle sleep may start. It is only consulted
// for idle sleep; macOS does not ask before forced sleep.
func (d *Daemon) canSystemSleep() bool {
	slog.Debug("received kIOMessageCanSystemSleep notification, idle sleep is about to kick in")
	if !d.usesActiveChargeControl() {
		return true
	}

	if !d.conf.PreventIdleSleep() {
		slog.Debug("PreventIdleSleep is disabled, allow idle sleep")
		return true
	} else if d.conf.PreventSystemSleep() {
		slog.Warn("prevent-system-sleep is active, no need in prevent-idle-sleep. Please disable it")
		return true
	}

	// We won't allow idle sleep if the system has just waked up,
	// because there may still be a maintain loop waiting (see loopDelay in loop.go).
	// So decisions may not be made yet. We need to wait.
	// Actually, we wait the larger of preSleepLoopDelay and postSleepLoopDelay. This is not implemented yet.
	if timeAfterWokenUp := time.Since(d.lastWakeTime); timeAfterWokenUp < d.preSleepLoopDelay {
		slog.Debug(fmt.Sprintf("system has just waked up (%fs ago), deny idle sleep", timeAfterWokenUp.Seconds()))
		return false
	}

	// Run a loop immediately to update `maintainedChargingInProgress`.
	d.maintainLoopInner(false)

	if d.maintainedCharging() {
		slog.Debug("maintained charging is in progress, deny idle sleep")
		return false
	}

	slog.Debug("no maintained charging is in progress, allow idle sleep")
	return true
}

// systemWillSleep runs right before the system sleeps. Sleep cannot be
// prevented here; the daemon only disables charging first when configured to.
func (d *Daemon) systemWillSleep() {
	slog.Debug("received kIOMessageSystemWillSleep notification, system will go to sleep")
	caps, ch := d.loadChargeControl()
	if !modeUsesActiveChargeControl(caps.ChargeControlMode) {
		return
	}

	if !d.conf.DisableChargingPreSleep() {
		slog.Debug("DisableChargingPreSleep is disabled, allow sleep")
		return
	} else if d.conf.PreventSystemSleep() {
		slog.Warn("prevent-system-sleep is active, no need in disable-charging-pre-sleep. Please disable it")
		return
	}

	// If charge limit is enabled (limit<100), no matter if maintained charging is in progress,
	// we disable charging just before sleep.
	// Previously, we only disabled charging if maintained charging was in progress. But we find
	// out this is not required, because if there is no maintained charging in progress, disabling
	// charging will not cause any problem.
	// By always disabling charging before sleep (if charge limit is enabled), we can prevent
	// some rare cases.
	if d.conf.UpperLimit() >= 100 {
		slog.Debug("no maintained charging is in progress, allow sleep")
		return
	}

	slog.Info("charge limit is enabled, disabling charging, and allowing sleep")
	// Delay next loop to prevent charging to be re-enabled after we disabled it.
	// macOS will wait 30s before going to sleep, there is a chance that a maintain loop is
	// executed during that time and it enables charging.
	// So we delay more than that, just to be sure.
	// No need to prevent duplicated runs.
	d.holdMaintainLoop(d.preSleepLoopDelay, nil)
	if err := ch.Disable(); err != nil {
		slog.Error("pre-sleep charger.Disable failed", "error", err)
		return
	}
	if d.conf.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
		if err := d.smc.DisableMagSafeLed(); err != nil {
			slog.Error("DisableMagSafeLed failed", "error", err)
		}
	}
}

// systemHasPoweredOn runs once the system has finished waking up.
func (d *Daemon) systemHasPoweredOn() {
	slog.Debug("received kIOMessageSystemHasPoweredOn notification, system has finished waking up")
	if !d.usesActiveChargeControl() {
		return
	}
	d.lastWakeTime = time.Now()

	d.scheduler.HandleWakeUp()

	if d.conf.UpperLimit() >= 100 {
		return
	}
	if d.conf.PreventSystemSleep() {
		slog.Debug("prevent-system-sleep is active, so next loop is not delayed")
		// System will wake up on charger connection for short period of time,
		// so we are checking if battery needs charging, and if so, enabling charging
		// and preventing system from re-entering sleep.
		//
		// This is required only in case laptop discharged below limit during sleep.
		// If charging was already enabled before entering sleep, this will just update mag-safe state.
		d.maintainLoopForced()
		return
	}

	slog.Debug(fmt.Sprintf("delaying next loop by %s", d.postSleepLoopDelay))
	d.holdMaintainLoop(d.postSleepLoopDelay, func() {
		if d.conf.DisableChargingPreSleep() && d.conf.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
			if err := d.smc.DisableMagSafeLed(); err != nil {
				slog.Error("DisableMagSafeLed failed", "error", err)
			}
		}
	})
}

// holdMaintainLoop keeps the maintain loop from running for wait, after
// running first. Holds overlap freely; the loop waits for the last one.
func (d *Daemon) holdMaintainLoop(wait time.Duration, first func()) {
	d.loopDelay.Add(1)
	go func() {
		defer d.loopDelay.Done()
		if first != nil {
			first()
		}
		sleepWallClock(wait)
	}()
}

// sleepWallClock waits for d of wall-clock time. Unlike time.Sleep or
// time.After it counts ticks, so when the computer sleeps the wait is
// prolonged by the time spent asleep instead of expiring at once on wake.
func sleepWallClock(d time.Duration) {
	if d <= 0 {
		return
	}
	tick := 250 * time.Millisecond
	if d < tick {
		tick = d
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for elapsed := time.Duration(0); elapsed < d; elapsed += tick {
		<-t.C
	}
}
