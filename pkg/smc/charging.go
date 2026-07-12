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

	// Tahoe / macOS 27+ firmware versions.
	// Both use the same 4-byte format: all zeros means charging enabled.
	chargingKey := c.getChargingKey()
	v, err := c.Read(chargingKey)
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 4 && bytes.Equal(v.Bytes, []byte{0x00, 0x00, 0x00, 0x00})
	logrus.Tracef("IsChargingEnabled (key=%s) returned %t", chargingKey, ret)

	return ret, nil
}

func (c *AppleSMC) IsChargingControlCapable() bool {
	logrus.Tracef("IsChargingControlCapable called")

	for _, key := range []string{ChargingKey1, ChargingKey2, ChargingKey3, ChargingKey4} {
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

	// Tahoe / macOS 27+ firmware versions.
	// Both use the same 4-byte format: all zeros means charging enabled.
	key := c.getChargingKey()
	logrus.Tracef("EnableCharging writing to key %s", key)
	return c.Write(key, []byte{0x00, 0x00, 0x00, 0x00})
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

	// Tahoe / macOS 27+ firmware versions.
	// Both use the same 4-byte format: 0x01 in first byte means charging disabled.
	key := c.getChargingKey()
	logrus.Tracef("DisableCharging writing to key %s", key)
	return c.Write(key, []byte{0x01, 0x00, 0x00, 0x00})
}

// getChargingKey returns the SMC key to use for charging control.
// Priority: ChargingKey4 (macOS 27+) > ChargingKey3 (Tahoe).
// Pre-Tahoe uses ChargingKey1+ChargingKey2 and is handled separately.
func (c *AppleSMC) getChargingKey() string {
	if c.capabilities[ChargingKey4] {
		return ChargingKey4
	}
	return ChargingKey3
}
