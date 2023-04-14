package main

/*
#cgo LDFLAGS: -framework IOKit
#include "hook.h"
*/
import "C"

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	delayNextLoopSeconds = 120
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
	logrus.Traceln("received kIOMessageCanSystemSleep notification")

	if !config.PreventIdleSleep {
		logrus.Debugln("system is going to sleep, but PreventIdleSleep is disabled, nothing to do")
		C.AllowPowerChange()
		return
	}

	maintainLoop()

	if maintainedChargingInProgress {
		logrus.Debugln("idle sleep is about to kick in, but maintained charging is in progress, deny idle sleep")
		C.CancelPowerChange()
		return
	}

	logrus.Debugln("idle sleep is about to kick in, no maintained charging is in progress, allow idle sleep")
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
	logrus.Traceln("received kIOMessageSystemWillSleep notification")

	if !config.DisableChargingPreSleep {
		logrus.Debugln("system is going to sleep, but DisableChargingPreSleep is disabled, nothing to do")
		C.AllowPowerChange()
		return
	}

	// If charge limit is enabled (limit<100), no matter if maintained charging is in progress,
	// we disable charging just before sleep.
	// Previously, we only disabled charging if maintained charging was in progress. But we find
	// out this is not required, because if there is no maintained charging in progress, disabling
	// charging will cause any problem.
	// By always disabling charging before sleep (if charge limit is enabled), we can prevent
	// some rare cases.
	if config.Limit < 100 {
		logrus.Info("system is going to sleep, charge limit is enabled, disabling charging just before sleep")
		// Delay next loop to prevent charging to be re-enabled after we disabled it.
		// macOS will wait 30s before going to sleep, so we delay more than that, just to be sure.
		// no need to prevent duplicated runs.
		logrus.Debugf("delaying next loop by %d seconds", delayNextLoopSeconds)
		wg.Add(1)
		go func() {
			<-time.After(time.Duration(delayNextLoopSeconds) * time.Second)
			wg.Done()
		}()
		err := smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return
		}
	} else {
		logrus.Debugln("system is going to sleep, no maintained charging is in progress, nothing to do")
	}

	C.AllowPowerChange()
}

//export systemWillPowerOnCallback
func systemWillPowerOnCallback() {
	// System has started the wake up process...
	logrus.Traceln("received kIOMessageSystemWillPowerOn notification")

	if config.Limit < 100 {
		logrus.Debugf("system has started the wake up process, delaying next loop by %d seconds", delayNextLoopSeconds)
		wg.Add(1)
		go func() {
			<-time.After(time.Duration(delayNextLoopSeconds) * time.Second)
			wg.Done()
		}()
	}
}

//export systemHasPoweredOnCallback
func systemHasPoweredOnCallback() {
	// System has finished waking up...
	logrus.Traceln("received kIOMessageSystemHasPoweredOn notification")

	if config.Limit < 100 {
		logrus.Debugf("system has finished waking up, delaying next loop by %d seconds", delayNextLoopSeconds)
		wg.Add(1)
		go func() {
			<-time.After(time.Duration(delayNextLoopSeconds) * time.Second)
			wg.Done()
		}()
	}
}

func listenNotifications() error {
	logrus.Info("registered and listening system sleep notifications")
	if int(C.ListenNotifications()) != 0 {
		return fmt.Errorf("IORegisterForSystemPower failed")
	}
	return nil
}
