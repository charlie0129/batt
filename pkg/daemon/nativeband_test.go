package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/config"
)

func useNativeBand(t *testing.T, battery nativeBatteryState) (*fakeNativeLimit, *nativeBatteryState) {
	t.Helper()
	gatedSMCNoAdapter(t)
	useNativeCapabilities(t)
	capabilities.NativeChargeBand = true
	fake := &fakeNativeLimit{limit: 100, band: true}
	useFakeNativeLimit(t, fake)
	previousRead := readNativeBatteryState
	previousBand := nativeBand
	resetNativeBand()
	t.Cleanup(func() {
		readNativeBatteryState = previousRead
		nativeBand = previousBand
	})
	readNativeBatteryState = func() (nativeBatteryState, error) { return battery, nil }
	return fake, &battery
}

func TestNativeBandFiftyToFiftyFive(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake, battery := useNativeBand(t, nativeBatteryState{charge: 52, plugged: true})
	// Start paused inside the band. A lower hold target prevents an automatic
	// top-up toward 55. Natural drift must not be a forced adapter discharge.
	for _, step := range []struct {
		charge, target int
	}{
		{52, 52}, {51, 51}, {50, 50}, // lower boundary is exclusive, like legacy
		{49, 55}, {50, 55}, {52, 55}, {54, 55}, // keep the charging intent
		{55, 55}, {54, 54}, {52, 52}, {50, 50}, {49, 55},
	} {
		battery.charge = step.charge
		if !maintainLoopForced() || fake.limit != step.target {
			t.Fatalf("at %d%%: target=%d want=%d", step.charge, fake.limit, step.target)
		}
	}
}

func TestNativeBandPreservesChargingAndUnplugState(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake, battery := useNativeBand(t, nativeBatteryState{charge: 52, charging: true, plugged: true})
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("restart interrupted an existing charge")
	}
	battery.charging = false // physical state can lag a requested charge
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("delayed native update lost charging intent")
	}
	battery.plugged = false
	if !maintainLoopForced() || fake.limit != 52 {
		t.Fatal("unplug did not end the charge session")
	}
	battery.plugged = true
	if !maintainLoopForced() || fake.limit != 52 {
		t.Fatal("reconnect started a top-up inside the band")
	}
	resetNativeBand()
	if !maintainLoopForced() || fake.limit != 52 {
		t.Fatal("restart started a top-up from a paused battery")
	}
}

func TestNativeBandAboveUpperDoesNotRequestDrainToLower(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake, _ := useNativeBand(t, nativeBatteryState{charge: 70, plugged: true})
	if !maintainLoopForced() || fake.limit != 70 {
		t.Fatal("above-upper battery must be held at its observed level")
	}
	if !maintainLoopForced() || !reflect.DeepEqual(fake.setCalls, []int{70}) {
		t.Fatal("unchanged hold was rewritten")
	}
}

func TestNativeBandDoesNotChaseOvershoot(t *testing.T) {
	state := nativeBandState{}
	for _, step := range []struct{ charge, target int }{{49, 55}, {54, 55}, {56, 55}, {57, 55}, {55, 55}, {54, 54}, {55, 54}} {
		target, err := state.target(50, 55, nativeBatteryState{charge: step.charge, plugged: true})
		if err != nil || target != step.target {
			t.Fatalf("at %d%%: target=%d want=%d err=%v", step.charge, target, step.target, err)
		}
	}
}

func TestNativeBandLowAndFullHolds(t *testing.T) {
	for _, charge := range []int{1, 8, 9, 99, 100} {
		state := nativeBandState{}
		target, err := state.target(0, 10, nativeBatteryState{charge: charge, plugged: true})
		if err != nil || target != charge {
			t.Fatalf("hold at %d%% raised or lowered the observed target: %d %v", charge, target, err)
		}
	}
}

func TestNativeBandErrorsAndDisable(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake, battery := useNativeBand(t, nativeBatteryState{charge: 49, plugged: true})
	fake.err = errors.New("native update failed")
	if maintainLoopForced() {
		t.Fatal("failed native update reported success")
	}
	battery.charge = 52
	fake.err = nil
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("failed charging request was not retried inside the band")
	}
	file.SetUpperLimit(100)
	if !maintainLoopForced() || fake.enabled {
		t.Fatal("disable left the native hold active")
	}
	file.SetUpperLimit(55)
	if !maintainLoopForced() || fake.limit != 52 {
		t.Fatal("re-enable reused the disabled charge session")
	}
	writes := len(fake.setCalls)
	readNativeBatteryState = func() (nativeBatteryState, error) { return nativeBatteryState{}, errors.New("telemetry unavailable") }
	if maintainLoopForced() || len(fake.setCalls) != writes {
		t.Fatal("failed telemetry must not alter the current policy")
	}
}

func TestNativeBandLowerLimitRoute(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	fake, battery := useNativeBand(t, nativeBatteryState{charge: 52, plugged: true})
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader("5")))
	if response.Code != http.StatusCreated || file.LowerLimit() != 50 || fake.limit != 52 {
		t.Fatalf("50–55 band rejected or ignored: %d %s", response.Code, response.Body.String())
	}
	battery.charge = 49
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("saved lower limit was not used")
	}
}

func TestNativeBandTemporaryDisableAndRestore(t *testing.T) {
	file, path := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake, battery := useNativeBand(t, nativeBatteryState{charge: 49, plugged: true})
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("initial charge request failed")
	}
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/disable", strings.NewReader(`"1h"`)))
	if response.Code != http.StatusCreated || fake.enabled {
		t.Fatalf("temporary disable failed: %d %s", response.Code, response.Body.String())
	}
	invalidDelta := httptest.NewRecorder()
	setupRoutes().ServeHTTP(invalidDelta, httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader("95")))
	if invalidDelta.Code != http.StatusBadRequest || file.UpperLimit()-file.LowerLimit() != 5 {
		t.Fatal("temporary disable accepted a delta that cannot restore the saved upper limit")
	}
	reloaded, err := config.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conf = reloaded
	resetNativeBand() // daemon restart with the pending timer
	if !maintainLoopForced() || fake.enabled {
		t.Fatal("restart reapplied the band before expiry")
	}
	battery.charge = 52
	if !restoreDisabledLimit(reloaded, reloaded.DisableUntil().Add(time.Second)) ||
		!maintainLoopForced() || reloaded.UpperLimit() != 55 || reloaded.LowerLimit() != 50 || fake.limit != 52 {
		t.Fatal("expiry lost the band or resumed a paused battery inside it")
	}
	battery.charge = 49
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("restored band ignored its lower threshold")
	}
}

func TestNativeBandRejectsBadTelemetryAndRetriesLowerLimit(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	fake, battery := useNativeBand(t, nativeBatteryState{charge: -1, plugged: true})
	if maintainLoopForced() || len(fake.setCalls) != 0 {
		t.Fatal("invalid telemetry changed the native target")
	}
	battery.charge = 49
	fake.err = errors.New("apply failed")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader("5")))
	if response.Code != http.StatusInternalServerError || file.LowerLimit() != 50 {
		t.Fatalf("apply failure hidden or saved band lost: %d %s", response.Code, response.Body.String())
	}
	fake.err = nil
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("saved band was not retried")
	}
}

func TestEveryNativeBandTarget(t *testing.T) {
	for upper := 10; upper < 100; upper++ {
		for lower := 0; lower < upper; lower++ {
			state := nativeBandState{}
			battery := nativeBatteryState{charge: upper, plugged: true}
			target, err := state.target(lower, upper, battery)
			if err != nil || target != upper || state.charging {
				t.Fatalf("hold %d–%d: %d %v", lower, upper, target, err)
			}
			if lower == 0 {
				continue
			}
			battery.charge = lower - 1
			target, err = state.target(lower, upper, battery)
			if err != nil || target != upper || !state.charging {
				t.Fatalf("resume %d–%d: %d %v", lower, upper, target, err)
			}
			battery.charge = upper - 1
			if target, err = state.target(lower, upper, battery); err != nil || target != upper || !state.charging {
				t.Fatalf("charge inside %d–%d: %d %v", lower, upper, target, err)
			}
		}
	}
}

func TestNativeBandMinimumRangeAndInputValidation(t *testing.T) {
	file, _ := useTempConfig(t)
	fake, _ := useNativeBand(t, nativeBatteryState{charge: 9, plugged: true})
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("10")))
	if response.Code != http.StatusCreated || file.LowerLimit() != 8 || fake.limit != 9 {
		t.Fatalf("minimum band did not hold 9%% inside 8–10%%: %d %s", response.Code, response.Body.String())
	}
	for _, delta := range []string{"0", "-1", "10"} {
		recorder := httptest.NewRecorder()
		setupRoutes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader(delta)))
		if recorder.Code != http.StatusBadRequest || file.LowerLimit() != 8 {
			t.Fatalf("invalid delta %s changed the band: %d %s", delta, recorder.Code, recorder.Body.String())
		}
	}
	response = httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader("2")))
	if response.Code != http.StatusCreated || file.LowerLimit() != 8 {
		t.Fatalf("valid 8–10%% band rejected: %d %s", response.Code, response.Body.String())
	}
}
