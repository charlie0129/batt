package daemon

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

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
		{"minimum native target", 8, 10, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExactNativeFixture(t, withCharge(tc.charge))
			f.native.limit, f.native.enabled = tc.previous, tc.previous < 100
			r := f.put("/limit", strconv.Itoa(tc.target))
			if r.Code != http.StatusCreated || f.native.limit != tc.target || !f.native.enabled {
				t.Fatalf("limit not applied: %d %s native=%+v", r.Code, r.Body.String(), f.native)
			}
			wantMessage := fmt.Sprintf("set charging limit to %d%%, current charge: %d%%. macOS resumes charging once the battery drops about 5%% below the limit.", tc.target, tc.charge)
			if tc.charge > tc.target {
				wantMessage += " Current charge is above the limit; macOS may use battery power to discharge toward the target."
			}
			if strings.TrimSpace(r.Body.String()) != strconv.Quote(wantMessage) || f.file.UpperLimit() != tc.target {
				t.Fatalf("unexpected native limit response or saved target: %s target=%d", r.Body.String(), f.file.UpperLimit())
			}
			f.d.conf = f.reloadFile() // daemon restart
			if !f.d.maintainLoopForced() || f.native.limit != tc.target || !f.native.enabled || f.native.disables != 0 {
				t.Fatal("restart changed or released the configured target")
			}
		})
	}
}

func TestNativeTargetDoesNotFollowUnpluggedBattery(t *testing.T) {
	f := newExactNativeFixture(t, withPluggedIn(false), withAdapter(false))
	f.file.SetUpperLimit(55)
	f.file.SetLowerLimit(50)
	for _, charge := range []int{52, 51, 49, 8} {
		f.smc.setCharge(t, charge)
		if !f.d.maintainLoopForced() || f.native.limit != 55 {
			t.Fatalf("unplugged battery at %d%% changed the target to %d%%", charge, f.native.limit)
		}
	}
	f.smc.setPluggedIn(t, true)
	f.smc.setAdapter(t, true)
	if !f.d.maintainLoopForced() || f.native.limit != 55 || len(f.native.setCalls) != 1 {
		t.Fatal("reconnect changed or rewrote the target")
	}
}

func TestNativeLowerLimitIsUnsupported(t *testing.T) {
	f := newExactNativeFixture(t)
	f.file.SetUpperLimit(55)
	f.file.SetLowerLimit(50)
	r := f.put("/lower-limit-delta", "10")
	if r.Code != http.StatusConflict || f.file.LowerLimit() != 50 || len(f.native.setCalls) != 0 {
		t.Fatalf("native lower limit accepted: %d %s", r.Code, r.Body.String())
	}
}

func TestNativeMinimumTargetIgnoresSavedLowerLimitDelta(t *testing.T) {
	f := newExactNativeFixture(t)
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(30)
	r := f.put("/limit", "10")
	if r.Code != http.StatusCreated || f.native.limit != 10 || f.file.UpperLimit() != 10 {
		t.Fatalf("saved delta blocked minimum native target: %d %s", r.Code, r.Body.String())
	}
}

func TestNativeMinimumTargetSaveFailureRestoresLimits(t *testing.T) {
	f := newExactNativeFixture(t)
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(30)
	if err := os.Mkdir(f.path, 0755); err != nil {
		t.Fatal(err)
	}
	r := f.put("/limit", "10")
	if r.Code != http.StatusInternalServerError || f.file.UpperLimit() != 80 || f.file.LowerLimit() != 30 || len(f.native.setCalls) != 0 {
		t.Fatalf("failed save changed limits: %d %s config=%d/%d", r.Code, r.Body.String(), f.file.UpperLimit(), f.file.LowerLimit())
	}
}
