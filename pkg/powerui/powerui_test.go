package powerui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type fakeOperations struct {
	custom, enabled, registered     bool
	target                          int
	manual, ignoreManual            bool
	registeredTargets               []int
	writes, sliders                 []int
	disables                        int
	refreshes                       int
	fail                            string
	ignoreWrite, ignoreRegistration bool
	registrationDelay               int
}

func (f *fakeOperations) Supported() bool                 { return true }
func (f *fakeOperations) CustomLimits() bool              { return f.custom }
func (f *fakeOperations) AvailableLimits() ([]int, error) { return []int{100, 80, 90}, nil }
func (f *fakeOperations) Limit() (int, bool, error)       { return f.target, f.enabled, nil }
func (f *fakeOperations) Disable() error                  { f.disables++; f.enabled = false; return nil }
func (f *fakeOperations) RefreshBattery() error {
	f.refreshes++
	if f.fail == "refresh" {
		return errors.New("refresh rejected")
	}
	return nil
}
func (f *fakeOperations) SetSliderLimit(target int) error {
	f.sliders = append(f.sliders, target)
	return nil
}
func (f *fakeOperations) Policy() (int, bool, error) {
	if f.fail == "read" || (f.fail == "readback" && len(f.writes) > 0) {
		return 0, false, errors.New("policy unavailable")
	}
	return f.target, f.enabled, nil
}
func (f *fakeOperations) SetPolicy(target int) error {
	f.writes = append(f.writes, target)
	if f.fail == "write" {
		return errors.New("write/reload failed")
	}
	if f.ignoreWrite {
		return nil
	}
	f.target, f.enabled = target, target < 100
	f.registered = target < 100
	if target < 100 && !f.ignoreManual {
		f.manual = false
	}
	if !f.ignoreRegistration {
		f.registeredTargets = nil
	}
	return nil
}
func (f *fakeOperations) Registrations() ([]int, bool, error) {
	if f.fail == "registration" || (f.fail == "registrationReadback" && len(f.writes) > 0) {
		return nil, false, errors.New("registration unavailable")
	}
	if len(f.writes) > 0 && f.registrationDelay > 0 {
		f.registrationDelay--
		return nil, f.manual, nil
	}
	if f.registeredTargets != nil {
		return f.registeredTargets, f.manual, nil
	}
	if f.registered {
		return []int{f.target}, f.manual, nil
	}
	return nil, f.manual, nil
}

func TestNativeCeilingTargets(t *testing.T) {
	targets := []int{10, 55, 99}
	for _, target := range targets {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 100}
			c := &Client{api: f}
			if err := c.SetLimit(target); err != nil {
				t.Fatal(err)
			}
			got, on, err := c.Limit()
			if err != nil || !on || got != target || len(f.sliders) > 0 || !reflect.DeepEqual(f.writes, []int{target}) {
				t.Fatalf("ceiling not applied: %+v %v", f, err)
			}
			if err := c.SetLimit(target); err != nil || !reflect.DeepEqual(f.writes, []int{target, target}) || f.refreshes != 2 {
				t.Fatalf("unchanged ceiling was not reapplied: %+v %v", f, err)
			}
		})
	}
}

func TestNativeCeilingReconcilesCompetingManualLimit(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		f := &fakeOperations{custom: true, target: 55, enabled: true, registered: true, manual: true, ignoreManual: ignored}
		c := &Client{api: f}
		if target, on, err := c.Limit(); err != nil || !on || target != 0 {
			t.Fatalf("manual conflict concealed: %d %t %v", target, on, err)
		}
		err := c.SetLimit(55)
		if (err != nil) != ignored || !reflect.DeepEqual(f.writes, []int{55}) {
			t.Fatalf("ignored=%t: writes=%v err=%v", ignored, f.writes, err)
		}
	}
}

func TestNativeCeilingReleasePreservesManualLimit(t *testing.T) {
	f := &fakeOperations{custom: true, target: 55, enabled: true, registered: true, manual: true}
	c := &Client{api: f}
	if err := c.Disable(); err != nil || !f.manual || f.enabled || f.registered {
		t.Fatalf("release changed Apple's limit or retained batt's policy: %+v %v", f, err)
	}
	if err := c.Disable(); err != nil || len(f.writes) != 1 {
		t.Fatalf("manual-only limit triggered another release: %+v %v", f, err)
	}
}

func TestNativeCeilingRepairsMissingStaleAndUnsafeRegistrations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		targets []int
	}{
		{"missing", []int{}}, {"stale", []int{70}}, {"stale mirror", []int{55, 70}},
		{"wrong flags", []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeOperations{custom: true, enabled: true, target: 55, registeredTargets: tc.targets, ignoreRegistration: true}
			c := &Client{api: f}
			if target, on, err := c.Limit(); err != nil || !on || target != 0 {
				t.Fatalf("drift concealed: %d %t %v", target, on, err)
			}
			if err := c.SetLimit(55); err == nil || !strings.Contains(err.Error(), "not confirmed") {
				t.Fatalf("unconfirmed write succeeded: %v", err)
			}
			f.registeredTargets = []int{55, 55}
			if target, on, err := c.Limit(); err != nil || !on || target != 55 {
				t.Fatal(target, on, err)
			}
			if err := c.SetLimit(55); err != nil || len(f.writes) != 2 {
				t.Fatal(f.writes, err)
			}
		})
	}
}

func TestNativeCeilingWriteFailures(t *testing.T) {
	for _, target := range []int{55, 100} {
		for _, stage := range []string{"read", "write", "readback", "registration", "registrationReadback", "ignored"} {
			t.Run(fmt.Sprintf("%d/%s", target, stage), func(t *testing.T) {
				f := &fakeOperations{custom: true, target: 100, fail: stage, ignoreWrite: stage == "ignored"}
				if target == 100 {
					f.target, f.enabled, f.registered = 55, true, true
				}
				if err := (&Client{api: f}).SetLimit(target); err == nil {
					t.Fatal("failed or ignored write succeeded")
				}
				if (stage == "read" || stage == "registration") && len(f.writes) > 0 {
					t.Fatal("wrote after failed precondition")
				}
			})
		}
	}
}

func TestNativeCeilingRelease(t *testing.T) {
	for _, tc := range []struct {
		name                string
		enabled, registered bool
	}{
		{"own policy", true, true}, {"saved but not loaded", true, false},
		{"live but not saved", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeOperations{custom: true, target: 55, enabled: tc.enabled, registered: tc.registered}
			c := &Client{api: f}
			if err := c.SetLimit(100); err != nil || f.enabled || f.registered {
				t.Fatalf("release: %+v %v", f, err)
			}
			if !reflect.DeepEqual(f.writes, []int{100}) {
				t.Fatal(f.writes)
			}
			if err := c.Disable(); err != nil || len(f.writes) != 1 {
				t.Fatal("clean release not idempotent", err)
			}
		})
	}
}

func TestNativeCeilingReleaseRequiresLiveRegistrationToDisappear(t *testing.T) {
	f := &fakeOperations{custom: true, target: 55, enabled: true, registeredTargets: []int{55}, ignoreRegistration: true}
	c := &Client{api: f}
	if err := c.Disable(); err == nil {
		t.Fatal("release succeeded while the live policy remained")
	}
	f.registeredTargets = []int{}
	if err := c.Disable(); err != nil || len(f.writes) != 1 {
		t.Fatal("released policy rewritten", f.writes, err)
	}
}

func TestNativeCeilingRegistrationDelay(t *testing.T) {
	f := &fakeOperations{custom: true, target: 100, registrationDelay: 2}
	if err := (&Client{api: f}).SetLimit(55); err != nil || len(f.writes) != 1 || f.refreshes != 1 {
		t.Fatal(f.writes, err)
	}
}

func TestTelemetryRefreshFailureDoesNotFailPolicyWrite(t *testing.T) {
	f := &fakeOperations{custom: true, target: 100, fail: "refresh"}
	c := &Client{api: f}
	if err := c.SetLimit(55); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 1 || f.refreshes != 1 {
		t.Fatal("unexpected policy writes or telemetry refreshes", f.writes, f.refreshes)
	}
}

func TestNativeLimitsAndOlderSystemFallback(t *testing.T) {
	for _, custom := range []bool{true, false} {
		f := &fakeOperations{custom: custom}
		c := &Client{api: f}
		if c.SupportsExactTargets() != custom {
			t.Fatal("wrong exact-target capability")
		}
		limits, err := c.AvailableLimits()
		if err != nil {
			t.Fatal(err)
		}
		if custom {
			if len(limits) != 91 || limits[0] != 10 || limits[90] != 100 {
				t.Fatal(limits)
			}
		} else {
			if !reflect.DeepEqual(limits, []int{80, 90, 100}) {
				t.Fatal(limits)
			}
			if err := c.SetLimit(90); err != nil || !reflect.DeepEqual(f.sliders, []int{90}) || len(f.writes) > 0 {
				t.Fatal(f, err)
			}
			if err := c.Disable(); err != nil || f.disables != 1 {
				t.Fatal(f, err)
			}
		}
		for _, target := range []int{-1, 0, 9, 101} {
			if err := c.SetLimit(target); err == nil {
				t.Fatalf("accepted %d", target)
			}
		}
	}
}
