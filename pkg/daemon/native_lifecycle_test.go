package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/config"
)

func TestNativeDisableReleasesStaleRegistration(t *testing.T) {
	useNativeCapabilities(t, 55, 100)
	f := &fakeNativeLimit{limit: 100, registered: true}
	useFakeNativeLimit(t, f)
	if _, err := ensureNativeChargeLimitDisabled(); err != nil || f.registered || f.disables != 1 {
		t.Fatalf("disabled feature concealed a stale policy: %v %+v", err, f)
	}
	if _, err := ensureNativeChargeLimitDisabled(); err != nil || f.disables != 1 {
		t.Fatalf("clean release was not idempotent: %v %+v", err, f)
	}
}

func TestNativeTemporaryDisableLifecycle(t *testing.T) {
	gatedSMCNoAdapter(t)
	file, path := useTempConfig(t)
	file.SetUpperLimit(55)
	useNativeCapabilities(t, 55, 100)
	f := &fakeNativeLimit{limit: 55, enabled: true, registered: true}
	useFakeNativeLimit(t, f)
	r := httptest.NewRecorder()
	setupRoutes().ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/disable", strings.NewReader(`"1h"`)))
	if r.Code != http.StatusCreated || f.enabled || f.registered || file.UpperLimit() != 100 {
		t.Fatalf("temporary release failed: %d %s %+v", r.Code, r.Body.String(), f)
	}
	reloaded, err := config.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PreDisableLimit() != 55 || reloaded.DisableUntil().IsZero() {
		t.Fatal("restore intent not persisted")
	}
	conf = reloaded // daemon restart with the saved temporary-disable state
	if !maintainManagedChargeLimit() || f.enabled {
		t.Fatal("restart re-enabled too early")
	}
	if !restoreDisabledLimit(reloaded, reloaded.DisableUntil().Add(time.Second)) {
		t.Fatal("expiry not restored")
	}
	if !maintainManagedChargeLimit() || !f.enabled || f.limit != 55 {
		t.Fatal("expiry failed to restore exact 55%")
	}
}

func TestNativeTemporaryDisableReportsReleaseFailure(t *testing.T) {
	gatedSMCNoAdapter(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	useNativeCapabilities(t, 55, 100)
	f := &fakeNativeLimit{limit: 55, enabled: true, err: errors.New("release failed")}
	useFakeNativeLimit(t, f)
	r := httptest.NewRecorder()
	setupRoutes().ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/disable", strings.NewReader(`"1h"`)))
	if r.Code != http.StatusInternalServerError || file.UpperLimit() != 100 || file.PreDisableLimit() != 55 {
		t.Fatalf("failure hidden or retry intent lost: %d %s", r.Code, r.Body.String())
	}
	f.err = nil
	if !maintainManagedChargeLimit() || f.enabled {
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
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	useNativeCapabilities(t, 55, 100)
	f := &fakeNativeLimit{limit: 100}
	useFakeNativeLimit(t, f)
	b := &blockingNativeController{fakeNativeLimit: f, entered: make(chan struct{}), proceed: make(chan struct{})}
	nativeLimit = b
	previous := chargeControlStopped
	t.Cleanup(func() { chargeControlStopped = previous })
	chargeControlStopped = false
	done := make(chan bool, 1)
	go func() { done <- maintainLoopForced() }()
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- stopChargeControl() }()
	close(b.proceed)
	if !<-done {
		t.Fatal("in-flight maintenance failed")
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if f.enabled {
		t.Fatal("shutdown left native limit active")
	}
	writes := len(f.setCalls)
	if !maintainLoopForced() || f.enabled || len(f.setCalls) != writes {
		t.Fatal("queued maintenance reapplied a released limit")
	}
}
