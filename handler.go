package main

import (
	"fmt"
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

	if l < 10 || l > 100 {
		err := fmt.Errorf("limit must be between 10 and 100, got %d", l)
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	config.Limit = l
	if err := saveConfig(); err != nil {
		logrus.Errorf("saveConfig failed: %w", err)
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

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
	logrus.Infof("set charging limit to %d", l)
}
