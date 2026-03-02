package daemon

import (
	"reflect"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

var (
	maintainedChargingInProgress = false
	maintainLoopInnerLock        = &sync.Mutex{}
	// wg is used to skip several loops when system woke up or before sleep
	wg                      = &sync.WaitGroup{}
	loopInterval            = time.Duration(10) * time.Second
	loopRecorder            = NewTimeSeriesRecorder(60)
	continuousLoopThreshold = 1*time.Minute + 20*time.Second // add 20s to be sure
)

// infiniteLoop runs forever and maintains the battery charge,
// which is called by the daemon.
func infiniteLoop() {
	for {
		maintainLoop()
		time.Sleep(loopInterval)
	}
}

// checkMissedMaintainLoops checks if there are too many missed maintain loops,
// which could indicate that the system was in sleep mode or there is some issue
// with the maintain loop execution.
// It returns true if there are too many missed loops.
func checkMissedMaintainLoops(logStatus bool) bool {
	maintainLoopCount := loopRecorder.GetRecordsIn(continuousLoopThreshold)
	expectedMaintainLoopCount := int(continuousLoopThreshold / loopInterval)
	minMaintainLoopCount := expectedMaintainLoopCount - 1
	relativeTimes := loopRecorder.GetLastRecords(continuousLoopThreshold)

	if maintainLoopCount < minMaintainLoopCount {
		if logStatus {
			logrus.WithFields(logrus.Fields{
				"maintainLoopCount":         maintainLoopCount,
				"expectedMaintainLoopCount": expectedMaintainLoopCount,
				"minMaintainLoopCount":      minMaintainLoopCount,
				"recentRecords":             formatRelativeTimes(relativeTimes),
			}).Infof("Possibly missed maintain loop")
		}

		return true
	}

	// Another loopInterval is added to the threshold so we can get
	// loop counts greater than minMaintainLoopCount, so we only print
	// once when the maintain loop is stabilized, instead of printing
	// every time when maintainLoopCount == minMaintainLoopCount (always
	// this case if using maintainLoopCount), which could be very spammy.
	if loopRecorder.GetRecordsIn(continuousLoopThreshold+loopInterval) == minMaintainLoopCount {
		if logStatus {
			logrus.WithFields(logrus.Fields{
				"maintainLoopCount":         maintainLoopCount,
				"expectedMaintainLoopCount": expectedMaintainLoopCount,
				"minMaintainLoopCount":      minMaintainLoopCount,
				"recentRecords":             formatRelativeTimes(relativeTimes),
			}).Infof("Maintain loop has been stabilized")
		}
	}

	return false
}

// maintainLoop maintains the battery charge. It has the logic to
// prevent parallel runs. So if one maintain loop is already running,
// the next one will need to wait until the first one finishes.
func maintainLoop() bool {
	defer loopRecorder.AddRecordNow()

	if conf.PreventSystemSleep() {
		// No need to keep track missed loops and wait post/before sleep delays, since
		// prevent-system-sleep would prevent unexpected sleep during charging.
		return maintainLoopInner(true)
	}

	// See wg.Add() in sleepcallback.go for why we need to wait.
	tsBeforeWait := time.Now()
	wg.Wait()
	tsAfterWait := time.Now()
	if tsAfterWait.Sub(tsBeforeWait) > time.Second*1 {
		logrus.Debugf("this maintain loop waited %d seconds after being initiated, now ready to execute", int(tsAfterWait.Sub(tsBeforeWait).Seconds()))
	}

	// just log status, not doing anything, yet
	_ = checkMissedMaintainLoops(true)

	return maintainLoopInner(false)
}

// maintainLoopForced maintains the battery charge. It runs without waiting
// for post/pre sleep delays, but yet has logic to prevent parallel runs.
// It is mainly called by the HTTP APIs.
func maintainLoopForced() bool {
	return maintainLoopInner(true)
}

func handleNoMaintain(isChargingEnabled bool) bool {
	if !isChargingEnabled {
		logrus.Debug("limit set to 100%, but charging is disabled, enabling")
		err := smcConn.EnableCharging()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %v", err)
			return false
		}

		switch conf.ControlMagSafeLED() {
		case config.ControlMagSafeModeAlwaysOff:
			err := smcConn.DisableMagSafeLed()
			if err != nil {
				// no fail
				logrus.Errorf("DisableMagSafeLed failed: %v", err)
			}
		default:
			// Reset MagSafe LED to system state.
			err = smcConn.SetMagSafeLedState(smc.LEDSystem)
			if err != nil {
				// no fail
				logrus.Errorf("SetMagSafeLedState(LEDSystem) failed: %v", err)
			}
		}
	}

	// Set MagSafe LED according to config.
	currentMagSafeLEDState, err := smcConn.GetMagSafeLedState()
	if err != nil {
		logrus.Errorf("GetMagSafeLedState failed: %v", err)
	}
	switch conf.ControlMagSafeLED() {
	case config.ControlMagSafeModeAlwaysOff:
		if currentMagSafeLEDState != smc.LEDOff {
			err := smcConn.DisableMagSafeLed()
			if err != nil {
				// no fail
				logrus.Errorf("DisableMagSafeLed failed: %v", err)
			}
		}
	default:
		// This applies to both ControlMagSafeModeEnabled and ControlMagSafeModeDisabled
		// modes. Because in Disabled mode we should not interfere with the LED state,
		// in Enabled mode we want to show the system state (which is the same as
		// apple's default behavior when limit=100%).
		if currentMagSafeLEDState != smc.LEDSystem {
			err := smcConn.SetMagSafeLedState(smc.LEDSystem)
			if err != nil {
				// no fail
				logrus.Errorf("SetMagSafeLedState(LEDSystem) failed: %v", err)
			}
		}
	}

	// Calling this multiple times is no-op.
	err = AllowSleepOnAC()
	if err != nil {
		logrus.Errorf("AllowSleepOnAC failed: %v", err)
	}

	cancelCalibrationNoRestoreNoError()

	maintainedChargingInProgress = false
	return true
}

func handleChargingLogic(ignoreMissedLoops, isChargingEnabled, isPluggedIn bool, batteryCharge, lower, upper int) bool {
	maintainLoopsMissed := checkMissedMaintainLoops(false)

	// Fix for #123.
	// Consider this case:
	//   1. charging is enabled (batteryCharge < lower)
	//   2. batt execution is interrupted constantly (maintain loop is executed with unexpected interval), the maintain loop is missed for some reason (macOS didn't send a sleep notification)
	//   3. batt could not disable charging in-time because it's constantly interrupted, and the battery keeps charging, which could cause overcharging.
	//
	// In this case, we can stop charging immediately when we detect that
	// there are too many missed maintain loops, even if the battery charge is
	// below the lower limit, to prevent overcharging.
	if isChargingEnabled && maintainLoopsMissed {
		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
		}).Infof("Too many missed maintain loops detected while charging is enabled. Disabling charging to prevent overcharging.")
		err := smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = false
		maintainedChargingInProgress = false
	}

	// Should enable charging.
	if batteryCharge < lower && !isChargingEnabled {
		// If there are too many missed maintain loops, it could indicate that
		// the system was in sleep mode, or macOS interrupted executing the
		// maintain loop for some reason, or system has just woken up.
		// In this case, we should wait until the maintain loops are stable
		// before enabling charging, to avoid enabling charging when the
		// system is in sleep mode, which could overcharge.
		if !ignoreMissedLoops && maintainLoopsMissed {
			logrus.WithFields(logrus.Fields{
				"batteryCharge": batteryCharge,
				"lower":         lower,
				"upper":         upper,
			}).Infof("Battery charge is below lower limit, but too many missed maintain loops are missed. Will wait until maintain loops are stable")
			return true
		}

		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
		}).Infof("Battery charge is below lower limit, enabling charging")
		err := smcConn.EnableCharging()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = true
		maintainedChargingInProgress = true
	}

	// Should disable charging.
	if batteryCharge >= upper && isChargingEnabled {
		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
		}).Infof("Battery charge is above upper limit, disabling charging")
		err := smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = false
		maintainedChargingInProgress = false
	}

	switch conf.ControlMagSafeLED() {
	case config.ControlMagSafeModeAlwaysOff:
		_ = smcConn.DisableMagSafeLed()
	case config.ControlMagSafeModeEnabled:
		updateMagSafeLed(isChargingEnabled)
	default:
		// nothing
	}

	if conf.PreventSystemSleep() {
		if isChargingEnabled {
			err := PreventSleepOnAC()
			if err != nil {
				logrus.Errorf("PreventSleepOnAC failed: %v", err)
			}
		} else {
			err := AllowSleepOnAC()
			if err != nil {
				logrus.Errorf("AllowSleepOnAC failed: %v", err)
			}
		}
	}

	// batteryCharge >= upper - delta && batteryCharge < upper
	// do nothing, keep as-is

	return true
}

func maintainLoopInner(ignoreMissedLoops bool) bool {
	maintainLoopInnerLock.Lock()
	defer maintainLoopInnerLock.Unlock()

	upper := conf.UpperLimit()
	lower := conf.LowerLimit()
	maintain := upper < 100

	isChargingEnabled, err := smcConn.IsChargingEnabled()
	if err != nil {
		logrus.Errorf("IsChargingEnabled failed: %v", err)
		return false
	}

	// Always get current battery charge to possibly drive calibration first.
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

	maintainedChargingInProgress = isChargingEnabled && isPluggedIn && calibrationState.Phase == calibration.PhaseIdle
	printStatus(batteryCharge, lower, upper, isChargingEnabled, isPluggedIn, maintainedChargingInProgress, calibrationState.Phase != calibration.PhaseIdle)

	// If calibration is active, advance it and skip normal maintain logic.
	if applyCalibrationWithinLoop(batteryCharge) {
		switch conf.ControlMagSafeLED() {
		case config.ControlMagSafeModeAlwaysOff:
			_ = smcConn.DisableMagSafeLed()
		case config.ControlMagSafeModeEnabled:
			updateMagSafeLed(isChargingEnabled)
		default:
			// nothing
		}
		return true
	}

	// If maintain is disabled, we don't care about the battery charge, enable charging anyway.
	if !maintain {
		return handleNoMaintain(isChargingEnabled)
	}

	return handleChargingLogic(ignoreMissedLoops, isChargingEnabled, isPluggedIn, batteryCharge, lower, upper)
}

func updateMagSafeLed(isChargingEnabled bool) {
	err := smcConn.SetMagSafeCharging(isChargingEnabled)
	if err != nil {
		logrus.Errorf("SetMagSafeCharging failed: %v", err)
	}
}

var lastPrintTime time.Time

type loopStatus struct {
	batteryCharge                int
	lower                        int
	upper                        int
	isChargingEnabled            bool
	isPluggedIn                  bool
	maintainedChargingInProgress bool
	calibrationInProgress        bool
}

var lastStatus loopStatus

func printStatus(
	batteryCharge int,
	lower int,
	upper int,
	isChargingEnabled bool,
	isPluggedIn bool,
	maintainedChargingInProgress bool,
	calibrationInProgress bool,
) {
	currentStatus := loopStatus{
		batteryCharge:                batteryCharge,
		lower:                        lower,
		upper:                        upper,
		isChargingEnabled:            isChargingEnabled,
		isPluggedIn:                  isPluggedIn,
		maintainedChargingInProgress: maintainedChargingInProgress,
		calibrationInProgress:        calibrationInProgress,
	}

	fields := logrus.Fields{
		"batteryCharge":                batteryCharge,
		"lower":                        lower,
		"upper":                        upper,
		"chargingEnabled":              isChargingEnabled,
		"isPluggedIn":                  isPluggedIn,
		"maintainedChargingInProgress": maintainedChargingInProgress,
		"calibrationInProgress":        calibrationInProgress,
	}

	defer func() { lastPrintTime = time.Now() }()

	// Skip printing if the last print was less than loopInterval+1 seconds ago and everything is the same.
	if time.Since(lastPrintTime) < loopInterval+time.Second && reflect.DeepEqual(lastStatus, currentStatus) {
		logrus.WithFields(fields).Trace("status")
		return
	}

	logrus.WithFields(fields).Debug("status")

	lastStatus = currentStatus
}
