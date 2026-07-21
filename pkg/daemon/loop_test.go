package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/smc"
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
		loopInterval = time.Second * 10
		t.Run(tt.name, func(t *testing.T) {
			r := &TimeSeriesRecorder{
				MaxRecordCount:        tt.fields.MaxRecordCount,
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
	value := func(key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
		v, err := gosmc.NewValue(key, dataType, data)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	mock := smc.NewMockValues(
		value(smc.ChargingKey1, gosmc.TypeUInt8, 0),
		value(smc.ChargingKey2, gosmc.TypeUInt8, 0),
		value(smc.BatteryChargeKey, gosmc.TypeUInt8, 71),
		value(smc.ACPowerKey, gosmc.TypeUInt8, 1),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })

	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	previousRecorder := loopRecorder
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		loopRecorder = previousRecorder
	})
	smcConn = mock
	conf = &mockConf{
		upper: 80,
		lower: 75,
	}
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlLegacy,
	}
	// No records represents the first maintain loop after a sleep interruption.
	loopRecorder = NewTimeSeriesRecorder(60)

	if !maintainLoop() {
		t.Fatal("legacy maintain loop failed")
	}
	charging, err := mock.IsChargingEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if !charging {
		t.Fatal("charging was disabled after missed loops despite disable-charging-pre-sleep being disabled")
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
			calibrationState = &calibration.State{Phase: phase}
			t.Cleanup(func() {
				calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
			})

			c := &mockConf{
				upper:           100,
				lower:           98,
				disableUntil:    tt.disableUntil,
				preDisableLimit: tt.preDisableLimit,
			}

			if got := restoreDisabledLimit(c, now); got != tt.want {
				t.Errorf("restoreDisabledLimit() = %v, want %v", got, tt.want)
			}
			if c.upper != tt.wantUpper {
				t.Errorf("upper limit = %d, want %d", c.upper, tt.wantUpper)
			}
			if gotTimer := !c.disableUntil.IsZero(); gotTimer != tt.wantTimer {
				t.Errorf("timer set = %v, want %v", gotTimer, tt.wantTimer)
			}
		})
	}
}

func TestRestoreDisabledLimitWaitsForPersistedCalibration(t *testing.T) {
	previousState := calibrationState
	t.Cleanup(func() { calibrationState = previousState })

	now := time.Now()
	configured := &mockConf{
		upper:           100,
		lower:           78,
		disableUntil:    now.Add(-time.Hour),
		preDisableLimit: 80,
	}
	calibrationState = &calibration.State{Phase: calibration.PhaseCharge}

	if restoreDisabledLimit(configured, now) {
		t.Fatal("temporary disable restored while calibration owned the charge limit")
	}
	if configured.disableUntil.IsZero() {
		t.Fatal("temporary disable timer was cleared while calibration was active")
	}

	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	if !restoreDisabledLimit(configured, now) {
		t.Fatal("temporary disable was not restored after calibration finished")
	}
	if configured.upper != 80 || !configured.disableUntil.IsZero() {
		t.Fatalf("restored config = %+v, want upper 80 with no timer", configured)
	}
}

func TestTemporaryDisableDoesNotCancelRestartedCalibration(t *testing.T) {
	previousConf, previousState := conf, calibrationState
	t.Cleanup(func() { conf, calibrationState = previousConf, previousState })

	conf = &mockConf{
		upper:           100,
		lower:           78,
		disableUntil:    time.Now().Add(time.Hour),
		preDisableLimit: 80,
	}
	calibrationState = &calibration.State{Phase: calibration.PhaseCharge, Paused: true}

	cancelCalibrationForPermanentDisable()
	if calibrationState.Phase != calibration.PhaseCharge || !calibrationState.Paused {
		t.Fatalf("persisted calibration was unexpectedly cancelled: %+v", calibrationState)
	}
}

func TestMaintainFirmwareChargeLimitWithoutLegacyPolling(t *testing.T) {
	value := func(key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
		v, err := gosmc.NewValue(key, dataType, data)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	mock := smc.NewMockValues(
		value(smc.FirmwareChargeLimitActivationKey, gosmc.TypeUInt8, 0),
		value(smc.FirmwareChargeLimitUpperKey, gosmc.TypeUInt32, 0, 0, 0, 0),
		value(smc.FirmwareChargeLimitLowerKey, gosmc.TypeUInt32, 0, 0, 0, 0),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })

	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
	})
	smcConn = mock
	conf = &mockConf{upper: 80, lower: 78}
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
	}

	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err := mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 78 || state.Upper != 80 {
		t.Fatalf("unexpected firmware state: %+v", state)
	}

	conf.SetUpperLimit(100)
	if !maintainLoopForced() {
		t.Fatal("firmware disable loop failed")
	}
	state, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active {
		t.Fatal("firmware charge limit should be inactive at 100%")
	}
}
