package daemon

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/temperature"
)

const temperatureActiveWindow = 5 * time.Minute

type temperatureLoopResult struct {
	protected bool
	recovered bool
}

var (
	temperatureMu               = &sync.Mutex{}
	temperatureProtectionActive = false
	lastTemperatureStatus       = temperature.Status{}
)

func readBatteryTemperatureCelsius() (float64, error) {
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
	if err != nil {
		return 0, err
	}
	if info == nil || info.IOKit == nil {
		return 0, fmt.Errorf("no IOKit data available")
	}
	temp := info.IOKit.Battery.Temperature
	if temp <= 0 || math.IsNaN(temp) || math.IsInf(temp, 0) {
		return 0, fmt.Errorf("battery temperature unavailable")
	}
	if temp > 100 {
		return 0, fmt.Errorf("battery temperature out of range: %.1f°C", temp)
	}
	return temp, nil
}

func handleTemperatureMonitoringAndProtection(isChargingEnabled, isPluggedIn bool) temperatureLoopResult {
	temperatureMu.Lock()
	protectionActive := temperatureProtectionActive
	temperatureMu.Unlock()

	now := time.Now()
	monitoringEnabled := conf.TemperatureMonitoringEnabled()
	threshold := conf.TemperatureProtectionThresholdCelsius()
	recoveryThreshold := threshold - conf.TemperatureProtectionRecoveryDeltaCelsius()
	if recoveryThreshold < 0 {
		recoveryThreshold = 0
	}

	status := temperature.Status{
		MonitoringEnabled:          monitoringEnabled,
		ProtectionThresholdCelsius: threshold,
		ProtectionActive:           protectionActive,
		RecoveryThresholdCelsius:   recoveryThreshold,
	}

	if !monitoringEnabled {
		temperatureMu.Lock()
		wasProtected := temperatureProtectionActive
		temperatureProtectionActive = false
		status.ProtectionActive = false
		lastTemperatureStatus = status
		temperatureMu.Unlock()
		if wasProtected {
			return temperatureLoopResult{recovered: true}
		}
		return temperatureLoopResult{}
	}

	tempC, err := readBatteryTemperatureCelsius()
	if err != nil {
		status.TemperatureUnavailableReason = err.Error()
		temperatureMu.Lock()
		protectionActive = temperatureProtectionActive
		status.ProtectionActive = protectionActive
		lastTemperatureStatus = status
		temperatureMu.Unlock()
		if protectionActive {
			return temperatureLoopResult{protected: true}
		}
		return temperatureLoopResult{}
	}

	userActive, activityErr := userIsActive(temperatureActiveWindow)
	if activityErr != nil {
		status.ActivityUnavailableReason = activityErr.Error()
		userActive = true
	}

	charging := isChargingEnabled && isPluggedIn

	current := tempC
	status.CurrentCelsius = &current
	status.UserActive = userActive
	status.Charging = charging
	status.LastUpdatedUnix = now.Unix()

	if tempC >= float64(threshold) {
		if !protectionActive {
			logrus.WithFields(logrus.Fields{
				"temperature": tempC,
				"threshold":   threshold,
			}).Info("battery temperature above threshold, disabling charging")
		}
		temperatureMu.Lock()
		temperatureProtectionActive = true
		status.ProtectionActive = true
		lastTemperatureStatus = status
		temperatureMu.Unlock()
		if isChargingEnabled {
			if err := smcConn.DisableCharging(); err != nil {
				logrus.WithError(err).Error("DisableCharging failed during temperature protection")
				return temperatureLoopResult{protected: true}
			}
		}
		return temperatureLoopResult{protected: true}
	}

	if protectionActive && tempC <= float64(recoveryThreshold) {
		logrus.WithFields(logrus.Fields{
			"temperature": tempC,
			"threshold":   threshold,
			"recovery":    recoveryThreshold,
		}).Info("battery temperature recovered, releasing temperature protection")
		temperatureMu.Lock()
		temperatureProtectionActive = false
		status.ProtectionActive = false
		lastTemperatureStatus = status
		temperatureMu.Unlock()
		return temperatureLoopResult{recovered: true}
	}

	temperatureMu.Lock()
	status.ProtectionActive = temperatureProtectionActive
	lastTemperatureStatus = status
	protectionActive = temperatureProtectionActive
	temperatureMu.Unlock()
	if protectionActive {
		return temperatureLoopResult{protected: true}
	}
	return temperatureLoopResult{}
}

func getTemperatureStatusSnapshot() temperature.Status {
	temperatureMu.Lock()
	defer temperatureMu.Unlock()

	status := lastTemperatureStatus
	status.MonitoringEnabled = conf.TemperatureMonitoringEnabled()
	status.ProtectionThresholdCelsius = conf.TemperatureProtectionThresholdCelsius()
	status.RecoveryThresholdCelsius = status.ProtectionThresholdCelsius - conf.TemperatureProtectionRecoveryDeltaCelsius()
	if status.RecoveryThresholdCelsius < 0 {
		status.RecoveryThresholdCelsius = 0
	}
	status.ProtectionActive = temperatureProtectionActive
	return status
}
