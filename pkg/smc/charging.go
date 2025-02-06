package smc

import "github.com/sirupsen/logrus"

// IsChargingEnabled returns whether charging is enabled.
func (c *AppleSMC) IsChargingEnabled() (bool, error) {
	logrus.Tracef("IsChargingEnabled called")

	v, err := c.Read(ChargingKey1)
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 1 && v.Bytes[0] == 0x0
	logrus.Tracef("IsChargingEnabled returned %t", ret)

	return ret, nil
}

// EnableCharging enables charging.
func (c *AppleSMC) EnableCharging() error {
	logrus.Tracef("EnableCharging called")

	// CHSC
	err := c.Write(ChargingKey1, []byte{0x0})
	if err != nil {
		return err
	}

	err = c.Write(ChargingKey2, []byte{0x0})
	if err != nil {
		return err
	}

	return c.EnableAdapter()
}

// DisableCharging disables charging.
func (c *AppleSMC) DisableCharging() error {
	logrus.Tracef("DisableCharging called")

	err := c.Write(ChargingKey1, []byte{0x2})
	if err != nil {
		return err
	}

	return c.Write(ChargingKey2, []byte{0x2})
}
