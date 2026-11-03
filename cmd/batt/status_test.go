package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/utils/ptr"
)

// statusConfig builds the client-side config view the status command works on.
// A chargeOnceTarget of 0 means no one-time charge is running.
func statusConfig(limit, lowerLimitDelta, chargeOnceTarget int) *config.File {
	raw := &config.RawFileConfig{
		Limit:           ptr.To(limit),
		LowerLimitDelta: ptr.To(lowerLimitDelta),
	}
	if chargeOnceTarget > 0 {
		raw.ChargeOnceTarget = ptr.To(chargeOnceTarget)
	}
	return config.NewFileFromConfig(raw, "")
}

// chargingBattery charges at 1000 mA with 5000 mAh of capacity, so one percent
// takes three minutes.
func chargingBattery() *powerinfo.Battery {
	return &powerinfo.Battery{
		State:          powerinfo.Charging,
		DesignCapacity: 5000,
		MaxCapacity:    5000,
		ChargeRate:     10000,
		DesignVoltage:  10,
	}
}

func TestComputeTimeToLimitFollowsTheOneTimeCharge(t *testing.T) {
	tests := []struct {
		name             string
		limit            int
		chargeOnceTarget int
		currentCharge    int
		want             *int
	}{
		{name: "towards the configured limit", limit: 80, currentCharge: 70, want: ptr.To(30)},
		{
			// Without this, the estimate named the 80% limit while the battery
			// was on its way to 100%.
			name: "towards a one-time charge to full", limit: 80, chargeOnceTarget: 100, currentCharge: 70, want: ptr.To(90),
		},
		{
			// A one-time charge to full runs past the configured limit, where
			// the limit-based estimate used to stop reporting anything.
			name: "past the configured limit", limit: 80, chargeOnceTarget: 100, currentCharge: 90, want: ptr.To(30),
		},
		{name: "one-time charge to the limit", limit: 80, chargeOnceTarget: 80, currentCharge: 70, want: ptr.To(30)},
		{name: "target already reached", limit: 80, chargeOnceTarget: 80, currentCharge: 80},
		{name: "batt is not limiting charging", limit: 100, currentCharge: 70},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &statusData{currentCharge: tt.currentCharge, batteryInfo: chargingBattery()}
			got := computeTimeToLimit(data, statusConfig(tt.limit, 2, tt.chargeOnceTarget))

			// The estimate truncates a percentage converted through floating
			// point, so allow the minute it can lose. The test is about which
			// target the estimate aims at.
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("computeTimeToLimit() = %d minutes, want none", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("computeTimeToLimit() = none, want %d minutes", *tt.want)
			case tt.want != nil && (*got < *tt.want-1 || *got > *tt.want):
				t.Fatalf("computeTimeToLimit() = %d minutes, want %d", *got, *tt.want)
			}
		})
	}
}

func TestChargeTarget(t *testing.T) {
	tests := []struct {
		name             string
		limit            int
		chargeOnceTarget int
		want             int
		wantOK           bool
	}{
		{name: "the configured limit", limit: 80, want: 80, wantOK: true},
		{name: "a one-time charge overrides it", limit: 80, chargeOnceTarget: 100, want: 100, wantOK: true},
		{name: "nothing to steer towards", limit: 100, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := chargeTarget(statusConfig(tt.limit, 2, tt.chargeOnceTarget))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("chargeTarget() = %d, %v, want %d, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestStatusFullChargeOverride(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "batt-status-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	var battery *powerinfo.Battery
	var currentCharge int
	var cfg *config.File
	var caps compatibility.Capabilities
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("status attempted to change state: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var response interface{}
		switch r.URL.Path {
		case "/compatibility":
			response = caps
		case "/plugged-in":
			response = true
		case "/current-charge":
			response = currentCharge
		case "/battery-info":
			response = battery
		case "/config":
			response, _ = config.NewRawFileConfigFromConfig(cfg)
		case "/telemetry":
			response = struct{}{}
		default:
			t.Errorf("unexpected status request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		payload, err := json.Marshal(response)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := w.Write(payload); err != nil {
			t.Error(err)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	previousClient := apiClient
	apiClient = client.NewClient(socket)
	t.Cleanup(func() { apiClient = previousClient })

	for _, tt := range []struct {
		name    string
		flag    *bool
		full    bool
		charge  int
		exact   bool
		limit   int
		notice  bool
		waiting bool
	}{
		{name: "native slider override", flag: ptr.To(true), limit: 80, notice: true},
		{name: "native exact override", flag: ptr.To(true), exact: true, limit: 80, notice: true},
		{name: "full signal before rounded percentage reaches 100", flag: ptr.To(true), full: true, charge: 99, limit: 80, notice: true, waiting: true},
		{name: "100 percent before full signal", flag: ptr.To(true), charge: 100, limit: 80, notice: true, waiting: true},
		{name: "cleared override", flag: ptr.To(false), limit: 80},
		{name: "older daemon or unavailable flag", limit: 80},
		{name: "no limit to suspend", flag: ptr.To(true), limit: 100},
	} {
		t.Run(tt.name, func(t *testing.T) {
			battery = chargingBattery()
			currentCharge = 70
			if tt.charge > 0 {
				currentCharge = tt.charge
			}
			battery.ChargingToFullOverride = tt.flag
			if tt.full {
				battery.State = powerinfo.Full
			}
			cfg = statusConfig(tt.limit, 2, 0)
			caps = compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlNative, NativeExactTargets: tt.exact}
			cmd := NewStatusCommand()
			var output bytes.Buffer
			cmd.SetOut(&output)
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			for _, notice := range []struct {
				text string
				want bool
			}{
				{powerinfo.FullChargeOverrideNotice, tt.notice && !tt.waiting},
				{powerinfo.FullChargeOverrideAtFullNotice, tt.notice && tt.waiting},
			} {
				if got := strings.Contains(output.String(), notice.text); got != notice.want {
					t.Fatalf("notice %q = %t, want %t:\n%s", notice.text, got, notice.want, &output)
				}
			}
			if tt.notice && strings.Contains(output.String(), "Time to limit") {
				t.Fatalf("suspended limit has a time estimate:\n%s", &output)
			}
			if !tt.notice && tt.limit < 100 && currentCharge < tt.limit && !strings.Contains(output.String(), "Time to limit") {
				t.Fatalf("configured limit lost its time estimate:\n%s", &output)
			}

			output.Reset()
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Charging map[string]json.RawMessage `json:"charging"`
			}
			if err := json.Unmarshal(output.Bytes(), &parsed); err != nil {
				t.Fatal(err)
			}
			flag, exists := parsed.Charging["chargingToFullOverride"]
			if exists != (tt.flag != nil) || (exists && string(flag) != strconv.FormatBool(*tt.flag)) {
				t.Fatalf("JSON override = %s, present = %t; want %v", flag, exists, tt.flag)
			}
		})
	}
}

func TestChargingNarrationDuringAOneTimeCharge(t *testing.T) {
	// 79% inside a 78-80% band: the charge sits above the lower limit, which is
	// exactly what the persistent explanation talks about. A one-time charge
	// ignores that band, so repeating the explanation would contradict it.
	data := &statusData{currentCharge: 79, pluggedIn: true, adapter: true, batteryInfo: chargingBattery()}

	persistent := chargingNarration(data, statusConfig(80, 2, 0))
	if !strings.Contains(persistent, "drops below the lower limit") {
		t.Fatalf("persistent narration lost its explanation: %q", persistent)
	}

	oneTime := chargingNarration(data, statusConfig(80, 2, 100))
	if strings.Contains(oneTime, "lower limit") {
		t.Fatalf("one-time narration still explains the persistent band: %q", oneTime)
	}
	if !strings.Contains(oneTime, "100%") {
		t.Fatalf("one-time narration does not name the target: %q", oneTime)
	}
}

func TestChargingNarration(t *testing.T) {
	tests := []struct {
		name             string
		charging         bool
		pluggedIn        bool
		adapter          bool
		currentCharge    int
		limit            int
		chargeOnceTarget int
		adapterControl   bool
		want             string
	}{
		{
			name: "charging while plugged in", charging: true, pluggedIn: true, adapter: true, currentCharge: 70, limit: 80,
			want: "Your Mac will charge.",
		},
		{
			name: "charging without a plug", charging: true, currentCharge: 70, limit: 80,
			want: "Your Mac will charge, but you are not plugged in yet.",
		},
		{
			name: "above the limit", pluggedIn: true, adapter: true, currentCharge: 85, limit: 80,
			want: "Your Mac will not charge, because your current charge is above the limit.",
		},
		{
			name: "inside the hysteresis gap", pluggedIn: true, adapter: true, currentCharge: 79, limit: 80,
			want: "Your Mac will not charge, because your current charge is above the lower limit. Charging will be allowed after current charge drops below the lower limit.",
		},
		{
			name: "below the lower limit with the adapter off", pluggedIn: true, currentCharge: 50, limit: 80, adapterControl: true,
			want: "Your Mac will not charge, because adapter is disabled.",
		},
		{
			name: "below the lower limit without adapter control", pluggedIn: true, currentCharge: 50, limit: 80,
			want: "Your Mac will not charge",
		},
		{
			name: "batt is not limiting charging", pluggedIn: true, adapter: true, currentCharge: 50, limit: 100,
			want: "",
		},
		{
			name: "a one-time charge has not started yet", pluggedIn: true, adapter: true, currentCharge: 79, limit: 80, chargeOnceTarget: 100,
			want: "Your Mac will charge to 100% once, starting with the next refresh.",
		},
		{
			name: "a one-time charge against a disabled adapter", pluggedIn: true, currentCharge: 50, limit: 80, chargeOnceTarget: 100, adapterControl: true,
			want: "Your Mac will not charge to its 100% one-time target, because adapter is disabled.",
		},
		{
			name: "a one-time charge without adapter control", pluggedIn: true, currentCharge: 50, limit: 80, chargeOnceTarget: 100,
			want: "Your Mac will charge to 100% once, starting with the next refresh.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &statusData{
				charging:      tt.charging,
				pluggedIn:     tt.pluggedIn,
				adapter:       tt.adapter,
				currentCharge: tt.currentCharge,
				batteryInfo:   chargingBattery(),
				capabilities:  compatibility.Capabilities{AdapterControl: tt.adapterControl},
			}
			if got := chargingNarration(data, statusConfig(tt.limit, 2, tt.chargeOnceTarget)); got != tt.want {
				t.Fatalf("chargingNarration() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintStatusJSON_PreventSleepOnAdapterDisable(t *testing.T) {
	tests := []struct {
		name                 string
		adapterControl       bool
		mode                 compatibility.ChargeControlMode
		preventSleep         bool
		wantPreventSleepJSON bool
		wantVal              bool
	}{
		{
			name:                 "supported and enabled",
			adapterControl:       true,
			preventSleep:         true,
			wantPreventSleepJSON: true,
			wantVal:              true,
		},
		{
			name:                 "supported and disabled",
			adapterControl:       true,
			preventSleep:         false,
			wantPreventSleepJSON: true,
			wantVal:              false,
		},
		{
			name:                 "adapter mode exposes sleep protection",
			mode:                 compatibility.ChargeControlAdapter,
			preventSleep:         true,
			wantPreventSleepJSON: true,
			wantVal:              true,
		},
		{
			name:                 "unsupported adapter control omits field",
			adapterControl:       false,
			preventSleep:         false,
			wantPreventSleepJSON: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			cmd := &cobra.Command{}
			cmd.SetOut(buf)

			limit := 80
			rawCfg := &config.RawFileConfig{
				Limit:                        &limit,
				PreventSleepOnAdapterDisable: &tt.preventSleep,
			}
			cfg := config.NewFileFromConfig(rawCfg, "")

			data := &statusData{
				capabilities: compatibility.Capabilities{
					ChargingControl:   true,
					AdapterControl:    tt.adapterControl,
					ChargeControlMode: tt.mode,
				},
				batteryInfo: &powerinfo.Battery{
					State: powerinfo.Charging,
				},
			}

			if err := printStatusJSON(cmd, data, cfg); err != nil {
				t.Fatalf("printStatusJSON failed: %v", err)
			}

			var parsed map[string]interface{}
			if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
				t.Fatalf("json unmarshal failed: %v", err)
			}

			configuration, ok := parsed["configuration"].(map[string]interface{})
			if !ok {
				t.Fatalf("configuration missing or not map: %v", parsed)
			}

			val, exists := configuration["preventSleepOnAdapterDisable"]
			if tt.wantPreventSleepJSON {
				if !exists {
					t.Fatalf("expected preventSleepOnAdapterDisable in configuration: %v", configuration)
				}
				if val != tt.wantVal {
					t.Fatalf("preventSleepOnAdapterDisable = %v, want %v", val, tt.wantVal)
				}
			} else if exists {
				t.Fatalf("expected preventSleepOnAdapterDisable to be omitted when unsupported, got: %v", val)
			}
		})
	}
}
