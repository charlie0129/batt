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

// Read powerd's registrations independently of PowerUI's feature flag. Do not
// treat a failed command or an unfamiliar output format as an empty policy list.
func (nativeBridge) ManualLimitRegistered() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/pmset", "-g", "battlimit")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("read native charge registrations: %w", err)
	}
	return manualLimitRegistered(string(output))
}

var registrationRow = regexp.MustCompile(`\{[^{}]*\}`)

func manualLimitRegistered(output string) (bool, error) {
	invalid := fmt.Errorf("unrecognized pmset battery-limit output; release cannot be confirmed")
	if len(output) > 64*1024 {
		return false, invalid
	}
	output = strings.TrimSpace(output)
	if output == "No battery level limits set" {
		return false, nil
	}
	body, ok := strings.CutPrefix(output, "Battery level limits:")
	if !ok {
		return false, invalid
	}
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
		return false, invalid
	}
	body = strings.TrimSpace(body[1 : len(body)-1])
	if body == "" {
		return false, nil
	}
	rows := registrationRow.FindAllString(body, -1)
	if len(rows) == 0 {
		return false, invalid
	}
	for i, separator := range registrationRow.Split(body, -1) {
		want := ","
		if i == 0 || i == len(rows) {
			want = ""
		}
		if strings.TrimSpace(separator) != want {
			return false, invalid
		}
	}
	manual := false
	for _, row := range rows {
		values := map[string]string{}
		for _, field := range strings.Split(row[1:len(row)-1], ";") {
			if strings.TrimSpace(field) == "" {
				continue
			}
			key, value, found := strings.Cut(field, "=")
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if !found || values[key] != "" || value == "" {
				return false, invalid
			}
			values[key] = value
		}
		maxima := map[string]uint64{"Terminated": 1, "chargeSocLimitDrain": 1,
			"chargeSocLimitIsEOC": 1, "chargeSocLimitNoChargeToFull": 1,
			"chargeSocLimitOwner": 1<<31 - 1, "chargeSocLimitSoc": 100}
		if len(values) != len(maxima)+1 {
			return false, invalid
		}
		for key, maxValue := range maxima {
			n, err := strconv.ParseUint(values[key], 10, 32)
			if err != nil || n > maxValue || (key == "chargeSocLimitSoc" && n == 0) {
				return false, invalid
			}
		}
		reason := values["chargeSocLimitReason"]
		if strings.HasPrefix(reason, `"`) {
			var err error
			reason, err = strconv.Unquote(reason)
			if err != nil {
				return false, invalid
			}
		}
		if reason == "" {
			return false, invalid
		}
		manual = manual || reason == "manualChargeLimit"
	}
	return manual, nil
}
