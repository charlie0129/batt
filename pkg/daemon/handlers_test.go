package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
)

func TestResolveDisableLimit(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name            string
		upper           int
		disableUntil    time.Time
		preDisableLimit int
		want            int
		wantOK          bool
	}{
		{
			name:   "limit is set",
			upper:  80,
			want:   80,
			wantOK: true,
		},
		{
			name:   "already disabled without a timer",
			upper:  100,
			want:   0,
			wantOK: false,
		},
		{
			name:            "already disabled with a pending timer",
			upper:           100,
			disableUntil:    now.Add(time.Hour),
			preDisableLimit: 80,
			want:            80,
			wantOK:          true,
		},
		{
			name:            "saved limit without a timer",
			upper:           100,
			preDisableLimit: 80,
			want:            0,
			wantOK:          false,
		},
		{
			name:            "pending timer with an invalid saved limit",
			upper:           100,
			disableUntil:    now.Add(time.Hour),
			preDisableLimit: 5,
			want:            0,
			wantOK:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &mockConf{
				upper:           tt.upper,
				disableUntil:    tt.disableUntil,
				preDisableLimit: tt.preDisableLimit,
			}

			got, gotOK := resolveDisableLimit(c)
			if got != tt.want || gotOK != tt.wantOK {
				t.Errorf("resolveDisableLimit() = (%d, %v), want (%d, %v)", got, gotOK, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSetDisableForRejectsCalibration(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	configured := &mockConf{upper: 80, lower: 78}
	conf = configured
	capabilities = compatibility.Capabilities{ChargingControl: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseCharge}
	calibrationStatePath = ""

	request := httptest.NewRequest(http.MethodPut, "/disable", strings.NewReader(`"1h0m0s"`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "calibration is in progress") {
		t.Fatalf("response does not explain conflict: %s", response.Body.String())
	}
	if configured.upper != 80 || !configured.disableUntil.IsZero() {
		t.Fatalf("temporary disable mutated config after rejection: %+v", configured)
	}
}

func TestSetLimitRejectsCalibration(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	for _, phase := range []calibration.Phase{calibration.PhaseCharge, calibration.PhaseError} {
		t.Run(string(phase), func(t *testing.T) {
			configured := &mockConf{upper: 80, lower: 78}
			conf = configured
			capabilities = compatibility.Capabilities{ChargingControl: true}
			calibrationState = &calibration.State{Phase: phase}
			calibrationStatePath = ""

			request := httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("90"))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			setupRoutes().ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), ErrCalibrationControlsChargeLimit.Error()) {
				t.Fatalf("response does not explain conflict: %s", response.Body.String())
			}
			if configured.upper != 80 {
				t.Fatalf("upper limit = %d after rejection, want 80", configured.upper)
			}
		})
	}
}

func TestStartCalibrationRequestRejectsTemporaryDisable(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	conf = &mockConf{
		upper:           100,
		lower:           78,
		disableUntil:    time.Now().Add(time.Hour),
		preDisableLimit: 80,
	}
	capabilities = compatibility.Capabilities{Calibration: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	calibrationStatePath = ""

	request := httptest.NewRequest(http.MethodPost, "/calibration/start", nil)
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrTemporaryDisableInProgress.Error()) {
		t.Fatalf("response does not explain conflict: %s", response.Body.String())
	}
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", calibrationState.Phase)
	}
}
