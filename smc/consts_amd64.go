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
	AdapterKey       = "CH0K"
	BatteryChargeKey = "BBIF"
)
