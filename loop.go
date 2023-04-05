package main

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	maintainedChargingInProgress = false
	maintainLoopLock             = &sync.Mutex{}
	maintainTick                 = time.NewTicker(time.Second * time.Duration(config.LoopIntervalSeconds))
	skipLoop                     = &atomic.Bool{}
)

func mainLoop() {
	for range maintainTick.C {
		maintainLoop()
	}
}

func maintainLoop() bool {
	maintainLoopLock.Lock()
	defer maintainLoopLock.Unlock()

	if skipLoop.Load() {
		logrus.Debugln("maintainLoop skipped")
		return true
	}

	limit := config.Limit
	maintain := limit < 100

	if !maintain {
		logrus.Debugf("maintain disabled")
		maintainedChargingInProgress = false
		return true
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

	logrus.Debugf("batteryCharge=%d, limit=%d, chargingEnabled=%t, isPluggedIn=%t, maintainedChargingInProgress=%t",
		batteryCharge,
		limit,
		isChargingEnabled,
		isPluggedIn,
		maintainedChargingInProgress,
	)

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
