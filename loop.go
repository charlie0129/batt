package main

import (
	"reflect"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	maintainedChargingInProgress = false
	maintainLoopLock             = &sync.Mutex{}
	// mg is used to skip several loops when system woke up or before sleep
	wg           = &sync.WaitGroup{}
	loopInterval = time.Duration(10) * time.Second
	loopRecorder = NewMaintainLoopRecorder(60)
)

// MaintainLoopRecorder records the last N maintain loop times.
type MaintainLoopRecorder struct {
	MaxRecordCount        int
	LastMaintainLoopTimes []time.Time
	mu                    *sync.Mutex
}

// NewMaintainLoopRecorder returns a new MaintainLoopRecorder.
func NewMaintainLoopRecorder(maxRecordCount int) *MaintainLoopRecorder {
	return &MaintainLoopRecorder{
		MaxRecordCount:        maxRecordCount,
		LastMaintainLoopTimes: make([]time.Time, 0),
		mu:                    &sync.Mutex{},
	}
}

// AddRecord adds a new record.
func (r *MaintainLoopRecorder) AddRecord() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) >= r.MaxRecordCount {
		r.LastMaintainLoopTimes = r.LastMaintainLoopTimes[1:]
	}
	r.LastMaintainLoopTimes = append(r.LastMaintainLoopTimes, time.Now())
}

// GetRecordsIn returns the number of continuous records in the last duration.
func (r *MaintainLoopRecorder) GetRecordsIn(last time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	// The last record must be within the last duration.
	if len(r.LastMaintainLoopTimes) > 0 && time.Since(r.LastMaintainLoopTimes[len(r.LastMaintainLoopTimes)-1]) >= loopInterval+time.Second {
		return 0
	}

	// Find continuous records from the end of the list.
	// Continuous records are defined as the time difference between
	// two adjacent records is less than loopInterval+1 second.
	count := 0
	for i := len(r.LastMaintainLoopTimes) - 1; i >= 0; i-- {
		record := r.LastMaintainLoopTimes[i]
		if time.Since(record) > last {
			break
		}

		theRecordAfter := record
		if i+1 < len(r.LastMaintainLoopTimes) {
			theRecordAfter = r.LastMaintainLoopTimes[i+1]
		}

		if theRecordAfter.Sub(record) >= loopInterval+time.Second {
			break
		}
		count++
	}

	return count
}

// GetRecordsRelativeToCurrent returns the time differences between the records and the current time.
func (r *MaintainLoopRecorder) GetRecordsRelativeToCurrent(last time.Duration) []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) == 0 {
		return nil
	}

	current := time.Now()
	var records []time.Duration
	for i := len(r.LastMaintainLoopTimes) - 1; i >= 0; i-- {
		record := r.LastMaintainLoopTimes[i]
		if current.Sub(record) > last {
			break
		}
		records = append(records, current.Sub(record))
	}

	return records
}

// GetLastRecord returns the last record.
func (r *MaintainLoopRecorder) GetLastRecord() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) == 0 {
		return time.Time{}
	}

	return r.LastMaintainLoopTimes[len(r.LastMaintainLoopTimes)-1]
}

// infiniteLoop runs forever and maintains the battery charge,
// which is called by the daemon.
func infiniteLoop() {
	for {
		maintainLoop()
		time.Sleep(loopInterval)
	}
}

// maintainLoop maintains the battery charge. It has the logic to
// prevent parallel runs. So if one maintain loop is already running,
// the next one will need to wait until the first one finishes.
func maintainLoop() bool {
	maintainLoopLock.Lock()
	defer maintainLoopLock.Unlock()

	// See wg.Add() in sleepcallback.go for why we need to wait.
	tsBeforeWait := time.Now()
	wg.Wait()
	tsAfterWait := time.Now()
	if tsAfterWait.Sub(tsBeforeWait) > time.Second*1 {
		logrus.Debugf("this maintain loop waited %d seconds after being initiated, now ready to execute", int(tsAfterWait.Sub(tsBeforeWait).Seconds()))
	}

	// TODO: put it in a function (and the similar code in maintainLoopForced)
	maintainLoopCount := loopRecorder.GetRecordsIn(time.Minute * 2)
	expectedMaintainLoopCount := int(time.Minute * 2 / loopInterval)
	minMaintainLoopCount := expectedMaintainLoopCount - 1
	relativeTimes := loopRecorder.GetRecordsRelativeToCurrent(time.Minute * 2)
	// If maintain loop is missed too many times, we assume the system is in a rapid sleep/wake loop, or macOS
	// haven't sent the sleep notification but the system is actually sleep/waking up. In either case, log it.
	if maintainLoopCount < minMaintainLoopCount {
		logrus.WithFields(logrus.Fields{
			"maintainLoopCount":         maintainLoopCount,
			"expectedMaintainLoopCount": expectedMaintainLoopCount,
			"minMaintainLoopCount":      minMaintainLoopCount,
			"relativeTimes":             relativeTimes,
		}).Infof("Possibly missed maintain loop")
	}

	loopRecorder.AddRecord()
	return maintainLoopInner()
}

// maintainLoopForced maintains the battery charge. It runs as soon as
// it is called, without waiting for the previous maintain loop to finish.
// It is mainly called by the HTTP APIs.
func maintainLoopForced() bool {
	return maintainLoopInner()
}

func maintainLoopInner() bool {
	upper := config.Limit
	delta := config.LowerLimitDelta
	lower := upper - delta
	maintain := upper < 100

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
			if config.ControlMagSafeLED {
				batteryCharge, err := smcConn.GetBatteryCharge()
				if err == nil {
					_ = smcConn.SetMagSafeCharging(batteryCharge < 100)
				}
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

	maintainedChargingInProgress = isChargingEnabled && isPluggedIn

	printStatus(batteryCharge, lower, upper, isChargingEnabled, isPluggedIn, maintainedChargingInProgress)

	if batteryCharge < lower && !isChargingEnabled {
		maintainLoopCount := loopRecorder.GetRecordsIn(time.Minute * 2)
		expectedMaintainLoopCount := int(time.Minute * 2 / loopInterval)
		minMaintainLoopCount := expectedMaintainLoopCount - 1
		relativeTimes := loopRecorder.GetRecordsRelativeToCurrent(time.Minute * 2)
		// If maintain loop is missed too many times, we assume the system is in a rapid sleep/wake loop, or macOS
		// haven't sent the sleep notification but the system is actually sleep/waking up. In either case, we should
		// not enable charging, which will cause unexpected charging.
		//
		// This is a workaround for the issue that macOS sometimes doesn't send the sleep notification.
		//
		// We allow at most 1 missed maintain loop.
		if maintainLoopCount < minMaintainLoopCount {
			logrus.WithFields(logrus.Fields{
				"batteryCharge":             batteryCharge,
				"lower":                     lower,
				"upper":                     upper,
				"delta":                     delta,
				"maintainLoopCount":         maintainLoopCount,
				"expectedMaintainLoopCount": expectedMaintainLoopCount,
				"minMaintainLoopCount":      minMaintainLoopCount,
				"relativeTimes":             relativeTimes,
			}).Infof("Battery charge is below lower limit, but too many missed maintain loops are missed. Will wait until maintain loops are stable")
			return true
		}

		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
			"delta":         delta,
		}).Infof("Battery charge is below lower limit, enabling charging")
		err = smcConn.EnableCharging()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = true
		maintainedChargingInProgress = true
	}

	if batteryCharge >= upper && isChargingEnabled {
		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
			"delta":         delta,
		}).Infof("Battery charge is above upper limit, disabling charging")
		err = smcConn.DisableCharging()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = false
		maintainedChargingInProgress = false
	}

	if config.ControlMagSafeLED {
		updateMagSafeLed(isChargingEnabled)
	}

	// batteryCharge >= upper - delta && batteryCharge < upper
	// do nothing, keep as-is

	return true
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
}

var lastStatus loopStatus

func printStatus(
	batteryCharge int,
	lower int,
	upper int,
	isChargingEnabled bool,
	isPluggedIn bool,
	maintainedChargingInProgress bool,
) {
	currentStatus := loopStatus{
		batteryCharge:                batteryCharge,
		lower:                        lower,
		upper:                        upper,
		isChargingEnabled:            isChargingEnabled,
		isPluggedIn:                  isPluggedIn,
		maintainedChargingInProgress: maintainedChargingInProgress,
	}

	fields := logrus.Fields{
		"batteryCharge":                batteryCharge,
		"lower":                        lower,
		"upper":                        upper,
		"chargingEnabled":              isChargingEnabled,
		"isPluggedIn":                  isPluggedIn,
		"maintainedChargingInProgress": maintainedChargingInProgress,
	}

	defer func() { lastPrintTime = time.Now() }()

	// Skip printing if the last print was less than loopInterval+1 seconds ago and everything is the same.
	if time.Since(lastPrintTime) < loopInterval+time.Second && reflect.DeepEqual(lastStatus, currentStatus) {
		logrus.WithFields(fields).Trace("maintain loop status")
		return
	}

	logrus.WithFields(fields).Debug("maintain loop status")

	lastStatus = currentStatus
}
