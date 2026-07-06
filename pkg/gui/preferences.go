package gui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/charlie0129/batt/pkg/config"
)

const (
	defaultTrayIconRefreshIntervalSeconds = 60
	preferencesFileName                   = "cc.chlc.batt.gui.json"
)

type guiPreferences struct {
	MenuBarIconStyle          config.TrayIconStyle `json:"menuBarIconStyle,omitempty"`
	MenuBarIconRefreshSeconds int                  `json:"menuBarIconRefreshSeconds,omitempty"`
}

func defaultGUIPreferences() guiPreferences {
	return guiPreferences{
		MenuBarIconStyle:          config.TrayIconStylePercentage,
		MenuBarIconRefreshSeconds: defaultTrayIconRefreshIntervalSeconds,
	}
}

func preferencesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Preferences", preferencesFileName), nil
}

func loadGUIPreferences() guiPreferences {
	prefs := defaultGUIPreferences()
	path, err := preferencesPath()
	if err != nil {
		return prefs
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return prefs
	}
	if err := json.Unmarshal(b, &prefs); err != nil {
		return defaultGUIPreferences()
	}
	if !prefs.MenuBarIconStyle.IsValid() {
		prefs.MenuBarIconStyle = config.TrayIconStylePercentage
	}
	if prefs.MenuBarIconRefreshSeconds <= 0 {
		prefs.MenuBarIconRefreshSeconds = defaultTrayIconRefreshIntervalSeconds
	}
	return prefs
}

func saveGUIPreferences(prefs guiPreferences) error {
	if !prefs.MenuBarIconStyle.IsValid() {
		prefs.MenuBarIconStyle = config.TrayIconStylePercentage
	}
	if prefs.MenuBarIconRefreshSeconds <= 0 {
		prefs.MenuBarIconRefreshSeconds = defaultTrayIconRefreshIntervalSeconds
	}

	path, err := preferencesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	b, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0644)
}
