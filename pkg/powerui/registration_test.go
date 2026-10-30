package powerui

import (
	"reflect"
	"strings"
	"testing"
)

const manualRegistration = `Battery level limits:
(
    {
        Terminated = 0;
        chargeSocLimitDrain = 1;
        chargeSocLimitIsEOC = 1;
        chargeSocLimitNoChargeToFull = 0;
        chargeSocLimitOwner = 123;
        chargeSocLimitReason = manualChargeLimit;
        chargeSocLimitSoc = 55;
    }
)
`

func TestManualLimitRegistration(t *testing.T) {
	for _, test := range []struct {
		name, output string
		want, bad    bool
	}{
		{"released", "No battery level limits set\n", false, false},
		{"empty array", "Battery level limits:\n(\n)", false, false},
		{"commas without rows", "Battery level limits:\n(,,)", false, true},
		{"leading comma", strings.Replace(manualRegistration, "(\n", "(,\n", 1), false, true},
		{"trailing comma", strings.Replace(manualRegistration, "    }\n)", "    },\n)", 1), false, true},
		{"manual", manualRegistration, true, false},
		{"multiple policies", strings.Replace(manualRegistration, "    }\n)", "    },\n"+registrationRow.FindString(strings.ReplaceAll(manualRegistration, "manualChargeLimit", "otherOSPolicy"))+"\n)", 1), true, false},
		{"quoted reason", strings.ReplaceAll(manualRegistration, "manualChargeLimit", `"manualChargeLimit"`), true, false},
		{"other policy", strings.ReplaceAll(manualRegistration, "manualChargeLimit", "otherOSPolicy"), false, false},
		{"empty response", "", false, true},
		{"unsupported command", "Usage: pmset", false, true},
		{"truncated", strings.TrimSuffix(manualRegistration, ")\n"), false, true},
		{"missing reason", strings.ReplaceAll(manualRegistration, "chargeSocLimitReason = manualChargeLimit;", ""), false, true},
		{"duplicate", strings.ReplaceAll(manualRegistration, "Terminated = 0;", "Terminated = 0; Terminated = 0;"), false, true},
		{"unknown flag", strings.ReplaceAll(manualRegistration, "chargeSocLimitDrain = 1", "chargeSocLimitDrain = 2"), false, true},
		{"invalid owner", strings.ReplaceAll(manualRegistration, "chargeSocLimitOwner = 123", "chargeSocLimitOwner = -1"), false, true},
		{"unexpected structure", "Battery level limits:\n(garbage)", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := manualLimitRegistered(test.output)
			if (err != nil) != test.bad || got != test.want {
				t.Fatalf("registered=%t err=%v", got, err)
			}
		})
	}
}

func TestActiveManualTargets(t *testing.T) {
	row := registrationRow.FindString(manualRegistration)
	for _, tc := range []struct {
		name, output string
		want         []int
	}{
		{"empty", "No battery level limits set", nil},
		{"manual", manualRegistration, []int{55}},
		{"other policy", strings.ReplaceAll(manualRegistration, "manualChargeLimit", "otherOSPolicy"), nil},
		{"terminated", strings.ReplaceAll(manualRegistration, "Terminated = 0", "Terminated = 1"), nil},
		{"mirror", "Battery level limits: (" + row + "," + row + ")", []int{55, 55}},
		{"stale mirror", "Battery level limits: (" + row + "," + strings.ReplaceAll(row, "Soc = 55", "Soc = 70") + ")", []int{55, 70}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := manualLimitTargets(tc.output, false)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, err)
			}
		})
	}
	// Release still sees terminated entries, preserving its conservative cleanup.
	if registered, err := manualLimitRegistered(strings.ReplaceAll(manualRegistration, "Terminated = 0", "Terminated = 1")); err != nil || !registered {
		t.Fatal(registered, err)
	}
}
