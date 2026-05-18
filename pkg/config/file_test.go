package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/temperature"
)

func TestFileLoadWithTemperatureReferenceComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "batt.json")
	if err := os.WriteFile(path, []byte(`# Temperature references (auto-generated)
# Idle + Not Charging: 31.8°C
# Idle + Charging: 36.2°C
# Active + Charging: 41.5°C

{
  "limit": 80,
  "temperatureMonitoringEnabled": true,
  "temperatureProtectionThresholdCelsius": 42
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := NewFile(path)
	if err != nil {
		t.Fatalf("NewFile returned error: %v", err)
	}

	if !cfg.TemperatureMonitoringEnabled() {
		t.Fatalf("expected temperature monitoring enabled")
	}
	if got := cfg.TemperatureProtectionThresholdCelsius(); got != 42 {
		t.Fatalf("threshold = %d, want 42", got)
	}
	refs := cfg.TemperatureReferences()
	if refs.IdleNotCharging == nil || *refs.IdleNotCharging != 31.8 {
		t.Fatalf("idle not charging reference = %v, want 31.8", refs.IdleNotCharging)
	}
	if refs.IdleCharging == nil || *refs.IdleCharging != 36.2 {
		t.Fatalf("idle charging reference = %v, want 36.2", refs.IdleCharging)
	}
	if refs.ActiveCharging == nil || *refs.ActiveCharging != 41.5 {
		t.Fatalf("active charging reference = %v, want 41.5", refs.ActiveCharging)
	}
}

func TestFileTrayIconStyle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "batt.json")
	if err := os.WriteFile(path, []byte(`{
  "trayIconStyle": "fixed"
}
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := NewFile(path)
	if err != nil {
		t.Fatalf("NewFile returned error: %v", err)
	}
	if got := cfg.TrayIconStyle(); got != TrayIconStyleFixed {
		t.Fatalf("tray icon style = %s, want %s", got, TrayIconStyleFixed)
	}

	cfg.SetTrayIconStyle(TrayIconStylePercentage)
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if got := string(content); !strings.Contains(got, `"trayIconStyle": "percentage"`) {
		t.Fatalf("saved config missing tray icon style:\n%s", got)
	}
}

func TestFileSaveWritesTemperatureReferenceComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "batt.json")
	cfg := NewFileFromConfig(&RawFileConfig{}, path)

	cfg.SetTemperatureMonitoringEnabled(true)
	cfg.SetTemperatureReference(temperature.ScenarioIdleNotCharging, 31.8)
	cfg.SetTemperatureReference(temperature.ScenarioIdleCharging, 36.2)
	cfg.SetTemperatureReference(temperature.ScenarioActiveCharging, 41.5)

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(content)
	for _, want := range []string{
		"# Temperature references (auto-generated)",
		"# Idle + Not Charging: 31.8°C",
		"# Idle + Charging: 36.2°C",
		"# Active + Charging: 41.5°C",
		`"temperatureMonitoringEnabled": true`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("saved config missing %q:\n%s", want, got)
		}
	}
}
