package powerui

import (
	"reflect"
	"strings"
	"testing"
)

const ceilingRegistration = `Battery level limits:
(
    {
        Terminated = 0;
        chargeSocLimitDrain = 0;
        chargeSocLimitIsEOC = 1;
        chargeSocLimitNoChargeToFull = 0;
        chargeSocLimitOwner = 123;
        chargeSocLimitReason = battChargeLimit;
        chargeSocLimitSoc = 55;
    }
)
`

func TestPolicyRegistration(t *testing.T) {
	row := registrationRow.FindString(ceilingRegistration)
	for _, tc := range []struct {
		name, output string
		want         []int
		bad          bool
	}{
		{"released", "No battery level limits set\n", nil, false},
		{"empty array", "Battery level limits: ()", nil, false},
		{"registered", ceilingRegistration, []int{55}, false},
		{"mirror", "Battery level limits: (" + row + "," + row + ")", []int{55, 55}, false},
		{"stale mirror", "Battery level limits: (" + row + "," + strings.ReplaceAll(row, "Soc = 55", "Soc = 70") + ")", []int{55, 70}, false},
		{"ordinary Apple policy", strings.ReplaceAll(ceilingRegistration, "battChargeLimit", "manualChargeLimit"), nil, false},
		{"quoted reason", strings.ReplaceAll(ceilingRegistration, "battChargeLimit", `"battChargeLimit"`), []int{55}, false},
		{"extra field", strings.ReplaceAll(ceilingRegistration, "Terminated = 0;", "Terminated = 0; futureField = ignored;"), []int{55}, false},
		{"discharge enabled", strings.ReplaceAll(ceilingRegistration, "Drain = 0", "Drain = 1"), []int{0}, false},
		{"calibration suppressed", strings.ReplaceAll(ceilingRegistration, "NoChargeToFull = 0", "NoChargeToFull = 1"), []int{0}, false},
		{"unknown flag", strings.ReplaceAll(ceilingRegistration, "Drain = 0", "Drain = 2"), []int{0}, false},
		{"missing flag", strings.ReplaceAll(ceilingRegistration, "chargeSocLimitDrain = 0;", ""), []int{0}, false},
		{"terminated", strings.ReplaceAll(ceilingRegistration, "Terminated = 0", "Terminated = 1"), []int{0}, false},
		{"empty response", "", nil, true},
		{"unsupported command", "Usage: pmset", nil, true},
		{"commas without rows", "Battery level limits: (,,)", nil, true},
		{"leading comma", "Battery level limits: (," + row + ")", nil, true},
		{"trailing comma", "Battery level limits: (" + row + ",)", nil, true},
		{"truncated", strings.TrimSuffix(ceilingRegistration, ")\n"), nil, true},
		{"missing reason", strings.ReplaceAll(ceilingRegistration, "chargeSocLimitReason = battChargeLimit;", ""), nil, true},
		{"missing termination", strings.ReplaceAll(ceilingRegistration, "Terminated = 0;", ""), nil, true},
		{"missing target", strings.ReplaceAll(ceilingRegistration, "chargeSocLimitSoc = 55;", ""), nil, true},
		{"duplicate field", strings.ReplaceAll(ceilingRegistration, "Terminated = 0;", "Terminated = 0; Terminated = 0;"), nil, true},
		{"invalid termination", strings.ReplaceAll(ceilingRegistration, "Terminated = 0", "Terminated = 2"), nil, true},
		{"zero target", strings.ReplaceAll(ceilingRegistration, "Soc = 55", "Soc = 0"), nil, true},
		{"invalid target", strings.ReplaceAll(ceilingRegistration, "Soc = 55", "Soc = 101"), nil, true},
		{"unexpected structure", "Battery level limits: (garbage)", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := policyTargets(tc.output)
			if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestManualPolicyRegistration(t *testing.T) {
	row := registrationRow.FindString(ceilingRegistration)
	manual := strings.ReplaceAll(strings.ReplaceAll(row, "battChargeLimit", "manualChargeLimit"), "Drain = 0", "Drain = 1")
	for _, terminated := range []bool{false, true} {
		other := manual
		if terminated {
			other = strings.ReplaceAll(other, "Terminated = 0", "Terminated = 1")
		}
		targets, active, err := policyTargets("Battery level limits: (" + row + "," + other + ")")
		if err != nil || !reflect.DeepEqual(targets, []int{55}) || active == terminated {
			t.Fatalf("terminated=%t: targets=%v manual=%t err=%v", terminated, targets, active, err)
		}
	}
}
