package main

import (
	"time"

	"github.com/sirupsen/logrus"
)

var (
	maintainedChargingInProgress = false
)

func mainLoop() bool {
	defer time.Sleep(time.Second * time.Duration(config.LoopIntervalSeconds))

	return maintainLoop()
}

func checkMaintainedChargingStatus() {
	maintain := config.Limit < 100
	if maintain {
		maintainedChargingInProgress = false
	}

	isChargingEnabled, err := smcConn.IsChargingEnabled()
	if err != nil {
		logrus.Errorf("IsChargingEnabled failed: %w", err)
		return
	}

	isPluggedIn, err := smcConn.IsPluggedIn()
	if err != nil {
		logrus.Errorf("IsPluggedIn failed: %w", err)
		return
	}

	if isChargingEnabled && isPluggedIn {
		maintainedChargingInProgress = true
	} else {
		maintainedChargingInProgress = false
	}
}

func maintainLoop() bool {
	limit := config.Limit
	maintain := limit < 100

	if !maintain {
		logrus.Debugf("maintain disabled")
		maintainedChargingInProgress = false
		return true
	}

	isChargingEnabled, err := smcConn.IsChargingEnabled()
	if err != nil {
		logrus.Errorf("IsChargingEnabled failed: %w", err)
		return false
	}

	batteryCharge, err := smcConn.GetBatteryCharge()
	if err != nil {
		logrus.Errorf("GetBatteryCharge failed: %w", err)
		return false
	}

	isPluggedIn, err := smcConn.IsPluggedIn()
	if err != nil {
		logrus.Errorf("IsPluggedIn failed: %w", err)
		return false
	}

	checkMaintainedChargingStatus()

	logrus.Debugf("batteryCharge=%d, limit=%d, chargingEnabled=%t, isPluggedIn=%t, maintainedChargingInProgress=%t",
		batteryCharge,
		limit,
		isChargingEnabled,
		isPluggedIn,
		maintainedChargingInProgress,
	)

	if batteryCharge < limit && !isChargingEnabled {
		logrus.Infof("battery charge (%d) below limit (%d), enable charging...",
			batteryCharge,
			limit,
		)
		smcConn.EnableCharging()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %w", err)
			return false
		}
		maintainedChargingInProgress = true
	}

	if batteryCharge > limit && isChargingEnabled {
		logrus.Infof("battery charge (%d) above limit (%d), disable charging...",
			batteryCharge,
			limit,
		)
		smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %w", err)
			return false
		}
		maintainedChargingInProgress = false
	}

	return true
}
