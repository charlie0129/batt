package smc

// Various SMC keys for arm64 (Apple Silicon)
const (
	MagSafeLedKey    = "ACLC"
	ACPowerKey       = "AC-W"
	ChargingKey1     = "CH0B"
	ChargingKey2     = "CH0C"
	ChargingKey3     = "CHTE"
	AdapterKey1      = "CH0I"
	AdapterKey2      = "CH0J"
	BatteryChargeKey = "BUIC"
)

var allKeys = []string{
	MagSafeLedKey,
	ACPowerKey,
	ChargingKey1,
	ChargingKey2,
	ChargingKey3,
	AdapterKey1,
	AdapterKey2,
	BatteryChargeKey,
}
