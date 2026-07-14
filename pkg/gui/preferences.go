package gui

import (
	"os"
	"path/filepath"

	"howett.net/plist"
)

const (
	defaultMenuBarIconRefreshIntervalSeconds = 60
	preferencesFileName                      = "cc.chlc.batt.gui.plist"

	menuBarIconStyleFixedStr        = "fixed"
	menuBarIconStyleFixedPercentStr = "fixed-percentage"
	menuBarIconStyleBatteryStr      = "battery"
	menuBarIconStylePercentageStr   = "percentage"
)

type menuBarIconStyle string

const (
	menuBarIconStyleFixed        menuBarIconStyle = menuBarIconStyleFixedStr
	menuBarIconStyleFixedPercent menuBarIconStyle = menuBarIconStyleFixedPercentStr
	menuBarIconStyleBattery      menuBarIconStyle = menuBarIconStyleBatteryStr
	menuBarIconStylePercentage   menuBarIconStyle = menuBarIconStylePercentageStr
)

func parseMenuBarIconStyle(value string) (menuBarIconStyle, bool) {
	switch value {
	case menuBarIconStyleFixedStr:
		return menuBarIconStyleFixed, true
	case menuBarIconStyleFixedPercentStr:
		return menuBarIconStyleFixedPercent, true
	case menuBarIconStyleBatteryStr:
		return menuBarIconStyleBattery, true
	case menuBarIconStylePercentageStr:
		return menuBarIconStylePercentage, true
	default:
		return menuBarIconStylePercentage, false
	}
}

func (style menuBarIconStyle) isValid() bool {
	_, ok := parseMenuBarIconStyle(string(style))
	return ok
}

type guiPreferences struct {
	MenuBarIconStyle          menuBarIconStyle `plist:"MenuBarIconStyle"`
	MenuBarIconRefreshSeconds int              `plist:"MenuBarIconRefreshSeconds"`
}

func defaultGUIPreferences() guiPreferences {
	return guiPreferences{
		MenuBarIconStyle:          menuBarIconStylePercentage,
		MenuBarIconRefreshSeconds: defaultMenuBarIconRefreshIntervalSeconds,
	}
}

func preferencesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Preferences", preferencesFileName), nil
}

func normalizeGUIPreferences(prefs guiPreferences) guiPreferences {
	if !prefs.MenuBarIconStyle.isValid() {
		prefs.MenuBarIconStyle = menuBarIconStylePercentage
	}
	if prefs.MenuBarIconRefreshSeconds <= 0 {
		prefs.MenuBarIconRefreshSeconds = defaultMenuBarIconRefreshIntervalSeconds
	}
	return prefs
}

func loadGUIPreferences() guiPreferences {
	path, err := preferencesPath()
	if err != nil {
		return defaultGUIPreferences()
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return defaultGUIPreferences()
	}

	prefs := defaultGUIPreferences()
	if _, err := plist.Unmarshal(contents, &prefs); err != nil {
		return defaultGUIPreferences()
	}
	return normalizeGUIPreferences(prefs)
}

func saveGUIPreferences(prefs guiPreferences) error {
	prefs = normalizeGUIPreferences(prefs)

	path, err := preferencesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	contents, err := plist.MarshalIndent(prefs, plist.XMLFormat, "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0644)
}
