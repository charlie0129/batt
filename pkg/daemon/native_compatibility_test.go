package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/config"
)

func useExactNativeTargets(t *testing.T) *fakeNativeLimit {
	t.Helper()
	gatedSMCNoAdapter(t)
	useNativeCapabilities(t)
	capabilities.NativeExactTargets = true
	previousRequests := maintainLoopRequested
	maintainLoopRequested = make(chan struct{}, 1)
	t.Cleanup(func() { maintainLoopRequested = previousRequests })
	fake := &fakeNativeLimit{limit: 100, exact: true}
	useFakeNativeLimit(t, fake)
	return fake
}

func runRequestedMaintenance(t *testing.T) bool {
	t.Helper()
	select {
	case <-maintainLoopRequested:
		return maintainLoopForced()
	default:
		t.Fatal("saved native target did not wake the daemon loop")
		return false
	}
}

func TestNativeChargeNowReappliesTargetOnce(t *testing.T) {
	for _, target := range []int{67, 99} {
		t.Run(strconv.Itoa(target), func(t *testing.T) {
			file, path := useTempConfig(t)
			file.SetUpperLimit(target)
			lower := file.LowerLimit()
			fake := useExactNativeTargets(t)
			fake.limit, fake.enabled = target, true
			stubAdapter(t, true)
			stubBatteryCharge(t, target-1)
			response := postChargeOnce(chargeOnceLimitPath)
			want := []int{target}
			if response.Code != http.StatusCreated || file.ChargeOnceTarget() != target || !reflect.DeepEqual(fake.setCalls, want) || fake.limit != target || !fake.enabled {
				t.Fatalf("native reapply failed: %d %s native=%+v", response.Code, response.Body.String(), fake)
			}
			reloaded, err := config.NewFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.UpperLimit() != target || reloaded.LowerLimit() != lower || reloaded.ChargeOnceTarget() != target {
				t.Fatal("native reapply changed the saved charge limits")
			}
			if !maintainLoopForced() || !reflect.DeepEqual(fake.setCalls, want) {
				t.Fatal("maintenance reapplied an unchanged target", fake.setCalls)
			}
			stubBatteryCharge(t, target)
			if !completeChargeOnce(file) || !maintainLoopForced() || fake.limit != target || !reflect.DeepEqual(fake.setCalls, want) {
				t.Fatal("completion did not retain the original ceiling", fake.setCalls)
			}
		})
	}
}

func TestNativeChargeNowWriteFailureRollsBackAction(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(67)
	fake := useExactNativeTargets(t)
	fake.limit, fake.enabled = 67, true
	fake.err = errors.New("native write failed")
	stubAdapter(t, true)
	stubBatteryCharge(t, 62)
	response := postChargeOnce(chargeOnceLimitPath)
	if response.Code != http.StatusInternalServerError || file.ChargeOnceTarget() != 0 || file.UpperLimit() != 67 || fake.limit != 67 || !fake.enabled {
		t.Fatalf("failed reapply changed the target or left a running action: %d %s backend=%+v", response.Code, response.Body.String(), fake)
	}
}

func TestNativeChargeNowAfterShutdownDoesNotWrite(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(67)
	fake := useExactNativeTargets(t)
	previous := chargeControlStopped
	t.Cleanup(func() { chargeControlStopped = previous })
	chargeControlStopped = true
	stubAdapter(t, true)
	stubBatteryCharge(t, 62)
	response := postChargeOnce(chargeOnceLimitPath)
	if response.Code != http.StatusInternalServerError || len(fake.setCalls) != 0 || file.ChargeOnceTarget() != 0 {
		t.Fatalf("shutdown accepted or applied a one-time charge: %d %s writes=%v", response.Code, response.Body.String(), fake.setCalls)
	}
}

func TestNativeRestartRetainsChargeNow(t *testing.T) {
	for _, target := range []int{55, 100} {
		t.Run(strconv.Itoa(target), func(t *testing.T) {
			file, path := useTempConfig(t)
			file.SetUpperLimit(55)
			file.SetChargeOnceTarget(target)
			if err := file.Save(); err != nil {
				t.Fatal(err)
			}
			fake := useExactNativeTargets(t)
			stubBatteryCharge(t, 52)
			disableUnsupportedConfiguredFeatures()
			reloaded, err := config.NewFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if file.ChargeOnceTarget() != target || reloaded.ChargeOnceTarget() != target {
				t.Fatal("restart discarded the one-time target")
			}
			conf = reloaded
			resumeNativeChargeOnce()
			if !maintainLoopForced() || (target == 55 && (!fake.enabled || fake.limit != 55 || !reflect.DeepEqual(fake.setCalls, []int{55}))) || (target == 100 && fake.enabled) {
				t.Fatal("recovery did not restart the saved native target", fake)
			}
		})
	}
}

func TestNativeRestartDoesNotReapplyCompletedOrUnknownCharge(t *testing.T) {
	for _, tt := range []struct {
		name       string
		saveErr    error
		readErr    error
		wantTarget int
	}{
		{name: "completed"},
		{name: "save failed", saveErr: errors.New("save failed"), wantTarget: 67},
		{name: "read failed", readErr: errors.New("charge unavailable"), wantTarget: 67},
	} {
		t.Run(tt.name, func(t *testing.T) {
			configured := &mockConf{upper: 67, chargeOnceTarget: 67, saveErr: tt.saveErr}
			useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
			fake := useExactNativeTargets(t)
			fake.limit, fake.enabled = 67, true
			stubBatteryCharge(t, 67)
			smcGetBatteryCharge = func() (int, error) { return 67, tt.readErr }
			resumeNativeChargeOnce()
			if configured.ChargeOnceTarget() != tt.wantTarget || len(fake.setCalls) != 0 {
				t.Fatalf("restart target=%d, native writes=%v", configured.ChargeOnceTarget(), fake.setCalls)
			}
		})
	}
}

func TestNativeFullChargeRestoresTargetAt100Percent(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	fake := useExactNativeTargets(t)
	stubAdapter(t, true)
	stubBatteryCharge(t, 52)
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/charge-once/full", nil))
	if response.Code != http.StatusCreated || file.ChargeOnceTarget() != 100 || fake.enabled {
		t.Fatalf("full charge failed: %d %s", response.Code, response.Body.String())
	}
	stubBatteryCharge(t, 100)
	if !completeChargeOnce(file) || !maintainLoopForced() || !fake.enabled || fake.limit != 55 {
		t.Fatal("reaching 100% did not restore the configured target")
	}
}
