package smc

// Various SMC keys for amd64 (Intel 64).
// This file is not used currently because we have no
// plan to support Classic Intel MacBooks. However,
// if we want to, we have something to work on.
const (
	MagSafeLedKey    = "ACLC" // Not verified yet.
	ACPowerKey       = "AC-W" // Not verified yet.
	ChargingKey1     = "CH0B" // Not verified yet.
	ChargingKey2     = "CH0C" // Not verified yet.
	ChargingKey3     = "CHTE" // Not verified yet.
	ChargingKey4     = "CHTC" // Not verified yet, macOS 27+ (Sequoia).
	AdapterKey       = "CH0K"
	AdapterKey1      = "CH0I" // Not verified yet.
	AdapterKey2      = "CH0J" // Not verified yet.
	AdapterKey3      = "CHIE" // Not verified yet.
	AdapterKey4      = "CHIB" // Not verified yet, macOS 27+ (Sequoia).
	AdapterKey5      = "CHIC" // Not verified yet, macOS 27+ (Sequoia).
	BatteryChargeKey = "BBIF"

	// Power Telemetry Keys
	DCInCurrentKey    = "ID0R"
	DCInVoltageKey    = "VD0R"
	DCInPowerKey      = "PDTR"
	BatteryCurrentKey = "B0AC"
	BatteryVoltageKey = "B0AV"
	BatteryPowerKey   = "PPBR"
)

var allKeys = []string{
	MagSafeLedKey,
	ACPowerKey,
	ChargingKey1,
	ChargingKey2,
	ChargingKey3,
	ChargingKey4,
	AdapterKey,
	AdapterKey1,
	AdapterKey2,
	AdapterKey3,
	AdapterKey4,
	AdapterKey5,
	BatteryChargeKey,
	DCInCurrentKey,
	DCInVoltageKey,
	DCInPowerKey,
	BatteryCurrentKey,
	BatteryVoltageKey,
	BatteryPowerKey,
}

