package daemon

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestNativeDisableReleasesStaleRegistration(t *testing.T) {
	native := &fakeNativeLimit{limit: 100, registered: true, exact: true}
	f := newFixture(t, firmwareGatedNoAdapter, withNative(native), withCapabilities(nativeCaps(55, 100)))
	if _, err := f.d.ensureNativeChargeLimitDisabled(); err != nil || native.registered || native.disables != 1 {
		t.Fatalf("disabled feature concealed a stale policy: %v %+v", err, native)
	}
	if _, err := f.d.ensureNativeChargeLimitDisabled(); err != nil || native.disables != 1 {
		t.Fatalf("clean release was not idempotent: %v %+v", err, native)
	}
}

func TestNativeTemporaryDisableLifecycle(t *testing.T) {
	native := &fakeNativeLimit{limit: 55, enabled: true, registered: true}
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withNative(native), withCapabilities(nativeCaps(55, 100)))
	f.file.SetUpperLimit(55)
	r := f.put("/disable", `"1h"`)
	if r.Code != http.StatusCreated || native.enabled || native.registered || f.file.UpperLimit() != 100 {
		t.Fatalf("temporary release failed: %d %s %+v", r.Code, r.Body.String(), native)
	}
	reloaded := f.reloadFile()
	if reloaded.PreDisableLimit() != 55 || reloaded.DisableUntil().IsZero() {
		t.Fatal("restore intent not persisted")
	}
	f.d.conf = reloaded // daemon restart with the saved temporary-disable state
	if !f.d.maintainManagedChargeLimit() || native.enabled {
		t.Fatal("restart re-enabled too early")
	}
	if !f.d.restoreDisabledLimit(reloaded.DisableUntil().Add(time.Second)) {
		t.Fatal("expiry not restored")
	}
	if !f.d.maintainManagedChargeLimit() || !native.enabled || native.limit != 55 {
		t.Fatal("expiry failed to restore exact 55%")
	}
}

func TestNativeTemporaryDisableReportsReleaseFailure(t *testing.T) {
	f := newExactNativeFixture(t)
	f.file.SetUpperLimit(55)
	f.native.limit, f.native.enabled, f.native.err = 55, true, errors.New("release failed")
	r := f.put("/disable", `"1h"`)
	if r.Code != http.StatusInternalServerError || f.file.UpperLimit() != 100 || f.file.PreDisableLimit() != 55 {
		t.Fatalf("failure hidden or retry intent lost: %d %s", r.Code, r.Body.String())
	}
	f.native.err = nil
	if !f.d.maintainManagedChargeLimit() || f.native.enabled {
		t.Fatal("release not retried")
	}
}

type blockingNativeController struct {
	*fakeNativeLimit
	entered, proceed chan struct{}
	once             sync.Once
}

func (b *blockingNativeController) Limit() (int, bool, error) {
	b.once.Do(func() { close(b.entered); <-b.proceed })
	return b.fakeNativeLimit.Limit()
}

func TestNativeShutdownDoesNotReapplyLimit(t *testing.T) {
	f := newExactNativeFixture(t)
	f.file.SetUpperLimit(55)
	b := &blockingNativeController{fakeNativeLimit: f.native, entered: make(chan struct{}), proceed: make(chan struct{})}
	f.d.native = b
	done := make(chan bool, 1)
	go func() { done <- f.d.maintainLoopForced() }()
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- f.d.stopChargeControl() }()
	close(b.proceed)
	if !<-done {
		t.Fatal("in-flight maintenance failed")
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if f.native.enabled {
		t.Fatal("shutdown left native limit active")
	}
	writes := len(f.native.setCalls)
	if !f.d.maintainLoopForced() || f.native.enabled || len(f.native.setCalls) != writes {
		t.Fatal("queued maintenance reapplied a released limit")
	}
}
