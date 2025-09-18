package smc

import "github.com/sirupsen/logrus"

// MagSafeLedState is the state of the MagSafe LED.
type MagSafeLedState uint8

// Representation of MagSafeLedState.
const (
	LEDSystem        MagSafeLedState = 0x00
	LEDOff           MagSafeLedState = 0x01
	LEDGreen         MagSafeLedState = 0x03
	LEDOrange        MagSafeLedState = 0x04
	LEDErrorOnce     MagSafeLedState = 0x05
	LEDErrorPermSlow MagSafeLedState = 0x06
	LEDErrorPermFast MagSafeLedState = 0x07
	LEDErrorPermOff  MagSafeLedState = 0x19
)

// SetMagSafeLedState .
func (c *AppleSMC) SetMagSafeLedState(state MagSafeLedState) error {
	logrus.Tracef("SetMagSafeLedState(%v) called", state)

	return c.Write(MagSafeLedKey, []byte{byte(state)})
}

func (c *AppleSMC) DisableMagSafeLed() error {
	logrus.Tracef("DisableMagSafeLed() called")

	return c.Write(MagSafeLedKey, []byte{byte(LEDOff)})
}

// GetMagSafeLedState .
func (c *AppleSMC) GetMagSafeLedState() (MagSafeLedState, error) {
	logrus.Tracef("GetMagSafeLedState called")

	v, err := c.Read(MagSafeLedKey)
	if err != nil || len(v.Bytes) != 1 {
		return LEDOrange, err
	}

	rawState := MagSafeLedState(v.Bytes[0])
	ret := LEDOrange
	switch rawState {
	case LEDOff, LEDGreen, LEDOrange, LEDErrorOnce, LEDErrorPermSlow:
		ret = rawState
	case 2:
		ret = LEDGreen
	}
	logrus.Tracef("GetMagSafeLedState returned %v", ret)
	return ret, nil
}

// CheckMagSafeExistence .
func (c *AppleSMC) CheckMagSafeExistence() bool {
	_, err := c.Read(MagSafeLedKey)
	return err == nil
}

// SetMagSafeCharging .
func (c *AppleSMC) SetMagSafeCharging(charging bool) error {
	state := LEDGreen
	if charging {
		state = LEDOrange
	}
	return c.SetMagSafeLedState(state)
}

// IsMagSafeCharging .
func (c *AppleSMC) IsMagSafeCharging() (bool, error) {
	state, err := c.GetMagSafeLedState()

	return state == LEDOrange, err
}
