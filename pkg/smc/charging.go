package smc

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/compatibility"
)

var (
	ErrNoChargingCapability     = errors.New("no charging control capability found")
	ErrNotLegacyChargeControl   = errors.New("direct charging control is unavailable")
	ErrNotFirmwareChargeControl = errors.New("firmware charge-limit control is unavailable")
)

// ChargeControlMode determines the charge-control mechanism from SMC key
// presence, not the macOS version. bfF0 takes precedence because old macOS
// versions may receive the newer firmware.
func (c *AppleSMC) ChargeControlMode() compatibility.ChargeControlMode {
	if c.capabilities[FirmwareChargeLimitActivationKey] {
		return compatibility.ChargeControlFirmware
	}
	if (c.capabilities[ChargingKey1] && c.capabilities[ChargingKey2]) || c.capabilities[ChargingKey3] {
		return compatibility.ChargeControlLegacy
	}
	return compatibility.ChargeControlUnsupported
}

func (c *AppleSMC) IsChargingControlCapable() bool {
	return c.ChargeControlMode() != compatibility.ChargeControlUnsupported
}

func (c *AppleSMC) IsDirectChargingControlCapable() bool {
	return c.ChargeControlMode() == compatibility.ChargeControlLegacy
}

// IsChargingEnabled returns whether direct, legacy charging is enabled.
func (c *AppleSMC) IsChargingEnabled() (bool, error) {
	logrus.Trace("IsChargingEnabled called")
	if c.ChargeControlMode() != compatibility.ChargeControlLegacy {
		return false, ErrNotLegacyChargeControl
	}

	if c.capabilities[ChargingKey1] && c.capabilities[ChargingKey2] {
		v, err := c.Read(ChargingKey1)
		if err != nil {
			return false, err
		}
		return len(v.Bytes) == 1 && v.Bytes[0] == 0x00, nil
	}

	v, err := c.Read(ChargingKey3)
	if err != nil {
		return false, err
	}
	return len(v.Bytes) == 4 && bytes.Equal(v.Bytes, []byte{0x00, 0x00, 0x00, 0x00}), nil
}

// EnableCharging enables direct, legacy charging.
func (c *AppleSMC) EnableCharging() error {
	logrus.Trace("EnableCharging called")
	if c.ChargeControlMode() != compatibility.ChargeControlLegacy {
		return ErrNotLegacyChargeControl
	}

	if c.capabilities[ChargingKey1] && c.capabilities[ChargingKey2] {
		if err := c.Write(ChargingKey1, []byte{0x00}); err != nil {
			return err
		}
		return c.Write(ChargingKey2, []byte{0x00})
	}
	return c.Write(ChargingKey3, []byte{0x00, 0x00, 0x00, 0x00})
}

// DisableCharging disables direct, legacy charging.
func (c *AppleSMC) DisableCharging() error {
	logrus.Trace("DisableCharging called")
	if c.ChargeControlMode() != compatibility.ChargeControlLegacy {
		return ErrNotLegacyChargeControl
	}

	if c.capabilities[ChargingKey1] && c.capabilities[ChargingKey2] {
		if err := c.Write(ChargingKey1, []byte{0x02}); err != nil {
			return err
		}
		return c.Write(ChargingKey2, []byte{0x02})
	}
	return c.Write(ChargingKey3, []byte{0x01, 0x00, 0x00, 0x00})
}

// FirmwareChargeLimit is the state stored in the macOS 27-era SMC keys.
type FirmwareChargeLimit struct {
	Active bool
	Lower  uint32
	Upper  uint32
}

func (c *AppleSMC) readFirmwareLimit(key string) (uint32, error) {
	v, err := c.Read(key)
	if err != nil {
		return 0, err
	}
	encoded, err := v.Uint64()
	if err != nil {
		return 0, fmt.Errorf("decode %s: %w", key, err)
	}
	if encoded > math.MaxUint32 {
		return 0, fmt.Errorf("decode %s: value %d exceeds uint32", key, encoded)
	}

	// These firmware keys encode ui32 percentages little-endian (for example,
	// 50% is 32 00 00 00). gosmc's typed ui32 codec correctly follows the
	// conventional big-endian SMC representation, so swap the typed value for
	// these exceptional keys.
	return bits.ReverseBytes32(uint32(encoded)), nil
}

// GetFirmwareChargeLimit reads the firmware-managed charge-limit state.
func (c *AppleSMC) GetFirmwareChargeLimit() (FirmwareChargeLimit, error) {
	if c.ChargeControlMode() != compatibility.ChargeControlFirmware {
		return FirmwareChargeLimit{}, ErrNotFirmwareChargeControl
	}

	activation, err := c.Read(FirmwareChargeLimitActivationKey)
	if err != nil {
		return FirmwareChargeLimit{}, err
	}
	if len(activation.Bytes) != 1 {
		return FirmwareChargeLimit{}, fmt.Errorf("%s has incorrect data length %d, want 1", FirmwareChargeLimitActivationKey, len(activation.Bytes))
	}
	upper, err := c.readFirmwareLimit(FirmwareChargeLimitUpperKey)
	if err != nil {
		return FirmwareChargeLimit{}, err
	}
	lower, err := c.readFirmwareLimit(FirmwareChargeLimitLowerKey)
	if err != nil {
		return FirmwareChargeLimit{}, err
	}
	return FirmwareChargeLimit{Active: activation.Bytes[0] == 0x02, Lower: lower, Upper: upper}, nil
}

func (c *AppleSMC) writeFirmwareLimit(key string, value uint32) error {
	return c.WriteUint32(key, bits.ReverseBytes32(value))
}

// EnsureFirmwareChargeLimit repairs the firmware limit only when it differs
// from the requested state. The write order is required by the firmware.
func (c *AppleSMC) EnsureFirmwareChargeLimit(lower, upper int) (bool, error) {
	if c.ChargeControlMode() != compatibility.ChargeControlFirmware {
		return false, ErrNotFirmwareChargeControl
	}
	if lower < 0 || upper > 100 || lower >= upper {
		return false, fmt.Errorf("invalid firmware charge limits %d/%d", lower, upper)
	}

	current, err := c.GetFirmwareChargeLimit()
	if err != nil {
		return false, err
	}
	if current.Active && current.Lower == uint32(lower) && current.Upper == uint32(upper) {
		return false, nil
	}

	if err := c.Write(FirmwareChargeLimitActivationKey, []byte{0x00}); err != nil {
		return false, err
	}
	if err := c.writeFirmwareLimit(FirmwareChargeLimitUpperKey, uint32(upper)); err != nil {
		return false, err
	}
	if err := c.writeFirmwareLimit(FirmwareChargeLimitLowerKey, uint32(lower)); err != nil {
		return false, err
	}
	if err := c.Write(FirmwareChargeLimitActivationKey, []byte{0x02}); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureFirmwareChargeLimitDisabled deactivates a firmware-managed limit.
func (c *AppleSMC) EnsureFirmwareChargeLimitDisabled() (bool, error) {
	if c.ChargeControlMode() != compatibility.ChargeControlFirmware {
		return false, ErrNotFirmwareChargeControl
	}
	v, err := c.Read(FirmwareChargeLimitActivationKey)
	if err != nil {
		return false, err
	}
	if len(v.Bytes) != 1 {
		return false, fmt.Errorf("%s has incorrect data length %d, want 1", FirmwareChargeLimitActivationKey, len(v.Bytes))
	}
	if v.Bytes[0] == 0x00 {
		return false, nil
	}
	return true, c.Write(FirmwareChargeLimitActivationKey, []byte{0x00})
}

// ResetChargeControl restores the platform's default charging behavior.
func (c *AppleSMC) ResetChargeControl() error {
	switch c.ChargeControlMode() {
	case compatibility.ChargeControlLegacy:
		return c.EnableCharging()
	case compatibility.ChargeControlFirmware:
		_, err := c.EnsureFirmwareChargeLimitDisabled()
		return err
	default:
		return ErrNoChargingCapability
	}
}
