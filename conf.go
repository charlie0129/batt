package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/sirupsen/logrus"
)

// Config is the configuration of batt.
type Config struct {
	// Limit is the battery charge limit in percentage, when Maintain is enabled.
	// batt will keep the battery charge around this limit. Note that if your
	// current battery charge is higher than the limit, it will simply stop
	// charging.
	Limit                   int  `json:"limit"`
	PreventIdleSleep        bool `json:"preventIdleSleep"`
	DisableChargingPreSleep bool `json:"disableChargingPreSleep"`
	AllowNonRootAccess      bool `json:"allowNonRootAccess"`
	LowerLimitDelta         int  `json:"lowerLimitDelta"`
	ControlMagSafeLED       bool `json:"controlMagSafeLED"`
}

var (
	configPath    = "/etc/batt.json"
	defaultConfig = Config{
		Limit:                   60,
		PreventIdleSleep:        true,
		DisableChargingPreSleep: true,
		AllowNonRootAccess:      false,
		LowerLimitDelta:         2,
		// There are Macs without MagSafe LED. We only do checks when the user
		// explicitly enables this feature. In the future, we might add a check
		// that disables this feature if the Mac does not have a MagSafe LED.
		ControlMagSafeLED: false,
	}
)

var config = defaultConfig

func saveConfig() error {
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, b, 0644)
}

func loadConfig() error {
	// Check if config file exists
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		logrus.WithField("config", defaultConfig).Infof("config file %s does not exist, using default config", configPath)
		config = defaultConfig
		err := saveConfig()
		if err != nil {
			logrus.Errorf("failed to save config: %v", err)
			return err
		}
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &config)
}
