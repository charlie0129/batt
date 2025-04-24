package main

import (
	"encoding/json"
	"errors"
	"os"
	"bytes"

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

func resetConfig() error {
	config = defaultConfig

	logrus.WithFields(logrus.Fields{
		"config": defaultConfig,
		"config_file": configPath,
	}).Warn("resetting config file to default")

	err := saveConfig()
	return err
}

func loadConfig() error {
	// Check if config file exists
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		logrus.WithFields(logrus.Fields{
			"config_file": configPath,
		}).Warn("config file does not exist")

		err = resetConfig()
		if err != nil {
			return err
		}
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	if len(bytes.TrimSpace(b)) == 0 {
		logrus.WithFields(logrus.Fields{
			"config_file": configPath,
			"config_bytes": b,
		}).Warn("config file is empty")

		err = resetConfig()
		if err != nil {
			return err
		}

		b, err = os.ReadFile(configPath)
		if err != nil {
			return err
		}
	}

	return json.Unmarshal(b, &config)
}
