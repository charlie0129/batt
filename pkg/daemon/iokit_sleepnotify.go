package daemon

/*
#cgo LDFLAGS: -framework IOKit
#include "hook.h"
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
)

// iokitSleepNotifier registers for IOKit system power notifications and runs
// the CFRunLoop that delivers them. IOKit calls the exported C functions at
// the bottom of this file, which forward to the handler given to Listen.
//
// The handler is a package-level variable because a C callback has no way to
// carry one. Only this file reads or writes it.
type iokitSleepNotifier struct{}

var (
	sleepHandlerMu sync.RWMutex
	sleepHandler   SleepHandler
)

func currentSleepHandler() SleepHandler {
	sleepHandlerMu.RLock()
	defer sleepHandlerMu.RUnlock()
	return sleepHandler
}

func (iokitSleepNotifier) Listen(h SleepHandler) error {
	sleepHandlerMu.Lock()
	sleepHandler = h
	sleepHandlerMu.Unlock()

	slog.Info("registered and listening system sleep notifications")
	if int(C.ListenNotifications()) != 0 {
		return fmt.Errorf("IORegisterForSystemPower failed")
	}
	return nil
}

func (iokitSleepNotifier) Stop() {
	C.StopListeningNotifications()
	sleepHandlerMu.Lock()
	sleepHandler = nil
	sleepHandlerMu.Unlock()
	slog.Info("stopped listening system sleep notifications")
}

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
	h := currentSleepHandler()
	if h == nil || h.CanSystemSleep() {
		C.AllowPowerChange()
		return
	}
	C.CancelPowerChange()
}

//export systemWillSleepCallback
func systemWillSleepCallback() {
	/* The system WILL go to sleep. If you do not call IOAllowPowerChange or
	   IOCancelPowerChange to acknowledge this message, sleep will be
	   delayed by 30 seconds.

	   NOTE: If you call IOCancelPowerChange to deny sleep it returns
	   kIOReturnSuccess, however the system WILL still go to sleep.
	*/
	if h := currentSleepHandler(); h != nil {
		h.SystemWillSleep()
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
	if h := currentSleepHandler(); h != nil {
		h.SystemHasPoweredOn()
	}
}
