package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/events"
	"github.com/charlie0129/batt/pkg/powerinfo"
)

// The simple setters: validation, persistence, and the capability gate.
func TestSettingEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     string
		wantCode int
		check    func(t *testing.T, c *memConfig)
	}{
		{name: "prevent idle sleep", path: "/prevent-idle-sleep", body: "true", wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if !c.PreventIdleSleep() {
				t.Fatal("not applied")
			}
		}},
		{name: "disable charging pre sleep", path: "/disable-charging-pre-sleep", body: "true", wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if !c.DisableChargingPreSleep() {
				t.Fatal("not applied")
			}
		}},
		{name: "prevent system sleep", path: "/prevent-system-sleep", body: "true", wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if !c.PreventSystemSleep() {
				t.Fatal("not applied")
			}
		}},
		{name: "malformed boolean", path: "/prevent-idle-sleep", body: `"yes"`, wantCode: http.StatusBadRequest},
		{name: "lower limit delta", path: "/lower-limit-delta", body: "5", wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if c.LowerLimit() != 75 {
				t.Fatalf("lower = %d, want 75", c.LowerLimit())
			}
		}},
		{name: "negative lower limit delta", path: "/lower-limit-delta", body: "-1", wantCode: http.StatusBadRequest},
		{name: "lower limit delta below 10%", path: "/lower-limit-delta", body: "75", wantCode: http.StatusBadRequest},
		{name: "limit", path: "/limit", body: "85", wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if c.UpperLimit() != 85 {
				t.Fatalf("upper = %d, want 85", c.UpperLimit())
			}
		}},
		{name: "limit below 10", path: "/limit", body: "5", wantCode: http.StatusBadRequest},
		{name: "limit above 100", path: "/limit", body: "101", wantCode: http.StatusBadRequest},
		{name: "limit too close to the lower limit", path: "/limit", body: "11", wantCode: http.StatusBadRequest},
		{name: "limit not a number", path: "/limit", body: `"eighty"`, wantCode: http.StatusBadRequest},
		{name: "negative disable duration", path: "/disable", body: `"-1h"`, wantCode: http.StatusBadRequest},
		{name: "malformed disable duration", path: "/disable", body: `"soon"`, wantCode: http.StatusBadRequest},
		{name: "zero adapter disable duration", path: "/adapter/disable", body: `"0s"`, wantCode: http.StatusBadRequest},
		{name: "calibration threshold", path: "/calibration/discharge-threshold", body: "20", wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if c.CalibrationDischargeThreshold() != 20 {
				t.Fatal("not applied")
			}
		}},
		{name: "calibration threshold too low", path: "/calibration/discharge-threshold", body: "5", wantCode: http.StatusBadRequest},
		{name: "calibration threshold too high", path: "/calibration/discharge-threshold", body: "60", wantCode: http.StatusBadRequest},
		{name: "calibration hold", path: "/calibration/hold-duration", body: "30", wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if c.CalibrationHoldDurationMinutes() != 30 {
				t.Fatal("not applied")
			}
		}},
		{name: "calibration hold too short", path: "/calibration/hold-duration", body: "5", wantCode: http.StatusBadRequest},
		{name: "magsafe led", path: "/magsafe-led", body: `"always-off"`, wantCode: http.StatusCreated, check: func(t *testing.T, c *memConfig) {
			if c.ControlMagSafeLED() != config.ControlMagSafeModeAlwaysOff {
				t.Fatal("not applied")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, firmwareLegacy)
			response := f.put(tt.path, tt.body)
			if response.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, tt.wantCode, response.Body.String())
			}
			if tt.check != nil {
				tt.check(t, f.mem)
			}
			if tt.wantCode == http.StatusCreated && f.mem.saves == 0 {
				t.Fatal("an accepted setting must be persisted")
			}
		})
	}
}

func TestSettingEndpointsReportSaveFailures(t *testing.T) {
	for _, path := range []string{"/prevent-idle-sleep", "/prevent-system-sleep", "/disable-charging-pre-sleep"} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t, firmwareLegacy)
			f.mem.failSave(errors.New("disk full"))
			if response := f.put(path, "true"); response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", response.Code)
			}
		})
	}
}

func TestMagSafeEndpointNeedsTheLED(t *testing.T) {
	f := newFixture(t, firmwareLegacy.withMagSafe(false), withCapabilities(legacyCaps()))
	if response := f.put("/magsafe-led", `"enabled"`); response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 on a Mac without MagSafe", response.Code)
	}
}

func TestReadEndpoints(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCharge(63), withCharging(false))
	f.battery.info = &powerkit.SystemInfo{IOKit: &powerkit.IOKitData{
		State:   powerkit.IOKitState{IsCharging: true},
		Battery: powerkit.IOKitBattery{Voltage: 12.0, Amperage: 2.0, DesignCapacity: 6000, MaxCapacity: 5500, CycleCount: 42},
		Adapter: powerkit.IOKitAdapter{InputVoltage: 20, InputAmperage: 3},
	}}

	if response := f.mustServe(http.MethodGet, "/limit", "", http.StatusOK); strings.TrimSpace(response.Body.String()) != "80" {
		t.Fatalf("/limit = %s", response.Body.String())
	}
	if response := f.mustServe(http.MethodGet, "/current-charge", "", http.StatusOK); strings.TrimSpace(response.Body.String()) != "63" {
		t.Fatalf("/current-charge = %s", response.Body.String())
	}
	if response := f.mustServe(http.MethodGet, "/plugged-in", "", http.StatusOK); strings.TrimSpace(response.Body.String()) != "true" {
		t.Fatalf("/plugged-in = %s", response.Body.String())
	}
	if response := f.mustServe(http.MethodGet, "/charging", "", http.StatusOK); strings.TrimSpace(response.Body.String()) != "false" {
		t.Fatalf("/charging = %s", response.Body.String())
	}
	if response := f.mustServe(http.MethodGet, "/adapter", "", http.StatusOK); strings.TrimSpace(response.Body.String()) != "true" {
		t.Fatalf("/adapter = %s", response.Body.String())
	}
	if response := f.mustServe(http.MethodGet, "/charging-control-capable", "", http.StatusOK); strings.TrimSpace(response.Body.String()) != "true" {
		t.Fatalf("/charging-control-capable = %s", response.Body.String())
	}
	f.mustServe(http.MethodGet, "/version", "", http.StatusOK)

	var caps compatibility.Capabilities
	if err := json.Unmarshal(f.mustServe(http.MethodGet, "/compatibility", "", http.StatusOK).Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.ChargeControlMode != compatibility.ChargeControlLegacy || !caps.SleepHooks {
		t.Fatalf("/compatibility = %+v", caps)
	}

	var raw config.RawFileConfig
	if err := json.Unmarshal(f.mustServe(http.MethodGet, "/config", "", http.StatusOK).Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Limit == nil || *raw.Limit != 80 {
		t.Fatalf("/config limit = %v", raw.Limit)
	}

	var battery powerinfo.Battery
	if err := json.Unmarshal(f.mustServe(http.MethodGet, "/battery-info", "", http.StatusOK).Body.Bytes(), &battery); err != nil {
		t.Fatal(err)
	}
	if battery.State != powerinfo.Charging || battery.ChargeRate != 24000 || battery.MaxCapacity != 5500 || battery.ChargingToFullOverride != nil {
		t.Fatalf("/battery-info = %+v", battery)
	}

	var telemetry struct {
		Power       *powerinfo.PowerTelemetry `json:"power"`
		Calibration *calibration.Status       `json:"calibration"`
	}
	response := f.mustServe(http.MethodGet, "/telemetry", "", http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &telemetry); err != nil {
		t.Fatal(err)
	}
	if telemetry.Power == nil || telemetry.Power.Battery.CycleCount != 42 || telemetry.Power.Adapter.InputVoltage != 20 {
		t.Fatalf("/telemetry power = %+v", telemetry.Power)
	}
	if telemetry.Calibration == nil || telemetry.Calibration.Phase != calibration.PhaseIdle || telemetry.Calibration.ChargePercent != 63 {
		t.Fatalf("/telemetry calibration = %+v", telemetry.Calibration)
	}
	if response.Header().Get("X-Batt-Telemetry-Version") != "1" {
		t.Fatal("telemetry version header missing")
	}

	response = f.mustServe(http.MethodGet, "/telemetry?power=0", "", http.StatusOK)
	if strings.Contains(response.Body.String(), `"power"`) {
		t.Fatal("power=0 must leave power telemetry out")
	}

	response = f.mustServe(http.MethodGet, "/power-telemetry", "", http.StatusOK)
	if response.Header().Get("X-Deprecated") != "true" {
		t.Fatal("the deprecated endpoint must say so")
	}
}

func TestReadEndpointsReportHardwareFailures(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	f.smc.fail("GetBatteryCharge", errors.New("smc read failed"))
	f.smc.fail("IsPluggedIn", errors.New("smc read failed"))
	f.smc.fail("IsChargingEnabled", errors.New("smc read failed"))
	f.smc.fail("IsAdapterEnabled", errors.New("smc read failed"))
	f.battery.setErr(errors.New("iokit unavailable"))

	for _, path := range []string{"/current-charge", "/plugged-in", "/charging", "/adapter", "/battery-info", "/power-telemetry"} {
		if response := f.serve(http.MethodGet, path, ""); response.Code != http.StatusInternalServerError {
			t.Errorf("GET %s = %d, want 500", path, response.Code)
		}
	}
	// Unified telemetry degrades instead of failing.
	response := f.mustServe(http.MethodGet, "/telemetry", "", http.StatusOK)
	if strings.Contains(response.Body.String(), `"power"`) {
		t.Fatal("unavailable power telemetry must be left out, not invented")
	}
}

func TestReadEndpointsFollowTheChargeControlMode(t *testing.T) {
	// Direct charging state only exists with the legacy keys, and the native
	// mode adds macOS's full-charge override to the battery info.
	f := newFixture(t, firmwareGated, withNative(sliderNative(80, 100)))
	f.battery.info = batteryCharging(true)
	f.battery.override = true

	if response := f.serve(http.MethodGet, "/charging", ""); response.Code != http.StatusConflict {
		t.Fatalf("/charging in native mode = %d, want 409", response.Code)
	}
	var battery powerinfo.Battery
	if err := json.Unmarshal(f.mustServe(http.MethodGet, "/battery-info", "", http.StatusOK).Body.Bytes(), &battery); err != nil {
		t.Fatal(err)
	}
	if battery.ChargingToFullOverride == nil || !*battery.ChargingToFullOverride {
		t.Fatalf("/battery-info in native mode = %+v, want the full-charge override", battery)
	}

	f.setCaps(compatibility.Capabilities{ChargingControl: false})
	if response := f.serve(http.MethodGet, "/adapter", ""); response.Code != http.StatusConflict {
		t.Fatalf("/adapter without adapter control = %d, want 409", response.Code)
	}
	if response := f.serve(http.MethodGet, "/charging-control-capable", ""); strings.TrimSpace(response.Body.String()) != "false" {
		t.Fatalf("/charging-control-capable = %s", response.Body.String())
	}
}

// streamRecorder is a response writer that can be read while the SSE handler
// is still writing to it.
type streamRecorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
}

func (r *streamRecorder) Header() http.Header { return r.header }
func (r *streamRecorder) WriteHeader(int)     {}
func (r *streamRecorder) Flush()              {}
func (r *streamRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(b)
}
func (r *streamRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func TestEventStreamDeliversDaemonEvents(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/event", nil).WithContext(ctx)
	response := &streamRecorder{header: http.Header{}}
	done := make(chan struct{})
	go func() {
		f.d.routes().ServeHTTP(response, request)
		close(done)
	}()

	// The subscription is taken when the handler starts, so publish until the
	// event shows up in the stream.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(response.String(), "event: "+events.ChargeOnceAction) {
		if time.Now().After(deadline) {
			break
		}
		f.d.hub.Publish(events.ChargeOnceAction, events.ChargeOnceActionEvent{Action: "Start", Target: 100})
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the stream did not end with the request")
	}

	body := response.String()
	if !strings.HasPrefix(body, "retry: 10000\n:ok\n\n") {
		t.Fatalf("stream did not open properly: %q", body)
	}
	if !strings.Contains(body, "event: "+events.ChargeOnceAction+"\ndata: {") || !strings.Contains(body, `"target":100`) {
		t.Fatalf("published event not streamed: %q", body)
	}
	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatal("wrong content type")
	}
}

func TestCalibrationEndpoints(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCharge(40), withCharging(false))

	for _, path := range []string{"/calibration/pause", "/calibration/resume", "/calibration/cancel"} {
		if response := f.post(path); response.Code != http.StatusBadRequest {
			t.Errorf("%s without a calibration = %d, want 400", path, response.Code)
		}
	}

	f.mustServe(http.MethodPost, "/calibration/start", "", http.StatusCreated)
	if f.phase() != calibration.PhaseDischarge || !f.asserts.holdsCalibrationSleep() {
		t.Fatalf("calibration did not start: phase=%s", f.phase())
	}
	if response := f.post("/calibration/start"); response.Code != http.StatusBadRequest {
		t.Fatalf("a second start = %d, want 400", response.Code)
	}

	f.mustServe(http.MethodPost, "/calibration/pause", "", http.StatusOK)
	if !f.calibration().Paused {
		t.Fatal("not paused")
	}
	f.mustServe(http.MethodPost, "/calibration/resume", "", http.StatusOK)
	if f.calibration().Paused {
		t.Fatal("not resumed")
	}

	var telemetry struct {
		Calibration *calibration.Status `json:"calibration"`
	}
	if err := json.Unmarshal(f.mustServe(http.MethodGet, "/telemetry?power=0", "", http.StatusOK).Body.Bytes(), &telemetry); err != nil {
		t.Fatal(err)
	}
	if telemetry.Calibration == nil || telemetry.Calibration.Phase != calibration.PhaseDischarge || !telemetry.Calibration.CanPause || !telemetry.Calibration.CanCancel {
		t.Fatalf("telemetry = %+v", telemetry.Calibration)
	}

	f.mustServe(http.MethodPost, "/calibration/cancel", "", http.StatusOK)
	if f.phase() != calibration.PhaseIdle || f.asserts.holdsCalibrationSleep() {
		t.Fatalf("cancel left phase=%s assertion=%t", f.phase(), f.asserts.holdsCalibrationSleep())
	}
}

func TestCalibrationEndpointsNeedTheCapability(t *testing.T) {
	f := newFixture(t, firmwareGated, withNative(sliderNative(80, 100)), withCapabilities(compatibility.Capabilities{
		ChargingControl: true, ChargeControlMode: compatibility.ChargeControlNative,
	}))
	for _, path := range []string{"/calibration/start", "/calibration/pause", "/calibration/resume", "/calibration/cancel"} {
		if response := f.post(path); response.Code != http.StatusConflict {
			t.Errorf("%s without calibration support = %d, want 409", path, response.Code)
		}
	}
	for _, path := range []string{"/schedule", "/schedule/skip", "/schedule/postpone", "/calibration/discharge-threshold", "/calibration/hold-duration"} {
		if response := f.put(path, `"1h"`); response.Code != http.StatusConflict {
			t.Errorf("%s without calibration support = %d, want 409", path, response.Code)
		}
	}
}

func TestScheduleEndpoints(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	t.Cleanup(f.d.scheduler.Stop)

	if response := f.put("/schedule/skip", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("skip without a schedule = %d, want 400", response.Code)
	}
	if response := f.put("/schedule/postpone", `"10m"`); response.Code != http.StatusBadRequest {
		t.Fatalf("postpone without a schedule = %d, want 400", response.Code)
	}
	if response := f.put("/schedule", `"every blue moon"`); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid cron = %d, want 400", response.Code)
	}
	if response := f.put("/schedule", `""`); response.Code != http.StatusCreated {
		t.Fatalf("disabling an absent schedule = %d, want 201", response.Code)
	}

	response := f.mustServe(http.MethodPut, "/schedule", `"@every 1h"`, http.StatusCreated)
	var resp struct {
		OK       bool        `json:"ok"`
		NextRuns []time.Time `json:"next_runs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || len(resp.NextRuns) != 3 || f.mem.Cron() != "@every 1h" {
		t.Fatalf("schedule response = %+v, cron = %q", resp, f.mem.Cron())
	}
	next, running := f.d.scheduler.Status()
	if !running || next.IsZero() {
		t.Fatal("the scheduler must be running")
	}

	f.mustServe(http.MethodPut, "/schedule/skip", "", http.StatusOK)
	if skipped, _ := f.d.scheduler.Status(); !skipped.After(next) {
		t.Fatalf("skip did not move the next run: %s -> %s", next, skipped)
	}
	f.mustServe(http.MethodPut, "/schedule/postpone", `"10m"`, http.StatusOK)
	if response := f.put("/schedule/postpone", `"2h"`); response.Code != http.StatusBadRequest {
		t.Fatalf("postponing past the next run = %d, want 400", response.Code)
	}

	f.mustServe(http.MethodPut, "/schedule", `""`, http.StatusCreated)
	if f.mem.Cron() != "" {
		t.Fatal("disabling the schedule must clear the cron expression")
	}
}
