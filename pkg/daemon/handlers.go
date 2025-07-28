package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/distatus/battery"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/types"
	"github.com/charlie0129/batt/pkg/version"
)

type SystemProfilerPowerReport struct {
	SPPowerDataType []PowerItem `json:"SPPowerDataType"`
}

type PowerItem struct {
	Name       string            `json:"_name"`
	HealthInfo BatteryHealthInfo `json:"sppower_battery_health_info"`
}

type BatteryHealthInfo struct {
	CycleCount  int    `json:"sppower_battery_cycle_count"`
	Condition   string `json:"sppower_battery_health"`
	MaxCapacity string `json:"sppower_battery_health_maximum_capacity"`
}

func getConfig(c *gin.Context) {
	fc, err := config.NewRawFileConfigFromConfig(conf)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.IndentedJSON(http.StatusOK, fc)
}

func getLimit(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, conf.UpperLimit())
}

func setLimit(c *gin.Context) {
	var l int
	if err := c.BindJSON(&l); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if l < 10 || l > 100 {
		err := fmt.Errorf("limit must be between 10 and 100, got %d", l)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if delta := conf.UpperLimit() - conf.LowerLimit(); l-delta <= 10 {
		err := fmt.Errorf("upper limit must be greater than lower limit + 10, got %d", l-delta)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetUpperLimit(l)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set charging limit to %d", l)

	var msg string
	charge, err := smcConn.GetBatteryCharge()
	if err != nil {
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%", conf.UpperLimit(), conf.LowerLimit())
	} else {
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%, current charge: %d%%", conf.UpperLimit(), conf.LowerLimit(), charge)
		if charge > conf.UpperLimit() {
			msg += ". Current charge is above the limit, so your computer will use power from the wall only. Battery charge will remain the same."
		}
	}

	if l >= 100 {
		msg = "set charging limit to 100%. batt will not control charging anymore."
	}

	// Immediate single maintain loop, to avoid waiting for the next loop
	maintainLoopForced()

	c.IndentedJSON(http.StatusCreated, msg)
}

func setPreventIdleSleep(c *gin.Context) {
	var p bool
	if err := c.BindJSON(&p); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetPreventIdleSleep(p)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set prevent idle sleep to %t", p)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setDisableChargingPreSleep(c *gin.Context) {
	var d bool
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetDisableChargingPreSleep(d)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set disable charging pre sleep to %t", d)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setPreventSystemSleep(c *gin.Context) {
	var p bool
	if err := c.BindJSON(&p); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetPreventSystemSleep(p)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set prevent system sleep to %t", p)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setAdapter(c *gin.Context) {
	var d bool
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if d {
		if err := smcConn.EnableAdapter(); err != nil {
			logrus.Errorf("enablePowerAdapter failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		logrus.Infof("enabled power adapter")
	} else {
		if err := smcConn.DisableAdapter(); err != nil {
			logrus.Errorf("disablePowerAdapter failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		logrus.Infof("disabled power adapter")
	}

	c.IndentedJSON(http.StatusCreated, "ok")
}

func getAdapter(c *gin.Context) {
	enabled, err := smcConn.IsAdapterEnabled()
	if err != nil {
		logrus.Errorf("getAdapter failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, enabled)
}

func getCharging(c *gin.Context) {
	charging, err := smcConn.IsChargingEnabled()
	if err != nil {
		logrus.Errorf("getCharging failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, charging)
}

func getBatteryInfo(c *gin.Context) {
	batteries, err := battery.GetAll()
	if err != nil {
		logrus.Errorf("getBatteryInfo failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	if len(batteries) == 0 {
		logrus.Errorf("no batteries found")
		c.IndentedJSON(http.StatusInternalServerError, "no batteries found")
		_ = c.AbortWithError(http.StatusInternalServerError, errors.New("no batteries found"))
		return
	}

	bat := batteries[0] // All Apple Silicon MacBooks only have one battery. No need to support more.
	if bat.State == battery.Discharging {
		bat.ChargeRate = -bat.ChargeRate
	}

	c.IndentedJSON(http.StatusOK, bat)
}

func setLowerLimitDelta(c *gin.Context) {
	var d int
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if d < 0 {
		err := fmt.Errorf("lower limit delta must be positive, got %d", d)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if conf.UpperLimit()-d < 10 {
		err := fmt.Errorf("lower limit delta must be less than limit - 10, got %d", d)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetLowerLimit(conf.UpperLimit() - d)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	ret := fmt.Sprintf("set lower limit delta to %d, current upper/lower limit is %d%%/%d%%", d, conf.UpperLimit(), conf.LowerLimit())
	logrus.Info(ret)

	c.IndentedJSON(http.StatusCreated, ret)
}

func setControlMagSafeLED(c *gin.Context) {
	// Check if MasSafe is supported first. If not, return error.
	if !smcConn.CheckMagSafeExistence() {
		logrus.Errorf("setControlMagSafeLED called but there is no MasSafe LED on this device")
		err := fmt.Errorf("there is no MasSafe on this device. You can only enable this setting on a compatible device, e.g. MacBook Pro 14-inch 2021")
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	var d bool
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetControlMagSafeLED(d)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set control MagSafe LED to %t", d)

	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("ControlMagSafeLED set to %t. You should be able to see the effect in a few minutes.", d))
}

func getCurrentCharge(c *gin.Context) {
	charge, err := smcConn.GetBatteryCharge()
	if err != nil {
		logrus.Errorf("getCurrentCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, charge)
}

func getPluggedIn(c *gin.Context) {
	pluggedIn, err := smcConn.IsPluggedIn()
	if err != nil {
		logrus.Errorf("getCurrentCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, pluggedIn)
}

func getChargingControlCapable(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, smcConn.IsChargingControlCapable())
}

func getVersion(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, version.Version)
}

func getPowerTelemetry(c *gin.Context) {
	telemetry, err := smcConn.GetPowerTelemetry()
	if err != nil {
		logrus.Errorf("getPowerTelemetry failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, telemetry)
}

func getDetailedBatteryInfo(c *gin.Context) {
	// Execute system_profiler to get all battery data in one go.
	cmd := exec.Command("system_profiler", "SPPowerDataType", "-json")
	out, err := cmd.Output()
	if err != nil {
		logrus.WithError(err).Warn("Could not run system_profiler to get battery health")
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	var report SystemProfilerPowerReport
	if err := json.Unmarshal(out, &report); err != nil {
		logrus.WithError(err).Warn("Failed to unmarshal system_profiler JSON output")
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Find the battery information within the JSON report.
	for _, item := range report.SPPowerDataType {
		if item.Name == "spbattery_information" {
			// Parse the number from the "95 %" string.
			var maxCapacityValue int
			capacityStr := strings.TrimSpace(item.HealthInfo.MaxCapacity)
			if _, err := fmt.Sscanf(capacityStr, "%d", &maxCapacityValue); err != nil {
				logrus.WithError(err).Warn("Failed to parse max capacity string")
				// maxCapacityValue will remain 0, which is acceptable.
			}

			// Create and send the response.
			detailedInfo := &types.DetailedBatteryInfo{
				CycleCount:      item.HealthInfo.CycleCount,
				Condition:       item.HealthInfo.Condition,
				MaximumCapacity: float64(maxCapacityValue),
			}
			c.IndentedJSON(http.StatusOK, detailedInfo)
			return // We're done.
		}
	}

	// If we finished the loop and found nothing, return an error.
	err = errors.New("could not find spbattery_information in system_profiler output")
	logrus.Error(err)
	_ = c.AbortWithError(http.StatusInternalServerError, err)
}
