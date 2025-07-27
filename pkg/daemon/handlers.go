package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"

	"github.com/distatus/battery"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/types"
	"github.com/charlie0129/batt/pkg/version"
)

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

// getBatteryHealthFromProfiler fetches Condition, MaximumCapacity, and CycleCount from system_profiler in one call.
func getBatteryHealthFromProfiler() (condition string, maxCapacity int, cycleCount int) {
	// Default values in case of an error
	condition = "Unknown"
	maxCapacity = 0
	cycleCount = 0

	cmd := exec.Command("system_profiler", "SPPowerDataType", "-xml")
	out, err := cmd.Output()
	if err != nil {
		logrus.WithError(err).Warn("Could not run system_profiler to get battery health")
		return
	}
	xmlOutput := string(out)

	// Regex to find: <key>sppower_battery_health</key><string></string>
	reCondition := regexp.MustCompile(`<key>sppower_battery_health</key>\s*<string>(.*)</string>`)
	if matches := reCondition.FindStringSubmatch(xmlOutput); len(matches) > 1 {
		condition = matches[1]
	}

	// Regex to find: <key>sppower_battery_health_maximum_capacity</key><string></string>
	reMaxCap := regexp.MustCompile(`<key>sppower_battery_health_maximum_capacity</key>\s*<string>(\d+).*</string>`)
	if matches := reMaxCap.FindStringSubmatch(xmlOutput); len(matches) > 1 {
		maxCapacity, _ = strconv.Atoi(matches[1])
	}

	// Regex to find: <key>sppower_battery_cycle_count</key><integer></integer>
	reCycleCount := regexp.MustCompile(`<key>sppower_battery_cycle_count</key>\s*<integer>(\d+)</integer>`)
	if matches := reCycleCount.FindStringSubmatch(xmlOutput); len(matches) > 1 {
		cycleCount, _ = strconv.Atoi(matches[1])
	}

	return
}

func getDetailedBatteryInfo(c *gin.Context) {
	// Get all battery health info from system_profiler in one efficient call.
	condition, maxCapacity, cycleCount := getBatteryHealthFromProfiler()

	// Create our response struct
	detailedInfo := &types.DetailedBatteryInfo{
		CycleCount:      cycleCount,
		Condition:       condition,
		MaximumCapacity: float64(maxCapacity),
	}

	c.IndentedJSON(http.StatusOK, detailedInfo)
}
