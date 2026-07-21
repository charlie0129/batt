package config

import (
	"path/filepath"
	"testing"
	"time"
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
