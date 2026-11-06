package daemon

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
)

func TestMaintainLoopRecorder_GetRecordsIn(t *testing.T) {
	type fields struct {
		MaxRecordCount        int
		LastMaintainLoopTimes []time.Time
		mu                    *sync.Mutex
	}
	type args struct {
		last time.Duration
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
	}{
		{
			name: "test noncontinuous records",
			fields: fields{
				MaxRecordCount: 10,
				LastMaintainLoopTimes: []time.Time{
					time.Now().Add(-time.Second * 31).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 20).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 10).Add(-10 * time.Millisecond),
				},
				mu: &sync.Mutex{},
			},
			args: args{
				last: time.Second * 40,
			},
			want: 2,
		},
		{
			name: "test continuous records",
			fields: fields{
				MaxRecordCount: 10,
				LastMaintainLoopTimes: []time.Time{
					time.Now().Add(-time.Second * 70).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 60).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 40).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 30).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 20).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 10).Add(-10 * time.Millisecond),
				},
				mu: &sync.Mutex{},
			},
			args: args{
				last: time.Second * 50,
			},
			want: 4,
		},
		{
			name: "test continuous records 2",
			fields: fields{
				MaxRecordCount: 10,
				LastMaintainLoopTimes: []time.Time{
					time.Now().Add(-time.Second * 70).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 60).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 40).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 30).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 20).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 15).Add(-10 * time.Millisecond),
				},
				mu: &sync.Mutex{},
			},
			args: args{
				last: time.Second * 50,
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TimeSeriesRecorder{
				MaxRecordCount:        tt.fields.MaxRecordCount,
				Interval:              10 * time.Second,
				LastMaintainLoopTimes: tt.fields.LastMaintainLoopTimes,
				mu:                    tt.fields.mu,
			}
			if got := r.GetRecordsIn(tt.args.last); got != tt.want {
				t.Errorf("GetRecordsIn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaintainLoopHonorsDisabledPreSleepChargingProtection(t *testing.T) {
	// No records represents the first maintain loop after a sleep interruption.
	f := newFixture(t, firmwareLegacy, withCharge(71), withCharging(true))
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(75)
	f.mem.SetDisableChargingPreSleep(false)

	if !f.d.maintainLoop() {
		t.Fatal("legacy maintain loop failed")
	}
	if !f.smc.chargingEnabled(t) {
		t.Fatal("charging was disabled after missed loops despite disable-charging-pre-sleep being disabled")
	}
}

func TestMaintainLoopDisablesChargingAfterMissedLoops(t *testing.T) {
	// With disable-charging-pre-sleep on, missed loops are treated as a sleep
	// macOS did not announce: charging is cut rather than left on.
	f := newFixture(t, firmwareLegacy, withCharge(71), withCharging(true))
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(75)
	f.mem.SetDisableChargingPreSleep(true)

	if !f.d.maintainLoop() {
		t.Fatal("legacy maintain loop failed")
	}
	if f.smc.chargingEnabled(t) {
		t.Fatal("charging must be disabled while maintain loops are missed")
	}
}

func TestRestoreDisabledLimit(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name             string
		disableUntil     time.Time
		preDisableLimit  int
		calibrationPhase calibration.Phase
		want             bool
		wantUpper        int
		wantTimer        bool
	}{
		{
			name:      "no timer set",
			want:      false,
			wantUpper: 100,
			wantTimer: false,
		},
		{
			name:            "deadline not reached",
			disableUntil:    now.Add(time.Hour),
			preDisableLimit: 80,
			want:            false,
			wantUpper:       100,
			wantTimer:       true,
		},
		{
			name:            "deadline reached",
			disableUntil:    now.Add(-time.Second),
			preDisableLimit: 80,
			want:            true,
			wantUpper:       80,
			wantTimer:       false,
		},
		{
			name:            "deadline elapsed while daemon was down",
			disableUntil:    now.Add(-48 * time.Hour),
			preDisableLimit: 75,
			want:            true,
			wantUpper:       75,
			wantTimer:       false,
		},
		{
			name:            "invalid saved limit",
			disableUntil:    now.Add(-time.Second),
			preDisableLimit: 0,
			want:            false,
			wantUpper:       100,
			wantTimer:       false,
		},
		{
			// Calibration force-writes the upper limit it snapshotted, so
			// restoring now would be silently undone. Wait for it to finish.
			name:             "deadline reached during calibration",
			disableUntil:     now.Add(-time.Second),
			preDisableLimit:  80,
			calibrationPhase: calibration.PhaseCharge,
			want:             false,
			wantUpper:        100,
			wantTimer:        true,
		},
		{
			// A failed calibration still holds its snapshot until it is
			// cancelled, and cancelling force-writes the limit.
			name:             "deadline reached after a failed calibration",
			disableUntil:     now.Add(-time.Second),
			preDisableLimit:  80,
			calibrationPhase: calibration.PhaseError,
			want:             false,
			wantUpper:        100,
			wantTimer:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.calibrationPhase
			if phase == "" {
				phase = calibration.PhaseIdle
			}
			c := newMemConfig()
			c.upper, c.lower = 100, 98
			c.disableUntil, c.preDisableLimit = tt.disableUntil, tt.preDisableLimit
			f := newFixture(t, firmwareLegacy, withConfig(c))
			f.setCalibration(&calibration.State{Phase: phase})

			if got := f.d.restoreDisabledLimit(now); got != tt.want {
				t.Errorf("restoreDisabledLimit() = %v, want %v", got, tt.want)
			}
			if c.UpperLimit() != tt.wantUpper {
				t.Errorf("upper limit = %d, want %d", c.UpperLimit(), tt.wantUpper)
			}
			if gotTimer := !c.DisableUntil().IsZero(); gotTimer != tt.wantTimer {
				t.Errorf("timer set = %v, want %v", gotTimer, tt.wantTimer)
			}
		})
	}
}

func TestRestoreDisabledLimitWaitsForPersistedCalibration(t *testing.T) {
	now := time.Now()
	c := newMemConfig()
	c.upper, c.lower = 100, 78
	c.disableUntil, c.preDisableLimit = now.Add(-time.Hour), 80
	f := newFixture(t, firmwareLegacy, withConfig(c))
	f.setCalibration(&calibration.State{Phase: calibration.PhaseCharge})

	if f.d.restoreDisabledLimit(now) {
		t.Fatal("temporary disable restored while calibration owned the charge limit")
	}
	if c.DisableUntil().IsZero() {
		t.Fatal("temporary disable timer was cleared while calibration was active")
	}

	f.setCalibration(&calibration.State{Phase: calibration.PhaseIdle})
	if !f.d.restoreDisabledLimit(now) {
		t.Fatal("temporary disable was not restored after calibration finished")
	}
	if c.UpperLimit() != 80 || !c.DisableUntil().IsZero() {
		t.Fatalf("restored config = %+v, want upper 80 with no timer", c.snapshot())
	}
}

func TestMaintainAdapterDisable(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name             string
		until            time.Time
		adapterEnabled   bool
		calibrationPhase calibration.Phase
		wantRestored     bool
		wantEnabled      bool
		wantTimer        bool
	}{
		{name: "no schedule", adapterEnabled: true, wantEnabled: true},
		{
			name:           "future schedule is enforced after restart",
			until:          now.Add(time.Hour),
			adapterEnabled: true,
			wantEnabled:    false,
			wantTimer:      true,
		},
		{
			name:           "future schedule already enforced",
			until:          now.Add(time.Hour),
			adapterEnabled: false,
			wantEnabled:    false,
			wantTimer:      true,
		},
		{
			name:           "expired schedule enables adapter",
			until:          now.Add(-time.Second),
			adapterEnabled: false,
			wantRestored:   true,
			wantEnabled:    true,
		},
		{
			name:             "calibration keeps expired schedule pending",
			until:            now.Add(-time.Second),
			adapterEnabled:   false,
			calibrationPhase: calibration.PhaseCharge,
			wantEnabled:      false,
			wantTimer:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.calibrationPhase
			if phase == "" {
				phase = calibration.PhaseIdle
			}
			c := newMemConfig()
			c.upper, c.lower = 80, 78
			c.adapterDisableUntil = tt.until
			f := newFixture(t, firmwareLegacy, withConfig(c), withAdapter(tt.adapterEnabled))
			f.setCalibration(&calibration.State{Phase: phase})

			if got := f.d.maintainAdapterDisable(now); got != tt.wantRestored {
				t.Errorf("maintainAdapterDisable() = %v, want %v", got, tt.wantRestored)
			}
			if enabled := f.smc.adapterEnabled(t); enabled != tt.wantEnabled {
				t.Errorf("adapter enabled = %v, want %v", enabled, tt.wantEnabled)
			}
			if gotTimer := !c.AdapterDisableUntil().IsZero(); gotTimer != tt.wantTimer {
				t.Errorf("timer set = %v, want %v", gotTimer, tt.wantTimer)
			}
		})
	}
}

func TestTemporaryDisableDoesNotCancelRestartedCalibration(t *testing.T) {
	c := newMemConfig()
	c.upper, c.lower = 100, 78
	c.disableUntil, c.preDisableLimit = time.Now().Add(time.Hour), 80
	f := newFixture(t, firmwareLegacy, withConfig(c))
	f.setCalibration(&calibration.State{Phase: calibration.PhaseCharge, Paused: true})

	f.d.cancelCalibrationForPermanentDisable()
	if st := f.calibration(); st.Phase != calibration.PhaseCharge || !st.Paused {
		t.Fatalf("persisted calibration was unexpectedly cancelled: %+v", st)
	}
}

func TestMaintainFirmwareChargeLimitWithoutLegacyPolling(t *testing.T) {
	f := newFixture(t, firmwareManaged)
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(78)

	if !f.d.maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	if state := f.smc.firmwareLimit(t); !state.Active || state.Lower != 78 || state.Upper != 80 {
		t.Fatalf("unexpected firmware state: %+v", state)
	}

	f.mem.SetUpperLimit(100)
	if !f.d.maintainLoopForced() {
		t.Fatal("firmware disable loop failed")
	}
	if state := f.smc.firmwareLimit(t); state.Active {
		t.Fatal("firmware charge limit should be inactive at 100%")
	}
}

func TestMaintainAdapterDisable_RestartIndefiniteReconcilesHold(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withAdapter(false))
	f.mem.SetPreventSleepOnAdapterDisable(true)

	f.d.maintainAdapterDisable(time.Now())

	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("expected sleep hold to be acquired for indefinite disable on restart")
	}
	if !f.sleep.disabled() {
		t.Fatal("expected SleepDisabled to be true")
	}
}

func TestMaintainAdapterDisable_EnableFailureKeepsTimer(t *testing.T) {
	now := time.Now()
	f := newFixture(t, firmwareLegacy, withAdapter(false))
	f.mem.SetAdapterDisableTimer(now.Add(-time.Second))
	f.smc.fail("EnableAdapter", errors.New("SMC enable failed"))

	if restored := f.d.maintainAdapterDisable(now); restored {
		t.Fatal("maintainAdapterDisable should report false when enable fails")
	}
	if f.mem.AdapterDisableUntil().IsZero() {
		t.Fatal("timer must not be cleared when adapter enable fails")
	}
}

func TestMaintainAdapterDisable_ActiveTimerRetriesReleaseAfterOptionDisabled(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withAdapter(false))
	f.mem.SetAdapterDisableTimer(time.Now().Add(time.Hour))
	if err := f.d.holds.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	f.mem.SetPreventSleepOnAdapterDisable(false)

	f.d.maintainAdapterDisable(time.Now())

	if f.holdsSleep(sleepHoldAdapter) || f.sleep.disabled() {
		t.Fatal("disabled option must release a stale hold before the timer expires")
	}
}

func TestMaintainAdapterDisable_AlreadyEnabledRetriesReleaseHold(t *testing.T) {
	now := time.Now()
	f := newFixture(t, firmwareLegacy, withAdapter(true))
	f.mem.SetAdapterDisableTimer(now.Add(-time.Second))
	if err := f.d.holds.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	// Simulate release failure
	f.sleep.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if restored := f.d.maintainAdapterDisable(now); restored {
		t.Fatal("maintainAdapterDisable should report false when release fails")
	}
	if f.mem.AdapterDisableUntil().IsZero() {
		t.Fatal("timer must not be cleared when sleep hold release fails")
	}
	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold must be preserved on release failure")
	}

	// Retry succeeds
	f.sleep.setErr = nil
	if restored := f.d.maintainAdapterDisable(now); !restored {
		t.Fatal("maintainAdapterDisable should report true when release succeeds")
	}
	if !f.mem.AdapterDisableUntil().IsZero() {
		t.Fatal("timer must be cleared once release succeeds")
	}
	if f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold must be released")
	}
}

func TestLoopOnceFinishesTimersAndMaintains(t *testing.T) {
	// One iteration of the main loop restores an elapsed temporary disable and
	// then enforces the restored limit.
	now := time.Now()
	f := newFixture(t, firmwareLegacy, withCharge(90), withCharging(true))
	f.mem.SetUpperLimit(100)
	f.mem.SetDisableTimer(now.Add(-time.Minute), 80)

	f.d.loopOnce(now)

	if f.mem.UpperLimit() != 80 || !f.mem.DisableUntil().IsZero() {
		t.Fatalf("elapsed temporary disable was not restored: %+v", f.mem.snapshot())
	}
	if f.smc.chargingEnabled(t) {
		t.Fatal("the restored 80% limit must stop charging at 90%")
	}
}
