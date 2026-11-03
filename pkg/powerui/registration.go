package powerui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Registrations reads powerd independently of PowerUI's feature flag. Do not
// treat a failed command or an unfamiliar output format as an empty policy list.
func (nativeBridge) Registrations() (targets []int, manual bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/pmset", "-g", "battlimit")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.Output()
	if err != nil {
		return nil, false, fmt.Errorf("read native charge registrations: %w", err)
	}
	return policyTargets(string(output))
}

var registrationRow = regexp.MustCompile(`\{[^{}]*\}`)

func policyTargets(output string) (targets []int, manual bool, err error) {
	invalid := fmt.Errorf("unrecognized pmset battery-limit output; registration cannot be confirmed")
	if len(output) > 64*1024 {
		return nil, false, invalid
	}
	output = strings.TrimSpace(output)
	if output == "No battery level limits set" {
		return nil, false, nil
	}
	body, ok := strings.CutPrefix(output, "Battery level limits:")
	if !ok {
		return nil, false, invalid
	}
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
		return nil, false, invalid
	}
	body = strings.TrimSpace(body[1 : len(body)-1])
	if body == "" {
		return nil, false, nil
	}
	rows := registrationRow.FindAllString(body, -1)
	if len(rows) == 0 {
		return nil, false, invalid
	}
	for i, separator := range registrationRow.Split(body, -1) {
		want := ","
		if i == 0 || i == len(rows) {
			want = ""
		}
		if strings.TrimSpace(separator) != want {
			return nil, false, invalid
		}
	}
	for _, row := range rows {
		values := map[string]string{}
		for _, field := range strings.Split(row[1:len(row)-1], ";") {
			if strings.TrimSpace(field) == "" {
				continue
			}
			key, value, found := strings.Cut(field, "=")
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if !found || values[key] != "" || value == "" {
				return nil, false, invalid
			}
			values[key] = value
		}
		maxima := map[string]uint64{"Terminated": 1, "chargeSocLimitSoc": 100}
		for key, maxValue := range maxima {
			n, err := strconv.ParseUint(values[key], 10, 32)
			if err != nil || n > maxValue || (key == "chargeSocLimitSoc" && n == 0) {
				return nil, false, invalid
			}
		}
		reason := values["chargeSocLimitReason"]
		if strings.HasPrefix(reason, `"`) {
			var err error
			reason, err = strconv.Unquote(reason)
			if err != nil {
				return nil, false, invalid
			}
		}
		if reason == "" {
			return nil, false, invalid
		}
		if reason == "manualChargeLimit" && values["Terminated"] == "0" {
			manual = true
		}
		if reason == "battChargeLimit" {
			target, _ := strconv.Atoi(values["chargeSocLimitSoc"])
			if !validCeilingFlags(values) {
				// A matching target with unsafe flags still needs reconciliation.
				target = 0
			}
			targets = append(targets, target)
		}
	}
	return targets, manual, nil
}

func validCeilingFlags(values map[string]string) bool {
	return values["Terminated"] == "0" && values["chargeSocLimitDrain"] == "0" &&
		values["chargeSocLimitNoChargeToFull"] == "0" && values["chargeSocLimitIsEOC"] == "1"
}
