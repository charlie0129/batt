package daemon

import (
	"reflect"
	"sync"
	"time"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"
	"github.com/sirupsen/logrus"
)

var (
	maintainedChargingInProgress = false
	maintainLoopLock             = &sync.Mutex{}
	// mg is used to skip several loops when system woke up or before sleep
	wg                      = &sync.WaitGroup{}
	loopInterval            = time.Duration(10) * time.Second
	loopRecorder            = NewTimeSeriesRecorder(60)
	continuousLoopThreshold = 1*time.Minute + 20*time.Second // add 20s to be sure
)

// TimeSeriesRecorder records the last N maintain loop times.
type TimeSeriesRecorder struct {
	MaxRecordCount        int
	LastMaintainLoopTimes []time.Time
	mu                    *sync.Mutex
}

// NewTimeSeriesRecorder returns a new TimeSeriesRecorder.
func NewTimeSeriesRecorder(maxRecordCount int) *TimeSeriesRecorder {
	return &TimeSeriesRecorder{
		MaxRecordCount:        maxRecordCount,
		LastMaintainLoopTimes: make([]time.Time, 0),
		mu:                    &sync.Mutex{},
	}
}

// AddRecordNow adds a new record with the current time.
func (r *TimeSeriesRecorder) AddRecordNow() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) >= r.MaxRecordCount {
		r.LastMaintainLoopTimes = r.LastMaintainLoopTimes[1:]
	}
	// Round to strip monotonic clock reading.
	// This will prevent time.Since from returning values that are not accurate (especially when the system is in sleep mode).
	r.LastMaintainLoopTimes = append(r.LastMaintainLoopTimes, time.Now().Round(0))
}

// ClearRecords clears all records.
func (r *TimeSeriesRecorder) ClearRecords() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.LastMaintainLoopTimes = make([]time.Time, 0)
}

// GetRecords returns the records.
func (r *TimeSeriesRecorder) GetRecords() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.LastMaintainLoopTimes
}

// GetRecordsString returns the records in string format.
func (r *TimeSeriesRecorder) GetRecordsString() []string {
	records := r.GetRecords()
	var recordsString []string
	for _, record := range records {
		recordsString = append(recordsString, record.Format(time.RFC3339))
	}
	return recordsString
}

// AddRecord adds a new record.
func (r *TimeSeriesRecorder) AddRecord(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Strip monotonic clock reading.
	t = t.Round(0)

	if len(r.LastMaintainLoopTimes) >= r.MaxRecordCount {
		r.LastMaintainLoopTimes = r.LastMaintainLoopTimes[1:]
	}
	r.LastMaintainLoopTimes = append(r.LastMaintainLoopTimes, t)
}

// GetRecordsIn returns the number of continuous records in the last duration.
func (r *TimeSeriesRecorder) GetRecordsIn(last time.Duration) int {
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

// GetLastRecords returns the time differences between the records and the current time.
func (r *TimeSeriesRecorder) GetLastRecords(last time.Duration) []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) == 0 {
		return nil
	}

	var records []time.Time
	for i := len(r.LastMaintainLoopTimes) - 1; i >= 0; i-- {
		record := r.LastMaintainLoopTimes[i]
		if time.Since(record) > last {
			break
		}
		records = append(records, record)
	}

	return records
}

//nolint:unused // .
func formatTimes(times []time.Time) []string {
	var timesString []string
	for _, t := range times {
		timesString = append(timesString, t.Format(time.RFC3339))
	}
	return timesString
}

func formatRelativeTimes(times []time.Time) []string {
	var timesString []string
	for _, t := range times {
		timesString = append(timesString, time.Since(t).String())
	}
	return timesString
}

// GetLastRecord returns the last record.
func (r *TimeSeriesRecorder) GetLastRecord() time.Time {
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
func checkMissedMaintainLoops() bool {
	maintainLoopCount := loopRecorder.GetRecordsIn(continuousLoopThreshold)
	expectedMaintainLoopCount := int(continuousLoopThreshold / loopInterval)
	minMaintainLoopCount := expectedMaintainLoopCount - 1
	relativeTimes := loopRecorder.GetLastRecords(continuousLoopThreshold)

	if maintainLoopCount < minMaintainLoopCount {
		logrus.WithFields(logrus.Fields{
			"maintainLoopCount":         maintainLoopCount,
			"expectedMaintainLoopCount": expectedMaintainLoopCount,
			"minMaintainLoopCount":      minMaintainLoopCount,
			"recentRecords":             formatRelativeTimes(relativeTimes),
		}).Infof("Possibly missed maintain loop")
		return true
	}
	return false
}

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

	checkMissedMaintainLoops()

	loopRecorder.AddRecordNow()
	return maintainLoopInner(false)
}

// maintainLoopForced maintains the battery charge. It runs as soon as
// it is called, without waiting for the previous maintain loop to finish.
// It is mainly called by the HTTP APIs.
func maintainLoopForced() bool {
	return maintainLoopInner(true)
}

func handleNoMaintain(isChargingEnabled bool) bool {
	logrus.Debug("limit set to 100%, maintain loop disabled")
	if !isChargingEnabled {
		logrus.Debug("Charging is disabled, enabling")
		err := powerkit.SetChargingState(powerkit.ChargingActionOn)
		if err != nil {
			logrus.Errorf("Enable charging failed: %v", err)
			return false
		}
		if conf.ControlMagSafeLED() {
			sysInfo, err := powerkit.GetSystemInfo()
			if err == nil {
				isCharging := !sysInfo.IOKit.State.FullyCharged
				updateMagSafeLed(isCharging)
			}
		}
	}
	maintainedChargingInProgress = false
	return true
}

func handleChargingLogic(ignoreMissedLoops, isChargingEnabled, isPluggedIn bool, batteryCharge, lower, upper int) bool {

	// Arming Logic (runs regardless of AC power)
	if batteryCharge < lower && !isChargingEnabled {
		// The stability check is only relevant if we are plugged in, as that's when
		// charging would start immediately. We don't want to enable charging right
		// after waking from sleep if the power adapter is connected.
		if isPluggedIn && !ignoreMissedLoops && checkMissedMaintainLoops() {
			logrus.WithFields(logrus.Fields{
				"batteryCharge": batteryCharge,
				"lower":         lower,
				"upper":         upper,
			}).Infof("Battery charge is below lower limit, but too many missed maintain loops are missed. Will wait until maintain loops are stable")
			return true // Bail out and wait for the next loop
		}

		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
		}).Infof("Battery charge is below lower limit, enabling charging.")
		err := powerkit.SetChargingState(powerkit.ChargingActionOn)
		if err != nil {
			logrus.Errorf("Enable charging failed: %v", err)
			return false
		}
		// Update our local state variable to reflect the change for the next steps.
		isChargingEnabled = true
	}

	// Logic to Disable Charging (remains conditional on AC power)
	if isPluggedIn {
		if batteryCharge >= upper && isChargingEnabled {
			logrus.WithFields(logrus.Fields{
				"batteryCharge": batteryCharge,
				"lower":         lower,
				"upper":         upper,
			}).Infof("Battery charge is above upper limit, disabling charging")
			err := powerkit.SetChargingState(powerkit.ChargingActionOff)
			if err != nil {
				logrus.Errorf("EnableChargeInhibit failed: %v", err)
				return false
			}
			// Update local state variable.
			isChargingEnabled = false
		}
	}

	// Final State Update and Logging
	// This now reflects the definitive state after all logic has run.
	maintainedChargingInProgress = isChargingEnabled && isPluggedIn
	printStatus(batteryCharge, lower, upper, isChargingEnabled, isPluggedIn, maintainedChargingInProgress)

	if conf.ControlMagSafeLED() {
		updateMagSafeLed(isChargingEnabled)
	}

	return true

}

func maintainLoopInner(ignoreMissedLoops bool) bool {
	upper := conf.UpperLimit()
	lower := conf.LowerLimit()
	maintain := upper < 100

	sysInfo, err := powerkit.GetSystemInfo()
	if err != nil {
		logrus.Errorf("GetSystemInfo failed: %v", err)
		return false
	}

	batteryCharge := sysInfo.IOKit.Battery.CurrentCharge
	isPluggedIn := sysInfo.IOKit.State.IsConnected
	isChargingEnabled := sysInfo.SMC.State.IsChargingEnabled

	if !maintain {
		return handleNoMaintain(isChargingEnabled)
	}

	return handleChargingLogic(ignoreMissedLoops, isChargingEnabled, isPluggedIn, batteryCharge, lower, upper)
}

func updateMagSafeLed(isChargingEnabled bool) {
	color := powerkit.LEDGreen
	if isChargingEnabled {
		color = powerkit.LEDAmber
	}
	err := powerkit.SetMagsafeLEDColor(color)
	if err != nil {
		logrus.Errorf("SetMagsafeLEDColor failed: %v", err)
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
