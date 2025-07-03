package smc

import (
	"bytes"

	"github.com/sirupsen/logrus"
)

// IsChargingEnabled returns whether charging is enabled.
func (c *AppleSMC) IsChargingEnabled() (bool, error) {
	logrus.Tracef("IsChargingEnabled called")

	// Pre-Tahoe firmware versions.
	if c.capabilities[ChargingKey1] && c.capabilities[ChargingKey2] {
		v, err := c.Read(ChargingKey1) // Key1 is enough, we can skip key2.
		if err != nil {
			return false, err
		}

		ret := len(v.Bytes) == 1 && v.Bytes[0] == 0x0
		logrus.Tracef("IsChargingEnabled returned %t", ret)

		return ret, nil
	}

	// Tahoe firmware versions.
	v, err := c.Read(ChargingKey3)
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 4 && bytes.Equal(v.Bytes, []byte{0x00, 0x00, 0x00, 0x00})
	logrus.Tracef("IsChargingEnabled returned %t", ret)

	return ret, nil
}

func (c *AppleSMC) IsChargingControlCapable() bool {
	logrus.Tracef("IsChargingControlCapable called")

	for _, key := range []string{ChargingKey1, ChargingKey2, ChargingKey3} {
		if c.capabilities[key] {
			logrus.Tracef("IsChargingControlCapable returned true for key %s", key)
			return true
		}
	}

	return false
}

// EnableCharging enables charging.
func (c *AppleSMC) EnableCharging() error {
	logrus.Tracef("EnableCharging called")

	// Pre-Tahoe firmware versions.
	if c.capabilities[ChargingKey1] && c.capabilities[ChargingKey2] {
		err := c.Write(ChargingKey1, []byte{0x0})
		if err != nil {
			return err
		}
		err = c.Write(ChargingKey2, []byte{0x0})
		if err != nil {
			return err
		}
		return nil
	}

	// Tahoe firmware versions.
	return c.Write(ChargingKey3, []byte{0x00, 0x00, 0x00, 0x00})
}

// DisableCharging disables charging.
func (c *AppleSMC) DisableCharging() error {
	logrus.Tracef("DisableCharging called")

	// Pre-Tahoe firmware versions.
	if c.capabilities[ChargingKey1] && c.capabilities[ChargingKey2] {
		err := c.Write(ChargingKey1, []byte{0x2})
		if err != nil {
			return err
		}
		err = c.Write(ChargingKey2, []byte{0x2})
		if err != nil {
			return err
		}
		return nil
	}

	// Tahoe firmware versions.
	return c.Write(ChargingKey3, []byte{0x01, 0x00, 0x00, 0x00})
}
