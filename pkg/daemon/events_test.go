package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/events"
)

// recvEvent waits for the next event on ch.
func recvEvent(t *testing.T, ch chan events.Event) events.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("no event published")
		return events.Event{}
	}
}

func TestScheduledCalibrationStartsWithTheConfiguredSettings(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCharge(40), withCharging(false))
	f.mem.SetCalibrationDischargeThreshold(25)
	f.mem.SetCalibrationHoldDurationMinutes(45)

	if err := f.d.runScheduledCalibration(); err != nil {
		t.Fatal(err)
	}
	if st := f.calibration(); st.Phase != calibration.PhaseDischarge || st.Threshold != 25 || st.HoldMinutes != 45 {
		t.Fatalf("scheduled calibration = %+v, want discharge to 25%% with a 45 minute hold", st)
	}
}

func TestScheduleNotificationsArePublished(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	ch := f.d.hub.Subscribe()
	defer f.d.hub.Unsubscribe(ch)

	runAt := time.Date(2026, time.November, 7, 10, 0, 0, 0, time.Local)
	f.d.notifyScheduleUpcoming(runAt)
	ev := recvEvent(t, ch)
	upcoming, err := events.DecodeAs[events.CalibrationActionEvent](ev)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Name != events.CalibrationAction || upcoming.Action != string(calibration.ActionScheduleUpComing) {
		t.Fatalf("event = %s %+v", ev.Name, upcoming)
	}

	f.d.notifyScheduleError(errors.New("the Mac must be plugged in"))
	ev = recvEvent(t, ch)
	failed, err := events.DecodeAs[events.CalibrationActionEvent](ev)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Action != string(calibration.ActionScheduleError) || failed.Message != "the Mac must be plugged in" {
		t.Fatalf("event = %+v", failed)
	}
}

func TestChargeOnceEventsArePublished(t *testing.T) {
	f := newChargeOnceFixture(t, chargeOnceConf(70, 40), 58, calibration.PhaseIdle)
	ch := f.d.hub.Subscribe()
	defer f.d.hub.Unsubscribe(ch)

	f.mustServe("POST", chargeOnceLimitPath, "", 201)
	started, err := events.DecodeAs[events.ChargeOnceActionEvent](recvEvent(t, ch))
	if err != nil {
		t.Fatal(err)
	}
	if started.Action != chargeOnceActionStart || started.Target != 70 {
		t.Fatalf("start event = %+v", started)
	}

	f.mustServe("POST", chargeOnceCancelPath, "", 200)
	cancelled, err := events.DecodeAs[events.ChargeOnceActionEvent](recvEvent(t, ch))
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Action != chargeOnceActionCancel || cancelled.Target != 70 {
		t.Fatalf("cancel event = %+v", cancelled)
	}
}

func TestSleepHandlerForwardsToTheDaemon(t *testing.T) {
	// The notifier only ever sees the adapter; each call must land on the
	// daemon.
	f := newSleepHookFixture(t, withCharge(78), withCharging(false))
	f.mem.SetPreventIdleSleep(true)
	f.mem.SetDisableChargingPreSleep(true)
	f.settleLoop()
	h := daemonSleepHandler{f.d}

	if !h.CanSystemSleep() {
		t.Fatal("inside the band, idle sleep is allowed")
	}
	f.smc.setCharging(t, true)
	h.SystemWillSleep()
	if f.smc.chargingEnabled(t) {
		t.Fatal("SystemWillSleep must disable charging")
	}
	before := time.Now()
	h.SystemHasPoweredOn()
	if f.d.lastWakeTime.Before(before) {
		t.Fatal("SystemHasPoweredOn must record the wake")
	}
	f.d.loopDelay.Wait()
}
