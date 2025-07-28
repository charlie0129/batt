package smc

import (
	"encoding/binary"
	"math"

	"github.com/charlie0129/batt/pkg/types"
	"github.com/pkg/errors"
)

// GetPowerTelemetry reads the raw SMC keys and returns calculated power metrics.
func (c *AppleSMC) GetPowerTelemetry() (*types.PowerTelemetry, error) {
	// Read all necessary keys first.
	dcinCurrent, err := c.Read(DCInCurrentKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read dcin current")
	}
	dcinVoltage, err := c.Read(DCInVoltageKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read dcin voltage")
	}
	battCurrent, err := c.Read(BatteryCurrentKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read battery current")
	}
	battVoltage, err := c.Read(BatteryVoltageKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read battery voltage")
	}

	// Decode all values and store them in variables.
	acAmperage := decodeFloat(dcinCurrent.Bytes)
	acVoltage := decodeFloat(dcinVoltage.Bytes)
	pAC := acAmperage * acVoltage // Calculate power from decoded values.

	pBatt := (float64(decodeInt(battCurrent.Bytes)) / 1000.0) * (float64(decodeUint(battVoltage.Bytes)) / 1000.0)
	pSystem := pAC - pBatt

	// Return the struct with all fields correctly populated.
	return &types.PowerTelemetry{
		ACPower:      pAC,
		BatteryPower: pBatt,
		SystemPower:  pSystem,
		ACVoltage:    acVoltage,
		ACAmperage:   acAmperage,
	}, nil
}

// decodeFloat decodes a 4-byte slice into a little-endian float32.
func decodeFloat(b []byte) float64 {
	if len(b) != 4 {
		return 0
	}
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
}

// decodeInt decodes a 2-byte slice into a little-endian int16.
func decodeInt(b []byte) int16 {
	if len(b) != 2 {
		return 0
	}
	return int16(binary.LittleEndian.Uint16(b))
}

// decodeUint decodes a 2-byte slice into a little-endian uint16.
func decodeUint(b []byte) uint16 {
	if len(b) != 2 {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}
