package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestNativeLimitReturnsWhileMaintenanceIsBusy(t *testing.T) {
	for _, latest := range []int{60, 100} {
		t.Run(strconv.Itoa(latest), func(t *testing.T) {
			file, _ := useTempConfig(t)
			fake := useExactNativeTargets(t)
			fake.limit, fake.enabled = 55, true
			router := setupRoutes()
			maintainLoopInnerLock.Lock()
			done := make(chan struct{})
			go func() {
				defer close(done)
				for _, target := range []int{50, latest} {
					r := httptest.NewRecorder()
					router.ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader(strconv.Itoa(target))))
					if r.Code != http.StatusAccepted {
						t.Errorf("target %d was not accepted: %d %s", target, r.Code, r.Body.String())
					}
				}
			}()
			select {
			case <-done:
				maintainLoopInnerLock.Unlock()
			case <-time.After(time.Second):
				maintainLoopInnerLock.Unlock()
				<-done
				t.Fatal("limit request waited for busy maintenance")
			}
			if file.UpperLimit() != latest || len(fake.setCalls) != 0 || fake.disables != 0 {
				t.Fatal("requests were not saved and coalesced without touching the backend")
			}
			if !runRequestedMaintenance(t) || fake.limit != latest || fake.enabled != (latest < 100) {
				t.Fatal("background maintenance did not apply the latest target")
			}
		})
	}
}

func TestNativeLimitUsesConfiguredTargetRegardlessOfCharge(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		charge, target, previous int
	}{
		{"full battery", 100, 55, 100},
		{"above limit charging", 70, 55, 80},
		{"at requested target", 50, 50, 50},
		{"below minimum", 8, 55, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, path := useTempConfig(t)
			fake := useExactNativeTargets(t)
			fake.limit, fake.enabled = tc.previous, tc.previous < 100
			mockNativeBatteryCharge(t, tc.charge)
			r := httptest.NewRecorder()
			setupRoutes().ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader(strconv.Itoa(tc.target))))
			if r.Code != http.StatusAccepted || !runRequestedMaintenance(t) || fake.limit != tc.target || !fake.enabled {
				t.Fatalf("limit not applied: %d %s native=%+v", r.Code, r.Body.String(), fake)
			}
			if file.UpperLimit() != tc.target {
				t.Fatalf("saved target = %d, want %d", file.UpperLimit(), tc.target)
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
	if r.Code != http.StatusAccepted || !runRequestedMaintenance(t) || fake.limit != 10 || file.UpperLimit() != 10 {
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
