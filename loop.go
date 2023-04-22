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
	wg           = &sync.WaitGroup{}
	loopInterval = time.Duration(20) * time.Second
)

func loop() {
	for {
		maintainLoop()
		time.Sleep(loopInterval)
	}
}

func maintainLoop() bool {
	maintainLoopLock.Lock()
	defer maintainLoopLock.Unlock()

	upper := config.Limit
	delta := config.LowerLimitDelta
	lower := upper - delta
	maintain := upper < 100

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

	// If maintain is disabled, we don't care about the battery charge, enable charging anyway.
	if !maintain {
		logrus.Debug("limit set to 100%, maintain loop disabled")
		if !isChargingEnabled {
			logrus.Debug("charging disabled, enabling")
			err = smcConn.EnableCharging()
			if err != nil {
				logrus.Errorf("EnableCharging failed: %v", err)
				return false
			}
		}
		maintainedChargingInProgress = false
		return true
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

	printStatus(batteryCharge, lower, upper, isChargingEnabled, isPluggedIn, maintainedChargingInProgress)

	// batteryCharge < upper - delta &&
	// charging is disabled
	if batteryCharge < lower && !isChargingEnabled {
		logrus.Infof("battery charge %d%% is below %d%% (%d-%d) but charging is disabled, enabling charging",
			batteryCharge,
			lower,
			upper,
			delta,
		)
		err = smcConn.EnableCharging()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %v", err)
			return false
		}
		maintainedChargingInProgress = true
	}

	// batteryCharge >= upper &&
	// charging is enabled
	if batteryCharge >= upper && isChargingEnabled {
		logrus.Infof("battery charge %d%% reached %d%% but charging is enabled, disabling charging",
			batteryCharge,
			upper,
		)
		err = smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return false
		}
		maintainedChargingInProgress = false
	}

	// batteryCharge >= upper - delta && batteryCharge < upper
	// do nothing, keep as-is

	return true
}

func printStatus(batteryCharge int, lower int, upper int, isChargingEnabled bool, isPluggedIn bool, maintainedChargingInProgress bool) {
	logrus.Debugf("batteryCharge=%d, lower=%d, upper=%d, chargingEnabled=%t, isPluggedIn=%t, maintainedChargingInProgress=%t",
		batteryCharge,
		lower,
		upper,
		isChargingEnabled,
		isPluggedIn,
		maintainedChargingInProgress,
	)
}
