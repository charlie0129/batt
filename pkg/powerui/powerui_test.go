package powerui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// noSleep keeps registration polling from slowing the tests down.
func noSleep(time.Duration) {}

type fakeOperations struct {
	custom, enabled, agentEnabled bool
	target                        int
	agentTarget                   int
	calls                         []string
	sliderTargets                 []int
	writeTargets                  []int
	fail                          string
	ignoreWrite                   bool
	ignoreReload                  bool
	registered, ignoreDisable     bool
	reads                         int
	registeredTargets             []int
	ignoreRegistration            bool
	registrationReads             int
	registrationDelay             int
}

func (f *fakeOperations) Supported() bool                 { return true }
func (f *fakeOperations) CustomLimits() bool              { return f.custom }
func (f *fakeOperations) AvailableLimits() ([]int, error) { return []int{100, 80, 90}, nil }
func (f *fakeOperations) Limit() (int, bool, error) {
	if f.fail == "agent" || (f.fail == "agentReadback" && len(f.writeTargets) > 0) {
		return 0, false, errors.New("XPC unavailable")
	}
	return f.agentTarget, f.agentEnabled, nil
}
func (f *fakeOperations) Preferences() (int, bool, error) {
	f.reads++
	if f.fail == "read" || (f.fail == "readback" && f.reads > 1) {
		return 0, false, errors.New("preferences unavailable")
	}
	return f.target, f.enabled, nil
}
func (f *fakeOperations) Enable() error {
	f.calls = append(f.calls, "enable")
	if f.fail == "enable" {
		return errors.New("enable rejected")
	}
	f.agentEnabled = true
	f.target, f.enabled = 80, true // Apple's enable API also resets the target.
	f.agentTarget = 80
	f.registered = true
	return nil
}
func (f *fakeOperations) WritePreferences(target int) error {
	f.calls = append(f.calls, "write")
	f.writeTargets = append(f.writeTargets, target)
	if f.fail == "write" {
		return errors.New("flush/notification failed")
	}
	if !f.ignoreWrite {
		f.target, f.enabled = target, true
	}
	if !f.ignoreWrite && !f.ignoreReload {
		f.agentTarget, f.agentEnabled = target, true
		f.registered = true
		if !f.ignoreRegistration {
			f.registeredTargets = nil
		}
	}
	return nil
}
func (f *fakeOperations) SetSliderLimit(target int) error {
	f.sliderTargets = append(f.sliderTargets, target)
	return nil
}
func (f *fakeOperations) Disable() error {
	f.calls = append(f.calls, "disable")
	if f.fail == "disable" {
		return errors.New("disable rejected")
	}
	if !f.ignoreDisable {
		f.enabled, f.agentEnabled, f.registered = false, false, false
	}
	return nil
}

func (f *fakeOperations) ManualLimitRegistered() (bool, error) {
	if f.fail == "registration" {
		return false, errors.New("registration read failed")
	}
	return f.registered, nil
}

func (f *fakeOperations) RegisteredManualLimits() ([]int, error) {
	f.registrationReads++
	if f.fail == "registration" || (f.fail == "registrationReadback" && len(f.writeTargets) > 0) {
		return nil, errors.New("registration read failed")
	}
	if len(f.writeTargets) > 0 && f.registrationDelay > 0 {
		f.registrationDelay--
		return nil, nil
	}
	if f.registeredTargets != nil {
		return f.registeredTargets, nil
	}
	if f.registered {
		return []int{f.agentTarget}, nil
	}
	return nil, nil
}

func TestExactNativeTargetAfterDisabledState(t *testing.T) {
	f := &fakeOperations{custom: true, target: 100}
	c := &Client{api: f, sleep: noSleep}
	if err := c.SetLimit(69); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"enable", "write"}) || len(f.sliderTargets) != 0 {
		t.Fatalf("sub-80 target must re-engage then use preferences, calls=%v slider=%v", f.calls, f.sliderTargets)
	}
	limit, enabled, err := c.Limit()
	if err != nil || limit != 69 || !enabled {
		t.Fatalf("exact read-back: %d %t %v", limit, enabled, err)
	}
	if err := c.SetLimit(69); err != nil {
		t.Fatal(err)
	}
	if len(f.writeTargets) != 1 {
		t.Fatalf("unchanged target rewritten: %v", f.writeTargets)
	}
	if err := c.SetLimit(61); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.writeTargets, []int{69, 61}) {
		t.Fatal(f.writeTargets)
	}
	if !reflect.DeepEqual(f.calls, []string{"enable", "write", "write"}) {
		t.Fatalf("changing an enabled target must not reset it to Apple's 80%% default: %v", f.calls)
	}
}

func TestNativeStaleEnabledPreferenceReengagesAgent(t *testing.T) {
	f := &fakeOperations{custom: true, target: 69, enabled: true, agentEnabled: false}
	c := &Client{api: f, sleep: noSleep}
	if _, enabled, err := c.Limit(); err != nil || enabled {
		t.Fatalf("stale preference reported enabled: %t %v", enabled, err)
	}
	if err := c.SetLimit(69); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"enable", "write"}) {
		t.Fatal(f.calls)
	}
}

func TestNativeStaleAgentTargetIsRetriedWithoutResettingHold(t *testing.T) {
	f := &fakeOperations{custom: true, target: 55, enabled: true,
		agentTarget: 70, agentEnabled: true, ignoreReload: true}
	c := &Client{api: f, sleep: noSleep}
	for range 2 {
		if target, enabled, err := c.Limit(); err != nil || !enabled || target != 0 {
			t.Fatalf("pending target must remain enabled and require reconciliation: target=%d enabled=%t err=%v", target, enabled, err)
		}
		if err := c.SetLimit(55); err == nil || !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("stale agent reported success: %v", err)
		}
	}
	if !reflect.DeepEqual(f.calls, []string{"write", "write"}) || f.agentTarget != 70 {
		t.Fatalf("pending target was not retried or existing hold was reset: %+v", f)
	}
	f.ignoreReload = false
	if err := c.SetLimit(55); err != nil {
		t.Fatal(err)
	}
	if target, loaded, err := c.Limit(); err != nil || !loaded || target != 55 {
		t.Fatalf("exact target did not load: %d %t %v", target, loaded, err)
	}
	if err := c.SetLimit(55); err != nil || len(f.calls) != 3 {
		t.Fatalf("unchanged loaded target should not be rewritten: %v %v", f.calls, err)
	}
}

func TestStaleDisabledPreferencesDoNotHideActiveAgent(t *testing.T) {
	f := &fakeOperations{custom: true, target: 100, agentTarget: 55, agentEnabled: true}
	c := &Client{api: f, sleep: noSleep}
	if target, enabled, err := c.Limit(); err != nil || !enabled || target != 0 {
		t.Fatalf("active agent hidden by disabled preference: %d %t %v", target, enabled, err)
	}
	if err := c.Disable(); err != nil || !reflect.DeepEqual(f.calls, []string{"disable"}) {
		t.Fatalf("stale registration not released directly: %v %v", f.calls, err)
	}
}

func TestEveryExactNativeTarget(t *testing.T) {
	// These are software checks, not a claim that every percentage was tested
	// on a real battery. In particular, no test may cycle the host battery.
	for target := 10; target < 100; target++ {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/enabled=%t", target, enabled), func(t *testing.T) {
				previous := 80
				if target == previous {
					previous = 85
				}
				f := &fakeOperations{custom: true, target: previous, agentTarget: previous, enabled: enabled, agentEnabled: enabled}
				c := &Client{api: f, sleep: noSleep}
				if err := c.SetLimit(target); err != nil {
					t.Fatal(err)
				}
				got, on, err := c.Limit()
				if err != nil || !on || got != target || len(f.sliderTargets) != 0 {
					t.Fatalf("exact %d%% target lost: got=%d enabled=%t err=%v slider=%v", target, got, on, err, f.sliderTargets)
				}
				if !reflect.DeepEqual(f.writeTargets, []int{target}) {
					t.Fatalf("unexpected intermediate target: %v", f.writeTargets)
				}
				want := []string{"write"}
				if !enabled {
					want = []string{"enable", "write"}
				}
				if !reflect.DeepEqual(f.calls, want) {
					t.Fatalf("enabled hold was reset: %v", f.calls)
				}
			})
		}
	}
}

func TestNativeWriteFailuresAreNotSuccess(t *testing.T) {
	for _, stage := range []string{"read", "agent", "enable", "write", "readback", "ignored", "agentReadback", "registrationReadback"} {
		t.Run(stage, func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 100, fail: stage, ignoreWrite: stage == "ignored"}
			if err := (&Client{api: f, sleep: noSleep}).SetLimit(69); err == nil {
				t.Fatal("unconfirmed write reported success")
			}
			if (stage == "read" || stage == "agent" || stage == "enable") && len(f.writeTargets) > 0 {
				t.Fatal("wrote after failed precondition")
			}
		})
	}
}

func TestNativeReleaseUsesAgentAndHandlesStaleDisabledState(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		f := &fakeOperations{custom: true, target: 69, enabled: enabled, agentEnabled: enabled, registered: true}
		if err := (&Client{api: f, sleep: noSleep}).SetLimit(100); err != nil {
			t.Fatal(err)
		}
		want := []string{"disable"}
		if !enabled {
			want = []string{"enable", "disable"}
		}
		if !reflect.DeepEqual(f.calls, want) || len(f.writeTargets) > 0 {
			t.Fatalf("release: %+v", f)
		}
	}
	f := &fakeOperations{custom: true, agentEnabled: true, fail: "disable"}
	if err := (&Client{api: f, sleep: noSleep}).Disable(); err == nil {
		t.Fatal("disable failure hidden")
	}
}

func TestNativeLimitsAndOlderSystemFallback(t *testing.T) {
	for _, custom := range []bool{true, false} {
		f := &fakeOperations{custom: custom}
		c := &Client{api: f, sleep: noSleep}
		if c.SupportsExactTargets() != custom {
			t.Fatal("exact native targets require the preference path")
		}
		limits, err := c.AvailableLimits()
		if err != nil {
			t.Fatal(err)
		}
		if custom {
			if len(limits) != 91 || limits[0] != 10 || limits[59] != 69 || limits[90] != 100 {
				t.Fatal(limits)
			}
		} else {
			if !reflect.DeepEqual(limits, []int{80, 90, 100}) {
				t.Fatal(limits)
			}
			if err := c.SetLimit(90); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.sliderTargets, []int{90}) || len(f.calls) > 0 {
				t.Fatal(f)
			}
		}
		for _, target := range []int{-1, 0, 9, 101} {
			if err := c.SetLimit(target); err == nil {
				t.Fatalf("accepted %d", target)
			}
		}
	}
}

func TestNativeDisableLifecycle(t *testing.T) {
	for _, target := range []int{10, 55, 70, 99} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 100}
			c := &Client{api: f, sleep: noSleep}
			for range 2 {
				if err := c.SetLimit(target); err != nil || !f.registered {
					t.Fatalf("enable: %v registered=%t", err, f.registered)
				}
				if err := c.SetLimit(100); err != nil || f.registered || f.agentEnabled || f.enabled {
					t.Fatalf("disable left control active: err=%v state=%+v", err, f)
				}
				calls := len(f.calls)
				if err := c.Disable(); err != nil || len(f.calls) != calls {
					t.Fatalf("clean disable must not re-enable an 80%% limit: %v %v", f.calls, err)
				}
			}
		})
	}
}

func TestNativeDisableChecksPolicyAndQueuedSettings(t *testing.T) {
	for _, tc := range []struct {
		name              string
		saved, registered bool
	}{
		{"stale registration", false, true},
		{"pending preference reload", true, false},
		{"both stale", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 55, enabled: tc.saved, registered: tc.registered}
			if err := (&Client{api: f, sleep: noSleep}).Disable(); err != nil {
				t.Fatal(err)
			}
			if f.registered || f.enabled || f.agentEnabled || !reflect.DeepEqual(f.calls, []string{"enable", "disable"}) {
				t.Fatalf("release skipped stale/pending state: %+v", f)
			}
		})
	}
}

func TestNativeDisableDoesNotClaimUnconfirmedRelease(t *testing.T) {
	for _, stage := range []string{"registration", "ignored disable", "enable", "disable"} {
		t.Run(stage, func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 55, registered: true, fail: stage,
				ignoreDisable: stage == "ignored disable"}
			if err := (&Client{api: f, sleep: noSleep}).Disable(); err == nil {
				t.Fatalf("unconfirmed release reported success: %+v", f)
			}
		})
	}
}

func TestNativeRegistrationRetriesUntilConfirmed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		registered []int
	}{
		{"missing", []int{}},
		{"stale", []int{70}},
		{"stale mirror", []int{55, 70}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 55, enabled: true,
				agentTarget: 55, agentEnabled: true, registeredTargets: tc.registered,
				ignoreRegistration: true}
			var slept time.Duration
			c := &Client{api: f, sleep: func(d time.Duration) { slept += d }}
			if target, on, err := c.Limit(); err != nil || !on || target != 0 {
				t.Fatalf("agent agreement concealed missing/stale powerd limit: %d %t %v", target, on, err)
			}
			if err := c.SetLimit(55); err == nil || !strings.Contains(err.Error(), "not confirmed") {
				t.Fatalf("unconfirmed registration reported success: %v", err)
			}
			if want := (registrationChecks - 1) * registrationCheckInterval; slept != want {
				t.Fatalf("waited %v for registration, want %v", slept, want)
			}
			for range 2 {
				if target, on, err := c.Limit(); err != nil || !on || target != 0 {
					t.Fatalf("pending registration reported confirmed: %d %t %v", target, on, err)
				}
				if err := c.SetLimit(55); err == nil || !strings.Contains(err.Error(), "not confirmed") {
					t.Fatalf("unconfirmed registration reported success: %v", err)
				}
			}
			if !reflect.DeepEqual(f.calls, []string{"write", "write", "write"}) {
				t.Fatalf("unconfirmed target was not retried on every call: %v", f.calls)
			}
			f.registeredTargets = []int{55, 55}
			if target, on, err := c.Limit(); err != nil || !on || target != 55 {
				t.Fatalf("matching agent and mirror not confirmed: %d %t %v", target, on, err)
			}
			if err := c.SetLimit(55); err != nil || len(f.calls) != 3 {
				t.Fatal(f.calls, err)
			}
			// Drift after confirmation also repairs immediately.
			f.registeredTargets = []int{}
			if err := c.SetLimit(55); err == nil || len(f.calls) != 4 {
				t.Fatal(f.calls, err)
			}
		})
	}
}

func TestNativePendingTargetCanChangeOrReleaseImmediately(t *testing.T) {
	f := &fakeOperations{custom: true, target: 55, enabled: true,
		agentTarget: 55, agentEnabled: true, registeredTargets: []int{}, ignoreRegistration: true}
	c := &Client{api: f, sleep: noSleep}
	if err := c.SetLimit(55); err == nil {
		t.Fatal("unconfirmed registration reported success")
	}
	if err := c.SetLimit(52); err == nil {
		t.Fatal("unconfirmed registration reported success")
	}
	if !reflect.DeepEqual(f.writeTargets, []int{55, 52}) {
		t.Fatal(f.writeTargets)
	}
	if err := c.Disable(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLimit(52); err == nil {
		t.Fatal("unconfirmed registration reported success")
	}
	if !reflect.DeepEqual(f.calls, []string{"write", "write", "disable", "enable", "write"}) {
		t.Fatal(f.calls)
	}
}

func TestNativeWriteConfirmsRegistrationBeforeReturning(t *testing.T) {
	for _, delay := range []int{0, 2} {
		t.Run(fmt.Sprintf("registrationDelay=%d", delay), func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 100, registrationDelay: delay}
			if err := (&Client{api: f, sleep: noSleep}).SetLimit(55); err != nil {
				t.Fatal(err)
			}
			if f.registrationReads != delay+1 || !reflect.DeepEqual(f.writeTargets, []int{55}) {
				t.Fatalf("write returned without confirming registration or rewrote while waiting: %+v", f)
			}
		})
	}
}

func TestNativeRegistrationReadFailureIsNotConfirmation(t *testing.T) {
	f := &fakeOperations{custom: true, target: 55, enabled: true,
		agentTarget: 55, agentEnabled: true, fail: "registration"}
	c := &Client{api: f, sleep: noSleep}
	if _, _, err := c.Limit(); err == nil {
		t.Fatal("registration failure hidden")
	}
	if err := c.SetLimit(55); err == nil || len(f.calls) != 0 {
		t.Fatal("failed check caused a blind rewrite", f.calls, err)
	}
}

func TestSliderNativeDoesNotUseRegistration(t *testing.T) {
	f := &fakeOperations{agentTarget: 80, agentEnabled: true, fail: "registration"}
	c := &Client{api: f, sleep: noSleep}
	if target, on, err := c.Limit(); err != nil || !on || target != 80 {
		t.Fatal(target, on, err)
	}
	for range 2 {
		if err := c.SetLimit(90); err != nil {
			t.Fatal(err)
		}
	}
	if f.registrationReads != 0 || !reflect.DeepEqual(f.sliderTargets, []int{90, 90}) {
		t.Fatal("slider backend entered exact-native reconciliation", f)
	}
}
