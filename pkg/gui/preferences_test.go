package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMenuBarIconStyle(t *testing.T) {
	got, ok := parseMenuBarIconStyle("fixed-percentage")
	if !ok || got != menuBarIconStyleFixedPercent {
		t.Fatalf("parseMenuBarIconStyle(fixed-percentage) = %s, %t; want %s, true", got, ok, menuBarIconStyleFixedPercent)
	}
}

func TestGUIPreferencesUsePlistInMacOSPreferencesDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	prefs := guiPreferences{
		MenuBarIconStyle:          menuBarIconStyleBattery,
		MenuBarIconRefreshSeconds: 30,
	}
	if err := saveGUIPreferences(prefs); err != nil {
		t.Fatalf("saveGUIPreferences returned error: %v", err)
	}

	path := filepath.Join(home, "Library", "Preferences", "cc.chlc.batt.gui.plist")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preferences: %v", err)
	}
	if !strings.Contains(string(contents), "<plist version=\"1.0\">") {
		t.Fatalf("preferences are not an XML plist:\n%s", contents)
	}

	got := loadGUIPreferences()
	if got != prefs {
		t.Fatalf("loadGUIPreferences() = %#v; want %#v", got, prefs)
	}
}
