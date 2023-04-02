package main

/*
#cgo LDFLAGS: -framework IOKit
#include "hook.h"
*/
import "C"

import (
	"fmt"

	"github.com/sirupsen/logrus"
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

	checkMaintainedChargingStatus()

	if maintainedChargingInProgress {
		logrus.Debugln("idle sleep is about to kick in, but maintained charging is in progress, deny idle sleep")
		C.CancelPowerChange()
		return
	} else {
		logrus.Debugln("idle sleep is about to kick in, no maintained charging is in progress, allow idle sleep")
		C.AllowPowerChange()
		return
	}
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

	checkMaintainedChargingStatus()

	if maintainedChargingInProgress {
		logrus.Info("system is going to sleep, but maintained charging is in progress, disabling charging just before sleep")
		err := smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %w", err)
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
}

//export systemHasPoweredOnCallback
func systemHasPoweredOnCallback() {
	// System has finished waking up...
	logrus.Traceln("received kIOMessageSystemHasPoweredOn notification")
}

func listenNotifications() error {
	logrus.Info("registered and listening system sleep notifications")
	if int(C.ListenNotifications()) != 0 {
		return fmt.Errorf("IORegisterForSystemPower failed")
	}
	return nil
}
