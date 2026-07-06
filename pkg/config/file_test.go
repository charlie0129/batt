package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLoadIgnoresLegacyCommentLines(t *testing.T) {
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
}

func TestParseTrayIconStyle(t *testing.T) {
	if got, ok := ParseTrayIconStyle("fixed-percentage"); !ok || got != TrayIconStyleFixedPercent {
		t.Fatalf("ParseTrayIconStyle(fixed-percentage) = %s, %t; want %s, true", got, ok, TrayIconStyleFixedPercent)
	}
}

func TestFileSaveDoesNotWriteTemperatureReferenceComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "batt.json")
	cfg := NewFileFromConfig(&RawFileConfig{}, path)

	cfg.SetTemperatureMonitoringEnabled(true)

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(content)
	if strings.Contains(got, "# Temperature references") {
		t.Fatalf("saved config should not include temperature reference comments:\n%s", got)
	}
	if !strings.Contains(got, `"temperatureMonitoringEnabled": true`) {
		t.Fatalf("saved config missing temperature monitoring setting:\n%s", got)
	}
}
