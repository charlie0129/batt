package smc

import (
	"fmt"

	"github.com/charlie0129/gosmc"
	"github.com/sirupsen/logrus"
)

type Connection struct {
	*gosmc.Connection
}

func New() *Connection {
	return &Connection{
		Connection: gosmc.New(),
	}
}

func (c *Connection) Open() error {
	return c.Connection.Open()
}
func (c *Connection) Close() error {
	return c.Connection.Close()
}

func (c *Connection) Read(key string) (gosmc.SMCVal, error) {
	logrus.Tracef("trying to read %s", key)

	v, err := c.Connection.Read(key)
	if err != nil {
		return v, err
	}

	logrus.Tracef("read %s succeed, value=%#v", key, v)

	return v, nil
}

func (c *Connection) Write(key string, value string) error {
	logrus.Tracef("trying to write %s to %s", value, key)

	err := c.Connection.Write(key, value)
	if err != nil {
		return err
	}

	logrus.Tracef("write %s to %s succeed", value, key)

	return nil
}

func (c *Connection) IsChargingEnabled() (bool, error) {
	logrus.Tracef("IsChargingEnabled called")

	v, err := c.Read("CH0B")
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 1 && v.Bytes[0] == 0x0
	logrus.Tracef("IsChargingEnabled returned %t", ret)

	return ret, nil
}

func (c *Connection) EnableCharging() error {
	logrus.Tracef("EnableCharging called")

	err := c.Write("CH0B", "00")
	if err != nil {
		return err
	}

	err = c.Write("CH0C", "00")
	if err != nil {
		return err
	}

	return c.EnableAdapter()
}

func (c *Connection) DisableCharging() error {
	logrus.Tracef("DisableCharging called")

	err := c.Write("CH0B", "02")
	if err != nil {
		return err
	}

	return c.Write("CH0C", "02")
}

func (c *Connection) IsAdapterEnabled() (bool, error) {
	logrus.Tracef("IsAdapterEnabled called")

	v, err := c.Read("CH0I")
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 1 && v.Bytes[0] == 0x0
	logrus.Tracef("IsAdapterEnabled returned %t", ret)

	return ret, nil
}

func (c *Connection) EnableAdapter() error {
	logrus.Tracef("EnableAdapter called")

	return c.Write("CH0I", "00")
}

func (c *Connection) DisableAdapter() error {
	logrus.Tracef("DisableAdapter called")

	return c.Write("CH0I", "01")
}

func (c *Connection) GetBatteryCharge() (int, error) {
	logrus.Tracef("GetBatteryCharge called")

	// BUIC (arm64)
	// BBIF (intel)
	v, err := c.Read("BUIC")
	if err != nil {
		return 0, err
	}

	if len(v.Bytes) != 1 {
		return 0, fmt.Errorf("incorrect data length %d!=1", len(v.Bytes))
	}

	return int(v.Bytes[0]), nil
}

func (c *Connection) IsPluggedIn() (bool, error) {
	logrus.Tracef("IsPluggedIn called")

	v, err := c.Read("AC-W")
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 1 && v.Bytes[0] == 0x1
	logrus.Tracef("IsPluggedIn returned %t", ret)

	return ret, nil
}
