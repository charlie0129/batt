package daemon

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/peterneutron/powerkit-go/pkg/powerkit"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/config"
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
	sysInfo, err := powerkit.GetSystemInfo()
	if err != nil {
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%", conf.UpperLimit(), conf.LowerLimit())
	} else {
		charge := sysInfo.IOKit.Battery.CurrentCharge
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

func setAdapter(c *gin.Context) {
	var d bool
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if d {
		if err := powerkit.SetAdapterState(powerkit.AdapterActionOn); err != nil {
			logrus.Errorf("enablePowerAdapter failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		logrus.Infof("enabled power adapter")
	} else {
		if err := powerkit.SetAdapterState(powerkit.AdapterActionOff); err != nil {
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
	sysInfo, err := powerkit.GetSystemInfo()
	if err != nil {
		logrus.Errorf("getAdapter/getSystemInfo failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// The API returns 'true' if the adapter is considered enabled.
	c.IndentedJSON(http.StatusOK, sysInfo.SMC.State.IsAdapterEnabled)
}

func getCharging(c *gin.Context) {
	sysInfo, err := powerkit.GetSystemInfo()
	if err != nil {
		logrus.Errorf("getSystemInfo for getCharging failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, sysInfo.IOKit.State.IsCharging)
}

func getSystemInfo(c *gin.Context) {
	sysInfo, err := powerkit.GetSystemInfo()
	if err != nil {
		logrus.Errorf("getSystemInfo failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.IndentedJSON(http.StatusOK, sysInfo)
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
	sysInfo, err := powerkit.GetSystemInfo()
	if err != nil {
		logrus.Errorf("getCurrentCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, sysInfo.IOKit.Battery.CurrentCharge)
}

func getPluggedIn(c *gin.Context) {
	sysInfo, err := powerkit.GetSystemInfo()
	if err != nil {
		logrus.Errorf("getPluggedIn failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, sysInfo.IOKit.State.IsConnected)
}

func getChargingControlCapable(c *gin.Context) {
	// powerkit abstracts this away. If the library is running, we assume it's capable.
	c.IndentedJSON(http.StatusOK, true)
}

func getVersion(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, version.Version)
}
