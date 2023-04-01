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
	logrus.Debugln("received kIOMessageCanSystemSleep notification")
	C.AllowPowerChange()
}

//export systemWillSleepCallback
func systemWillSleepCallback() {
	logrus.Debugln("received kIOMessageSystemWillSleep notification")
	C.AllowPowerChange()
}

//export systemWillPowerOnCallback
func systemWillPowerOnCallback() {
	logrus.Debugln("received kIOMessageSystemWillPowerOn notification")
}

//export systemHasPoweredOnCallback
func systemHasPoweredOnCallback() {
	logrus.Debugln("received kIOMessageSystemHasPoweredOn notification")
}

func listenNotifications() error {
	logrus.Info("registered to receive system sleep notifications")
	if int(C.ListenNotifications()) != 0 {
		return fmt.Errorf("IORegisterForSystemPower failed")
	}
	return nil
}

func main() {
	logrus.SetLevel(logrus.TraceLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05.000",
		FullTimestamp:   true,
	})

	listenNotifications()
}
