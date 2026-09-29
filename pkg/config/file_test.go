package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/utils/ptr"
)

func TestAdapterDisableTimerPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batt.json")
	configured, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, time.July, 21, 12, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	configured.SetAdapterDisableTimer(until)
	if err := configured.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AdapterDisableUntil(); !got.Equal(until) {
		t.Fatalf("AdapterDisableUntil() = %s, want %s", got, until)
	}

	reloaded.ClearAdapterDisableTimer()
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	reloadedAgain, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloadedAgain.AdapterDisableUntil(); !got.IsZero() {
		t.Fatalf("AdapterDisableUntil() after clear = %s, want zero", got)
	}
}

func TestChargeOnceTargetPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batt.json")
	configured, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	configured.SetUpperLimit(70)
	configured.SetChargeOnceTarget(100)
	if err := configured.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ChargeOnceTarget(); got != 100 {
		t.Fatalf("ChargeOnceTarget() = %d, want 100", got)
	}
	// The whole point of a one-time charge is that it leaves the configured
	// limit alone, so there is nothing to restore afterwards.
	if got := reloaded.UpperLimit(); got != 70 {
		t.Fatalf("UpperLimit() = %d, want 70", got)
	}

	reloaded.ClearChargeOnceTarget()
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	reloadedAgain, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloadedAgain.ChargeOnceTarget(); got != 0 {
		t.Fatalf("ChargeOnceTarget() after clear = %d, want 0", got)
	}
}

func TestChargeOnceTargetIgnoresOutOfRangeValue(t *testing.T) {
	for _, target := range []int{-1, 0, 5, 101} {
		path := filepath.Join(t.TempDir(), "batt.json")
		raw := fmt.Sprintf(`{"limit": 70, "chargeOnceTarget": %d}`, target)
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}

		loaded, err := NewFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := loaded.ChargeOnceTarget(); got != 0 {
			t.Fatalf("ChargeOnceTarget() for stored %d = %d, want 0", target, got)
		}
	}
}

func TestRawConfigCarriesChargeOnceTarget(t *testing.T) {
	configured := NewFileFromConfig(&RawFileConfig{Limit: ptr.To(70), LowerLimitDelta: ptr.To(30)}, "")

	raw, err := NewRawFileConfigFromConfig(configured)
	if err != nil {
		t.Fatal(err)
	}
	if raw.ChargeOnceTarget != nil {
		t.Fatalf("ChargeOnceTarget = %d, want absent", *raw.ChargeOnceTarget)
	}

	configured.SetChargeOnceTarget(100)
	raw, err = NewRawFileConfigFromConfig(configured)
	if err != nil {
		t.Fatal(err)
	}
	if raw.ChargeOnceTarget == nil || *raw.ChargeOnceTarget != 100 {
		t.Fatalf("ChargeOnceTarget = %v, want 100", raw.ChargeOnceTarget)
	}
}
