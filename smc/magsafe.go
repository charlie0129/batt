package smc

import "github.com/sirupsen/logrus"

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

// SetMagSafeLedState .
func (c *Connection) SetMagSafeLedState(state MagSafeLedState) error {
	logrus.Tracef("SetMagSafeLedState(%v) called", state)

	return c.Write(MagSafeLedKey, []byte{byte(state)})
}

// GetMagSafeLedState .
func (c *Connection) GetMagSafeLedState() (MagSafeLedState, error) {
	logrus.Tracef("GetMagSafeLedState called")

	v, err := c.Read(MagSafeLedKey)
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

// CheckMagSafeExistence .
func (c *Connection) CheckMagSafeExistence() bool {
	_, err := c.Read(MagSafeLedKey)
	return err == nil
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
