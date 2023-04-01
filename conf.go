package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/sirupsen/logrus"
)

type Config struct {
	Limit               int  `json:"limit"`
	Maintain            bool `json:"maintain"`
	LoopIntervalSeconds int  `json:"loopIntervalSeconds"`
}

var (
	config        Config
	configPath    = "/etc/batt.json"
	defaultConfig = Config{
		Limit:               60,
		Maintain:            true,
		LoopIntervalSeconds: 60,
	}
)

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
		logrus.Warnf("config file %s does not exist, using default config %#v", configPath, defaultConfig)
		config = defaultConfig
		saveConfig()
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &config)
}
