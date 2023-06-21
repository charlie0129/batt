package smc

import "github.com/sirupsen/logrus"

// IsPluggedIn returns whether the device is plugged in.
func (c *Connection) IsPluggedIn() (bool, error) {
	logrus.Tracef("IsPluggedIn called")

	v, err := c.Read(ACPowerKey)
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 1 && int8(v.Bytes[0]) > 0
	logrus.Tracef("IsPluggedIn returned %t", ret)

	return ret, nil
}
