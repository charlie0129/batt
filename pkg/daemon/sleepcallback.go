package daemon

/*
#cgo LDFLAGS: -framework IOKit
#include "hook.h"
*/
import "C"

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/smc"
)

var (
	preSleepLoopDelaySeconds  = 60
	postSleepLoopDelaySeconds = 30
)

var (
	lastWakeTime = time.Now()
)

//export canSystemSleepCallback
func canSystemSleepCallback() {
	/* Idle sleep is about to kick in. This message will not be sent for forced sleep.
	   Applications have a chance to prevent sleep by calling IOCancelPowerChange.
	   Most applications should not prevent idle sleep.

	   Power Management waits up to 30 seconds for you to either allow or deny idle
	   sleep. If you don't acknowledge this power change by calling either
	   IOAllowPowerChange or IOCancelPowerChange, the system will wait 30
	   seconds then go to sleep.
	*/
	logrus.Debugln("received kIOMessageCanSystemSleep notification, idle sleep is about to kick in")

	if !conf.PreventIdleSleep() {
		logrus.Debugln("PreventIdleSleep is disabled, allow idle sleep")
		C.AllowPowerChange()
		return
	} else if conf.PreventSystemSleep() {
		logrus.Warningln("prevent-system-sleep is active, no need in prevent-idle-sleep. Please disable it")
		C.AllowPowerChange()
		return
	}

	// We won't allow idle sleep if the system has just waked up,
	// because there may still be a maintain loop waiting (see the wg.Wait() in loop.go).
	// So decisions may not be made yet. We need to wait.
	// Actually, we wait the larger of preSleepLoopDelaySeconds and postSleepLoopDelaySeconds. This is not implemented yet.
	if timeAfterWokenUp := time.Since(lastWakeTime); timeAfterWokenUp < time.Duration(preSleepLoopDelaySeconds)*time.Second {
		logrus.Debugf("system has just waked up (%fs ago), deny idle sleep", timeAfterWokenUp.Seconds())
		C.CancelPowerChange()
		return
	}

	// Run a loop immediately to update `maintainedChargingInProgress` variable.
	maintainLoopInner(false)

	if maintainedChargingInProgress {
		logrus.Debugln("maintained charging is in progress, deny idle sleep")
		C.CancelPowerChange()
		return
	}

	logrus.Debugln("no maintained charging is in progress, allow idle sleep")
	C.AllowPowerChange()
}

//export systemWillSleepCallback
func systemWillSleepCallback() {
	/* The system WILL go to sleep. If you do not call IOAllowPowerChange or
	   IOCancelPowerChange to acknowledge this message, sleep will be
	   delayed by 30 seconds.

	   NOTE: If you call IOCancelPowerChange to deny sleep it returns
	   kIOReturnSuccess, however the system WILL still go to sleep.
	*/
	logrus.Debugln("received kIOMessageSystemWillSleep notification, system will go to sleep")

	if !conf.DisableChargingPreSleep() {
		logrus.Debugln("DisableChargingPreSleep is disabled, allow sleep")
		C.AllowPowerChange()
		return
	} else if conf.PreventSystemSleep() {
		logrus.Warningln("prevent-system-sleep is active, no need in disable-charging-pre-sleep. Please disable it")
		C.AllowPowerChange()
		return
	}

	// If charge limit is enabled (limit<100), no matter if maintained charging is in progress,
	// we disable charging just before sleep.
	// Previously, we only disabled charging if maintained charging was in progress. But we find
	// out this is not required, because if there is no maintained charging in progress, disabling
	// charging will not cause any problem.
	// By always disabling charging before sleep (if charge limit is enabled), we can prevent
	// some rare cases.
	if conf.UpperLimit() < 100 {
		logrus.Infof("charge limit is enabled, disabling charging, and allowing sleep")
		// Delay next loop to prevent charging to be re-enabled after we disabled it.
		// macOS will wait 30s before going to sleep, there is a chance that a maintain loop is
		// executed during that time and it enables charging.
		// So we delay more than that, just to be sure.
		// No need to prevent duplicated runs.
		wg.Add(1)
		go func() {
			// Use sleep instead of time.After because when the computer sleeps, we
			// actually want the sleep to prolong as well.
			sleep(preSleepLoopDelaySeconds)
			wg.Done()
		}()
		err := smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return
		}
		if conf.ControlMagSafeLED() {
			err = smcConn.SetMagSafeLedState(smc.LEDOff)
			if err != nil {
				logrus.Errorf("SetMagSafeLedState failed: %v", err)
			}
		}
	} else {
		logrus.Debugln("no maintained charging is in progress, allow sleep")
	}

	C.AllowPowerChange()
}

//export systemWillPowerOnCallback
func systemWillPowerOnCallback() {
	// System has started the wake-up process...
}

//export systemHasPoweredOnCallback
func systemHasPoweredOnCallback() {
	// System has finished waking up...
	logrus.Debugln("received kIOMessageSystemHasPoweredOn notification, system has finished waking up")
	lastWakeTime = time.Now()

	if conf.UpperLimit() < 100 {
		if conf.PreventSystemSleep() {
			logrus.Debugf("prevent-system-sleep is active, so next loop is not delayed")
			// System will wake up on charger connection for short period of time,
			// so we are checking if battery needs charging, and if so, enabling charging
			// and preventing system from re-entering sleep.
			//
			// This is required only in case laptop discharged below limit during sleep.
			// If charging was already enabled before entering sleep, this will just update mag-safe state.
			maintainLoopForced()
		} else {
			logrus.Debugf("delaying next loop by %d seconds", postSleepLoopDelaySeconds)
			wg.Add(1)
			go func() {
				if conf.DisableChargingPreSleep() && conf.ControlMagSafeLED() {
					err := smcConn.SetMagSafeLedState(smc.LEDOff)
					if err != nil {
						logrus.Errorf("SetMagSafeLedState failed: %v", err)
					}
				}

				// Use sleep instead of time.After because when the computer sleeps, we
				// actually want the sleep to prolong as well.
				sleep(postSleepLoopDelaySeconds)

				wg.Done()
			}()
		}
	}
}

// Use sleep instead of time.After or time.Sleep because when the computer sleeps, we
// actually want the sleep to prolong as well.
func sleep(seconds int) {
	tl := 250 // ms
	t := time.NewTicker(time.Duration(tl) * time.Millisecond)
	ticksWanted := seconds * 1000 / tl
	ticksElapsed := 0
	for range t.C {
		ticksElapsed++
		if ticksElapsed > ticksWanted {
			break
		}
	}
	t.Stop()
}

func listenNotifications() error {
	logrus.Info("registered and listening system sleep notifications")
	if int(C.ListenNotifications()) != 0 {
		return fmt.Errorf("IORegisterForSystemPower failed")
	}
	return nil
}

func stopListeningNotifications() {
	C.StopListeningNotifications()
	logrus.Info("stopped listening system sleep notifications")
}
