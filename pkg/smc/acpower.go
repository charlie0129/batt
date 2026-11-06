package smc

import (
	"fmt"

	"github.com/charlie0129/batt/pkg/logging"
)

// IsPluggedIn returns whether the device is plugged in.
func (c *AppleSMC) IsPluggedIn() (bool, error) {
	logging.Trace("IsPluggedIn called")

	v, err := c.Read(ACPowerKey)
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 1 && int8(v.Bytes[0]) > 0
	logging.Trace(fmt.Sprintf("IsPluggedIn returned %t", ret))

	return ret, nil
}
