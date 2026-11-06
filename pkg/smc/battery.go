package smc

import (
	"fmt"

	"github.com/charlie0129/batt/pkg/logging"
)

// GetBatteryCharge returns the battery charge.
func (c *AppleSMC) GetBatteryCharge() (int, error) {
	logging.Trace("GetBatteryCharge called")

	v, err := c.Read(BatteryChargeKey)
	if err != nil {
		return 0, err
	}

	if len(v.Bytes) != 1 {
		return 0, fmt.Errorf("incorrect data length %d!=1", len(v.Bytes))
	}

	return int(v.Bytes[0]), nil
}
