package smc

// Various SMC keys for amd64 (Intel 64).
// This file is not used currently because we have no
// plan to support Classic Intel MacBooks. However,
// if we want to, we have something to work on.
const (
	MagSafeLedKey                    = "ACLC" // Not verified yet.
	ACPowerKey                       = "AC-W" // Not verified yet.
	ChargingKey1                     = "CH0B" // Not verified yet.
	ChargingKey2                     = "CH0C" // Not verified yet.
	ChargingKey3                     = "CHTE" // Not verified yet.
	FirmwareChargeLimitActivationKey = "bfF0" // Not verified yet.
	FirmwareChargeLimitUpperKey      = "bfD0" // Not verified yet.
	FirmwareChargeLimitLowerKey      = "bfE0" // Not verified yet.
	AdapterKey1                      = "CH0K"
	AdapterKey2                      = "CH0J"
	AdapterKey3                      = "CHIE"
	BatteryChargeKey                 = "BBIF"
	DCInCurrentKey                   = "ID0R"
	DCInVoltageKey                   = "VD0R"
	DCInPowerKey                     = "PDTR"
	BatteryCurrentKey                = "B0AC"
	BatteryVoltageKey                = "B0AV"
	BatteryPowerKey                  = "PPBR"
)

var allKeys = []string{
	MagSafeLedKey,
	ACPowerKey,
	ChargingKey1,
	ChargingKey2,
	ChargingKey3,
	FirmwareChargeLimitActivationKey,
	FirmwareChargeLimitUpperKey,
	FirmwareChargeLimitLowerKey,
	AdapterKey1,
	AdapterKey2,
	AdapterKey3,
	BatteryChargeKey,
	DCInCurrentKey,
	DCInVoltageKey,
	DCInPowerKey,
	BatteryCurrentKey,
	BatteryVoltageKey,
	BatteryPowerKey,
}
