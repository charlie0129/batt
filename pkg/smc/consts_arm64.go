package smc

// Various SMC keys for arm64 (Apple Silicon)
const (
	MagSafeLedKey = "ACLC"
	ACPowerKey    = "AC-W"
	ChargingKey1  = "CH0B"
	ChargingKey2  = "CH0C"
	// ChargingKey3 is used for Tahoe firmware versions.
	ChargingKey3 = "CHTE"
	// ChargingKey4 is used for macOS 27+ (Sequoia) firmware versions.
	ChargingKey4 = "CHTC"
	AdapterKey1  = "CH0I"
	AdapterKey2  = "CH0J"
	// AdapterKey3 is used for Tahoe firmware versions.
	AdapterKey3 = "CHIE"
	// AdapterKey4 is used for macOS 27+ (Sequoia) firmware versions.
	AdapterKey4 = "CHIB"
	// AdapterKey5 is used for macOS 27+ (Sequoia) firmware versions.
	AdapterKey5      = "CHIC"
	BatteryChargeKey = "BUIC"

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
