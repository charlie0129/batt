package main

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func getConfig(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, config)
}

func setConfig(c *gin.Context) {
	var cfg Config
	if err := c.BindJSON(&cfg); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if cfg.LoopIntervalSeconds < 1 {
		err := fmt.Errorf("loopIntervalSeconds must be grater than 1, got %d", cfg.LoopIntervalSeconds)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if cfg.Limit < 10 || cfg.Limit > 100 {
		err := fmt.Errorf("limit must be between 10 and 100, got %d", cfg.Limit)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	config = cfg
	if err := saveConfig(); err != nil {
		logrus.Errorf("saveConfig failed: %w", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set config: %#v", cfg)

	// Immediate single maintain loop, to avoid waiting for the next loop
	maintainLoop()
	c.IndentedJSON(http.StatusCreated, "ok")
}

func getLimit(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, config.Limit)
}

func setLimit(c *gin.Context) {
	var l int
	if err := c.BindJSON(&l); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if l < 10 || l > 100 {
		err := fmt.Errorf("limit must be between 10 and 100, got %d", l)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	config.Limit = l
	if err := saveConfig(); err != nil {
		logrus.Errorf("saveConfig failed: %w", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set charging limit to %d", l)

	var msg string
	charge, err := smcConn.GetBatteryCharge()
	if err != nil {
		msg = fmt.Sprintf("set charging limit to %d", l)
	} else {
		msg = fmt.Sprintf("set charging limit to %d, current charge: %d", l, charge)
		if charge > config.Limit {
			msg += ", you may need to drain your battery below the limit to see any effect"
		}
	}

	// Immediate single maintain loop, to avoid waiting for the next loop
	maintainLoop()

	c.IndentedJSON(http.StatusCreated, msg)
}

func setPreventIdleSleep(c *gin.Context) {
	var p bool
	if err := c.BindJSON(&p); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	config.PreventIdleSleep = p
	if err := saveConfig(); err != nil {
		logrus.Errorf("saveConfig failed: %w", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set prevent idle sleep to %t", p)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setDisableChargingPreSleep(c *gin.Context) {
	var d bool
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	config.DisableChargingPreSleep = d
	if err := saveConfig(); err != nil {
		logrus.Errorf("saveConfig failed: %w", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set disable charging pre sleep to %t", d)

	c.IndentedJSON(http.StatusCreated, "ok")
}
