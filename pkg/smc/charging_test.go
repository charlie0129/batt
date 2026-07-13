package smc

import (
	"bytes"
	"testing"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/compatibility"
)

func smcValue(t *testing.T, key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
	t.Helper()
	value, err := gosmc.NewValue(key, dataType, data)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func openMockSMC(t *testing.T, values ...gosmc.Value) *AppleSMC {
	t.Helper()
	client := NewMockValues(values...)
	if err := client.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestChargeControlModeFromKeys(t *testing.T) {
	tests := []struct {
		name   string
		values []gosmc.Value
		want   compatibility.ChargeControlMode
	}{
		{
			name: "firmware key takes precedence",
			values: []gosmc.Value{
				smcValue(t, ChargingKey1, gosmc.TypeUInt8, 0),
				smcValue(t, ChargingKey2, gosmc.TypeUInt8, 0),
				smcValue(t, FirmwareChargeLimitActivationKey, gosmc.TypeUInt8, 0),
				smcValue(t, FirmwareChargeLimitUpperKey, gosmc.TypeUInt32, 0, 0, 0, 0),
				smcValue(t, FirmwareChargeLimitLowerKey, gosmc.TypeUInt32, 0, 0, 0, 0),
			},
			want: compatibility.ChargeControlFirmware,
		},
		{
			name: "zero-sized firmware placeholder does not override legacy keys",
			values: []gosmc.Value{
				smcValue(t, ChargingKey1, gosmc.TypeUInt8, 0),
				smcValue(t, ChargingKey2, gosmc.TypeUInt8, 0),
				smcValue(t, FirmwareChargeLimitActivationKey, gosmc.TypeUInt8),
				smcValue(t, FirmwareChargeLimitUpperKey, gosmc.TypeUInt32, 0, 0, 0, 0),
				smcValue(t, FirmwareChargeLimitLowerKey, gosmc.TypeUInt32, 0, 0, 0, 0),
			},
			want: compatibility.ChargeControlLegacy,
		},
		{
			name: "paired legacy keys",
			values: []gosmc.Value{
				smcValue(t, ChargingKey1, gosmc.TypeUInt8, 0),
				smcValue(t, ChargingKey2, gosmc.TypeUInt8, 0),
			},
			want: compatibility.ChargeControlLegacy,
		},
		{
			name: "Tahoe legacy key",
			values: []gosmc.Value{
				smcValue(t, ChargingKey3, gosmc.TypeUInt32, 0, 0, 0, 0),
			},
			want: compatibility.ChargeControlLegacy,
		},
		{
			name: "incomplete legacy keys are unsupported",
			values: []gosmc.Value{
				smcValue(t, ChargingKey1, gosmc.TypeUInt8, 0),
			},
			want: compatibility.ChargeControlUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := openMockSMC(t, test.values...)
			if got := client.ChargeControlMode(); got != test.want {
				t.Fatalf("ChargeControlMode() = %q, want %q; capabilities=%v", got, test.want, client.capabilities)
			}
		})
	}
}

func TestEnsureFirmwareChargeLimit(t *testing.T) {
	client := openMockSMC(t,
		smcValue(t, FirmwareChargeLimitActivationKey, gosmc.TypeUInt8, 0),
		smcValue(t, FirmwareChargeLimitUpperKey, gosmc.TypeUInt32, 0, 0, 0, 0),
		smcValue(t, FirmwareChargeLimitLowerKey, gosmc.TypeUInt32, 0, 0, 0, 0),
	)

	changed, err := client.EnsureFirmwareChargeLimit(48, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected initial reconciliation to change SMC state")
	}

	state, err := client.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 48 || state.Upper != 50 {
		t.Fatalf("unexpected firmware state: %+v", state)
	}
	upper, err := client.Read(FirmwareChargeLimitUpperKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(upper.Bytes, []byte{0x32, 0x00, 0x00, 0x00}) {
		t.Fatalf("upper bytes = % x, want little-endian 32 00 00 00", upper.Bytes)
	}

	changed, err = client.EnsureFirmwareChargeLimit(48, 50)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("correct firmware state should not be rewritten")
	}

	changed, err = client.EnsureFirmwareChargeLimitDisabled()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("active limit should be deactivated")
	}
	state, err = client.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active {
		t.Fatal("firmware limit is still active")
	}
}

func TestLegacyChargingBehavior(t *testing.T) {
	client := openMockSMC(t,
		smcValue(t, ChargingKey1, gosmc.TypeUInt8, 0),
		smcValue(t, ChargingKey2, gosmc.TypeUInt8, 0),
	)
	if err := client.DisableCharging(); err != nil {
		t.Fatal(err)
	}
	enabled, err := client.IsChargingEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("charging should be disabled")
	}
	if err := client.EnableCharging(); err != nil {
		t.Fatal(err)
	}
	enabled, err = client.IsChargingEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("charging should be enabled")
	}
}
