package daemon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

func mockNativeBatteryCharge(t *testing.T, charge int) {
	t.Helper()
	value, err := gosmc.NewValue(smc.BatteryChargeKey, gosmc.TypeUInt8, []byte{byte(charge)})
	if err != nil {
		t.Fatal(err)
	}
	useMockSMC(t, value)
}

func TestNativeLimitUsesConfiguredTargetRegardlessOfCharge(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		charge, target, previous int
	}{
		{"full battery", 100, 97, 100},
		{"above limit paused", 70, 55, 70},
		{"above limit charging", 70, 55, 80},
		{"near full", 99, 50, 99},
		{"above requested target", 60, 50, 60},
		{"at requested target", 50, 50, 50},
		{"inside old band", 52, 55, 52},
		{"below minimum", 8, 55, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, path := useTempConfig(t)
			fake := useExactNativeTargets(t)
			fake.limit, fake.enabled = tc.previous, tc.previous < 100
			mockNativeBatteryCharge(t, tc.charge)
			r := httptest.NewRecorder()
			setupRoutes().ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader(strconv.Itoa(tc.target))))
			if r.Code != http.StatusCreated || fake.limit != tc.target || !fake.enabled {
				t.Fatalf("limit not applied: %d %s native=%+v", r.Code, r.Body.String(), fake)
			}
			wantMessage := fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%, current charge: %d%%", tc.target, tc.target-5, tc.charge)
			if tc.charge > tc.target {
				wantMessage += ". Current charge is above the limit; macOS may use battery power until it falls within the configured range."
			}
			if strings.TrimSpace(r.Body.String()) != strconv.Quote(wantMessage) || file.UpperLimit() != tc.target {
				t.Fatalf("unexpected native limit response or saved target: %s target=%d", r.Body.String(), file.UpperLimit())
			}
			reloaded, err := config.NewFile(path)
			if err != nil {
				t.Fatal(err)
			}
			conf = reloaded
			if !maintainLoopForced() || fake.limit != tc.target || !fake.enabled || fake.disables != 0 {
				t.Fatal("restart changed or released the configured target")
			}
		})
	}
}

func TestNativeTargetDoesNotFollowUnpluggedBattery(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake := useExactNativeTargets(t)
	stubAdapter(t, false)
	for _, charge := range []int{52, 51, 49, 8} {
		mockNativeBatteryCharge(t, charge)
		if !maintainLoopForced() || fake.limit != 55 {
			t.Fatalf("unplugged battery at %d%% changed the target to %d%%", charge, fake.limit)
		}
	}
	stubAdapter(t, true)
	if !maintainLoopForced() || fake.limit != 55 || len(fake.setCalls) != 1 {
		t.Fatal("reconnect changed or rewrote the target")
	}
}

func TestNativeLowerLimitIsUnsupported(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake := useExactNativeTargets(t)
	r := httptest.NewRecorder()
	setupRoutes().ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader("10")))
	if r.Code != http.StatusConflict || file.LowerLimit() != 50 || len(fake.setCalls) != 0 {
		t.Fatalf("native lower limit accepted: %d %s", r.Code, r.Body.String())
	}
}

func TestNativeMinimumTargetIgnoresSavedLowerLimitDelta(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(80)
	file.SetLowerLimit(30)
	fake := useExactNativeTargets(t)
	r := httptest.NewRecorder()
	setupRoutes().ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("10")))
	if r.Code != http.StatusCreated || fake.limit != 10 || file.UpperLimit() != 10 {
		t.Fatalf("saved delta blocked minimum native target: %d %s", r.Code, r.Body.String())
	}
}

func TestNativeMinimumTargetSaveFailureRestoresLimits(t *testing.T) {
	file, path := useTempConfig(t)
	file.SetUpperLimit(80)
	file.SetLowerLimit(30)
	fake := useExactNativeTargets(t)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	setupRoutes().ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("10")))
	if r.Code != http.StatusInternalServerError || file.UpperLimit() != 80 || file.LowerLimit() != 30 || len(fake.setCalls) != 0 {
		t.Fatalf("failed save changed limits: %d %s config=%d/%d", r.Code, r.Body.String(), file.UpperLimit(), file.LowerLimit())
	}
}
