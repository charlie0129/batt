package main

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	maintainedChargingInProgress = false
	maintainLoopLock             = &sync.Mutex{}
	// mg is used to skip several loops when system woke up or before sleep
	wg = &sync.WaitGroup{}
)

func loop() {
	for {
		maintainLoop()
		time.Sleep(time.Duration(20) * time.Second)
	}
}

func maintainLoop() bool {
	maintainLoopLock.Lock()
	defer maintainLoopLock.Unlock()

	limit := config.Limit
	maintain := limit < 100

	if !maintain {
		logrus.Debugf("maintain disabled")
		maintainedChargingInProgress = false
		return true
	}

	tsBeforeWait := time.Now()
	wg.Wait()
	tsAfterWait := time.Now()
	if tsAfterWait.Sub(tsBeforeWait) > time.Second*1 {
		logrus.Debugf("this maintain loop waited %d seconds after being initiated, now ready to execute", int(tsAfterWait.Sub(tsBeforeWait).Seconds()))
	}

	isChargingEnabled, err := smcConn.IsChargingEnabled()
	if err != nil {
		logrus.Errorf("IsChargingEnabled failed: %v", err)
		return false
	}

	batteryCharge, err := smcConn.GetBatteryCharge()
	if err != nil {
		logrus.Errorf("GetBatteryCharge failed: %v", err)
		return false
	}

	isPluggedIn, err := smcConn.IsPluggedIn()
	if err != nil {
		logrus.Errorf("IsPluggedIn failed: %v", err)
		return false
	}

	if isChargingEnabled && isPluggedIn {
		maintainedChargingInProgress = true
	} else {
		maintainedChargingInProgress = false
	}

	printStatus(batteryCharge, limit, isChargingEnabled, isPluggedIn, maintainedChargingInProgress)

	if batteryCharge < limit && !isChargingEnabled {
		logrus.Infof("battery charge (%d) below limit (%d) but charging is disabled, enabling charging",
			batteryCharge,
			limit,
		)
		err = smcConn.EnableCharging()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %v", err)
			return false
		}
		maintainedChargingInProgress = true
	}

	if batteryCharge > limit && isChargingEnabled {
		logrus.Infof("battery charge (%d) above limit (%d) but charging is enabled, disabling charging",
			batteryCharge,
			limit,
		)
		err = smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return false
		}
		maintainedChargingInProgress = false
	}

	return true
}

var (
	lastBatteryCharge                = -1
	lastLimit                        = -1
	lastIsChargingEnabled            = false
	lastIsPluggedIn                  = false
	lastMaintainedChargingInProgress = false
	lastTriedPrintTime               = time.Now()
)

func printStatus(batteryCharge int, limit int, isChargingEnabled bool, isPluggedIn bool, maintainedChargingInProgress bool) {
	lastTriedPrintTime = time.Now()

	if batteryCharge == lastBatteryCharge &&
		limit == lastLimit &&
		isChargingEnabled == lastIsChargingEnabled &&
		isPluggedIn == lastIsPluggedIn &&
		maintainedChargingInProgress == lastMaintainedChargingInProgress && // All values are the same as last time
		time.Now().Sub(lastTriedPrintTime) < time.Second*30 && // And it's been less than 30 seconds since last tried print
		!logrus.IsLevelEnabled(logrus.TraceLevel) { // Trace level is not enabled. If trace level is enabled, we want to print the status every time.
		// So we don't want to print the status every time.
		return
	}

	lastBatteryCharge = batteryCharge
	lastLimit = limit
	lastIsChargingEnabled = isChargingEnabled
	lastIsPluggedIn = isPluggedIn
	lastMaintainedChargingInProgress = maintainedChargingInProgress

	logrus.Debugf("batteryCharge=%d, limit=%d, chargingEnabled=%t, isPluggedIn=%t, maintainedChargingInProgress=%t",
		batteryCharge,
		limit,
		isChargingEnabled,
		isPluggedIn,
		maintainedChargingInProgress,
	)
}
