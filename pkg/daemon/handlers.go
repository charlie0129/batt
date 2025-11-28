package daemon

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
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
	// Use powerkit-go to retrieve current system info (IOKit only is sufficient here)
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
	if err != nil || info == nil || info.IOKit == nil {
		if err == nil {
			err = errors.New("no IOKit data available")
		}
		logrus.Errorf("getBatteryInfo failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Map powerkit-go data to our backwards-compatible Battery structure
	var state powerinfo.BatteryState
	switch {
	case info.IOKit.State.FullyCharged:
		state = powerinfo.Full
	case info.IOKit.State.IsCharging:
		state = powerinfo.Charging
	default:
		state = powerinfo.Discharging
	}

	// Compute charge rate (mW) using native amperage sign from IOKit
	powerW := info.IOKit.Battery.Voltage * info.IOKit.Battery.Amperage
	chargeRateMilliW := int(math.Round(powerW * 1000.0))

	// Use the actual achievable max capacity (mAh) from IOKit.
	designmAh := info.IOKit.Battery.MaxCapacity

	resp := powerinfo.Battery{
		State:         state,
		Design:        designmAh,
		ChargeRate:    chargeRateMilliW,
		DesignVoltage: info.IOKit.Battery.Voltage,
	}

	c.IndentedJSON(http.StatusOK, resp)
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

	var mode config.ControlMagSafeMode
	if err := c.BindJSON(&mode); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetControlMagSafeLED(mode)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set control MagSafe LED to %s", mode)

	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("ControlMagSafeLED set to %s. You should be able to see the effect in a few minutes.", mode))
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
	// Use powerkit-go to fetch a snapshot of system power state
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Info", "Use /telemetry?power=1 instead; /power-telemetry will be removed in a future release")
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
	if err != nil || info == nil || info.IOKit == nil {
		if err == nil {
			err = errors.New("failed to fetch IOKit power data")
		}
		logrus.Errorf("getPowerTelemetry failed: %v", err)
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Build simplified telemetry expected by the GUI
	var snapshot powerinfo.PowerTelemetry
	snapshot.Adapter.InputVoltage = info.IOKit.Adapter.InputVoltage
	snapshot.Adapter.InputAmperage = info.IOKit.Adapter.InputAmperage

	snapshot.Battery.CycleCount = info.IOKit.Battery.CycleCount

	snapshot.Calculations.ACPower = info.IOKit.Calculations.AdapterPower
	snapshot.Calculations.BatteryPower = info.IOKit.Calculations.BatteryPower
	snapshot.Calculations.SystemPower = info.IOKit.Calculations.SystemPower
	snapshot.Calculations.HealthByMaxCapacity = info.IOKit.Calculations.HealthByMaxCapacity

	c.IndentedJSON(http.StatusOK, snapshot)
}

// Unified telemetry endpoint: /telemetry?power=1&calibration=1 (flags optional; default all)
func getUnifiedTelemetry(c *gin.Context) {
	wantPower := c.Query("power") != "0"
	wantCal := c.Query("calibration") != "0"

	resp := gin.H{}

	if wantPower {
		info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
		if err != nil || info == nil || info.IOKit == nil {
			if err == nil {
				err = errors.New("failed to fetch IOKit power data")
			}
			logrus.WithError(err).Warn("power telemetry unavailable for unified telemetry")
		} else {
			var snapshot powerinfo.PowerTelemetry
			snapshot.Adapter.InputVoltage = info.IOKit.Adapter.InputVoltage
			snapshot.Adapter.InputAmperage = info.IOKit.Adapter.InputAmperage
			snapshot.Battery.CycleCount = info.IOKit.Battery.CycleCount
			snapshot.Calculations.ACPower = info.IOKit.Calculations.AdapterPower
			snapshot.Calculations.BatteryPower = info.IOKit.Calculations.BatteryPower
			snapshot.Calculations.SystemPower = info.IOKit.Calculations.SystemPower
			snapshot.Calculations.HealthByMaxCapacity = info.IOKit.Calculations.HealthByMaxCapacity
			resp["power"] = snapshot
		}
	}

	if wantCal {
		resp["calibration"] = getCalibrationStatus()
	}

	// Add deprecation header if caller still hitting legacy endpoints (not detectable here), but we can add a generic hint.
	c.Header("X-Batt-Telemetry-Version", "1")
	c.IndentedJSON(http.StatusOK, resp)
}

// SSE endpoint: streams daemon events (first: calibration phase changes)
func getEventStream(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusInternalServerError)
		return
	}

	ch := sseHub.Subscribe()
	defer sseHub.Unsubscribe(ch)

	// Notify client that stream is open and suggest retry interval
	if _, err := c.Writer.WriteString("retry: 10000\n"); err != nil {
		logrus.WithError(err).Warn("failed to write initial retry for SSE stream")
		return
	}
	if _, err := c.Writer.WriteString(":ok\n\n"); err != nil {
		logrus.WithError(err).Warn("failed to write initial comment for SSE stream")
		return
	}
	flusher.Flush()

	// Heartbeat ticker: send SSE comment periodically to keep the connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Stream loop
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			// SSE comment line as heartbeat
			_, _ = c.Writer.WriteString(":ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// SSE frame: event + data
			if msg.Name != "" {
				_, _ = c.Writer.WriteString("event: " + msg.Name + "\n")
			}
			_, _ = c.Writer.WriteString("data: ")
			_, _ = c.Writer.Write(msg.Data)
			_, _ = c.Writer.WriteString("\n\n")
			flusher.Flush()
		}
	}
}

// ===== Calibration Handlers =====

func postStartCalibration(c *gin.Context) {
	// Read threshold & hold from current config getters
	threshold := conf.CalibrationDischargeThreshold()
	hold := conf.CalibrationHoldDurationMinutes()
	if err := startCalibration(threshold, hold); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusCreated, gin.H{"ok": true})
}

func postPauseCalibration(c *gin.Context) {
	if err := pauseCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func postResumeCalibration(c *gin.Context) {
	if err := resumeCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func postCancelCalibration(c *gin.Context) {
	if err := cancelCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func setSchedule(c *gin.Context) {
	var cronExpr string
	if err := c.BindJSON(&cronExpr); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	nextRuns, err := schedule(cronExpr)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	resp := gin.H{"ok": true}
	if nextRuns != nil {
		resp["next_runs"] = nextRuns
	}

	c.IndentedJSON(http.StatusCreated, resp)
}

func skipSchedule(c *gin.Context) {
	if err := skipNextSchedule(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func postponeSchedule(c *gin.Context) {
	var raw string
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// default 1 hour
	if raw == "" {
		raw = "1h"
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if d < leadDuration {
		err := fmt.Errorf("postpone duration must be at least %s, got %s", leadDuration.String(), d.String())
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := postpone(d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}
