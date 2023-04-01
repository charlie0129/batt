package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func getLimit(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, config.Limit)
}

func setLimit(c *gin.Context) {
	var l int
	if err := c.BindJSON(&l); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	config.Limit = l
	if err := saveConfig(); err != nil {
		logrus.Errorf("saveConfig failed: %w", err)
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	// Immediate single maintain loop, to avoid waiting for the next loop
	maintainLoop()
	c.IndentedJSON(http.StatusCreated, l)
	logrus.Infof("set charging limit to %d", l)
}

func enableMaintain(c *gin.Context) {
	config.Maintain = true
	if err := saveConfig(); err != nil {
		logrus.Errorf("saveConfig failed: %w", err)
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	// Immediate single maintain loop, to avoid waiting for the next loop
	maintainLoop()
	c.IndentedJSON(http.StatusCreated, true)
	logrus.Infof("maintain enabled")
}

func disableMaintain(c *gin.Context) {
	config.Maintain = false
	if err := saveConfig(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	// Reset charging
	if err := smcConn.EnableCharging(); err != nil {
		logrus.Errorf("EnableCharging failed: %w", err)
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.IndentedJSON(http.StatusCreated, false)
	logrus.Infof("maintain disabled")
}
