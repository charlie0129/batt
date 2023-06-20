package smc

import (
	"fmt"

	"github.com/charlie0129/gosmc"
	"github.com/sirupsen/logrus"
)

// Connection is a wrapper of gosmc.Connection.
type Connection struct {
	*gosmc.Connection
}

// MagSafeLedState is the state of the MagSafe LED.
type MagSafeLedState int

// Representation of MagSafeLedState.
const (
	LedOff       MagSafeLedState = 1
	LedGreen     MagSafeLedState = 3
	LedOrange    MagSafeLedState = 4
	LedErrorOnce MagSafeLedState = 5
	LedErrorPerm MagSafeLedState = 6
)

// New returns a new Connection.
func New() *Connection {
	return &Connection{
		Connection: gosmc.New(),
	}
}

// Open opens the connection.
func (c *Connection) Open() error {
	return c.Connection.Open()
}

// Close closes the connection.
func (c *Connection) Close() error {
	return c.Connection.Close()
}

// Read reads a value from SMC.
func (c *Connection) Read(key string) (gosmc.SMCVal, error) {
	logrus.Tracef("trying to read %s", key)

	v, err := c.Connection.Read(key)
	if err != nil {
		return v, err
	}

	logrus.Tracef("read %s succeed, value=%#v", key, v)

	return v, nil
}

// Write writes a value to SMC.
func (c *Connection) Write(key string, value []byte) error {
	logrus.Tracef("trying to write %#v to %s", value, key)

	err := c.Connection.Write(key, value)
	if err != nil {
		return err
	}

	logrus.Tracef("write %#v to %s succeed", value, key)

	return nil
}

// IsChargingEnabled returns whether charging is enabled.
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

// EnableCharging enables charging.
func (c *Connection) EnableCharging() error {
	logrus.Tracef("EnableCharging called")

	// CHSC
	err := c.Write("CH0B", []byte{0x0})
	if err != nil {
		return err
	}

	err = c.Write("CH0C", []byte{0x0})
	if err != nil {
		return err
	}

	return c.EnableAdapter()
}

// DisableCharging disables charging.
func (c *Connection) DisableCharging() error {
	logrus.Tracef("DisableCharging called")

	err := c.Write("CH0B", []byte{0x2})
	if err != nil {
		return err
	}

	return c.Write("CH0C", []byte{0x2})
}

// IsAdapterEnabled returns whether the adapter is plugged in.
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

// EnableAdapter enables the adapter.
func (c *Connection) EnableAdapter() error {
	logrus.Tracef("EnableAdapter called")

	return c.Write("CH0I", []byte{0x0})
}

// DisableAdapter disables the adapter.
func (c *Connection) DisableAdapter() error {
	logrus.Tracef("DisableAdapter called")

	return c.Write("CH0I", []byte{0x1})
}

// GetBatteryCharge returns the battery charge.
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

// IsPluggedIn returns whether the device is plugged in.
func (c *Connection) IsPluggedIn() (bool, error) {
	logrus.Tracef("IsPluggedIn called")

	v, err := c.Read("AC-W")
	if err != nil {
		return false, err
	}

	ret := len(v.Bytes) == 1 && int8(v.Bytes[0]) > 0
	logrus.Tracef("IsPluggedIn returned %t", ret)

	return ret, nil
}

// SetMagSafeLedState .
func (c *Connection) SetMagSafeLedState(state MagSafeLedState) error {
	logrus.Tracef("SetMagSafeLedState(%v) called", state)

	return c.Write("ACLC", []byte{byte(state)})
}

// GetMagSafeLedState .
func (c *Connection) GetMagSafeLedState() (MagSafeLedState, error) {
	logrus.Tracef("GetMagSafeLedState called")

	v, err := c.Read("ACLC")
	if err != nil || len(v.Bytes) != 1 {
		return LedOrange, err
	}

	rawState := MagSafeLedState(v.Bytes[0])
	ret := LedOrange
	switch rawState {
	case LedOff, LedGreen, LedOrange, LedErrorOnce, LedErrorPerm:
		ret = rawState
	case 2:
		ret = LedGreen
	}
	logrus.Tracef("GetMagSafeLedState returned %v", ret)
	return ret, nil
}

// SetMagSafeCharging .
func (c *Connection) SetMagSafeCharging(charging bool) error {
	state := LedGreen
	if charging {
		state = LedOrange
	}
	return c.SetMagSafeLedState(state)
}

// IsMagSafeCharging .
func (c *Connection) IsMagSafeCharging() (bool, error) {
	state, err := c.GetMagSafeLedState()

	return state != LedGreen, err
}
