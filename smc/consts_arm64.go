package smc

// Various SMC keys for arm64 (Apple Silicon)
const (
	MagSafeLedKey         = "ACLC"
	ACPowerKey            = "AC-W"
	ChargingKey1          = "CH0B"
	ChargingKey2          = "CH0C"
	AdapterKey            = "CH0I" // CH0K on Intel, if we need it later
	BatteryChargeKey      = "BUIC"
	BatteryChargeKeyIntel = "BBIF" // TODO: separate Intel and Apple keys using go build tags
)
