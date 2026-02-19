package main

import (
	"encoding/json"
	"math"
	"time"

	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
)

type statusJSON struct {
	Charging      statusChargingJSON     `json:"charging"`
	Battery       statusBatteryJSON      `json:"battery"`
	Configuration statusConfigJSON       `json:"configuration"`
	// Calibration is omitted when telemetry data is unavailable (e.g. API error).
	Calibration   *statusCalibrationJSON `json:"calibration,omitempty"`
}

type statusChargingJSON struct {
	AllowCharging bool `json:"allowCharging"`
	UseAdapter    bool `json:"useAdapter"`
	PluggedIn     bool `json:"pluggedIn"`
}

type statusBatteryJSON struct {
	CurrentChargePercent int     `json:"currentChargePercent"`
	State                string  `json:"state"`
	TimeToLimitMinutes   *int    `json:"timeToLimitMinutes"`
	FullCapacityMah      int     `json:"fullCapacityMah"`
	ChargeRateWatts      float64 `json:"chargeRateWatts"`
	VoltageVolts         float64 `json:"voltageVolts"`
}

type statusConfigJSON struct {
	Enabled                 bool                 `json:"enabled"`
	UpperLimitPercent       int                  `json:"upperLimitPercent"`
	LowerLimitPercent       int                  `json:"lowerLimitPercent"`
	PreventIdleSleep        bool                 `json:"preventIdleSleep"`
	DisableChargingPreSleep bool                 `json:"disableChargingPreSleep"`
	PreventSystemSleep      bool                 `json:"preventSystemSleep"`
	AllowNonRootAccess      bool                 `json:"allowNonRootAccess"`
	ControlMagSafeLed       statusMagSafeLedJSON `json:"controlMagSafeLed"`
}

type statusMagSafeLedJSON struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

type statusCalibrationJSON struct {
	Phase     string                     `json:"phase"`
	StartedAt *time.Time                 `json:"startedAt"`
	Paused    bool                       `json:"paused"`
	CanPause  bool                       `json:"canPause"`
	CanCancel bool                       `json:"canCancel"`
	Message   string                     `json:"message"`
	Schedule  statusCalibrationSchedJSON `json:"schedule"`
}

type statusCalibrationSchedJSON struct {
	Enabled     bool       `json:"enabled"`
	Cron        string     `json:"cron"`
	ScheduledAt *time.Time `json:"scheduledAt"`
}

// batteryStateString returns a camelCase string for the battery state.
func batteryStateString(state powerinfo.BatteryState, chargeRate int) string {
	switch state {
	case powerinfo.Charging:
		return "charging"
	case powerinfo.Discharging:
		if chargeRate != 0 {
			return "discharging"
		}
		return "notCharging"
	case powerinfo.Full:
		return "full"
	default:
		return "notCharging"
	}
}

func printStatusJSON(cmd *cobra.Command, data *statusData, cfg *config.File) error {
	mode := cfg.ControlMagSafeLED()
	upperLimit := cfg.UpperLimit()
	enabled := upperLimit < 100

	// When batt is disabled (limit=100%), lower limit equals upper limit
	// because the battery is always allowed to charge freely.
	lowerLimit := cfg.LowerLimit()
	if !enabled {
		lowerLimit = upperLimit
	}

	out := statusJSON{
		Charging: statusChargingJSON{
			AllowCharging: data.charging,
			UseAdapter:    data.adapter,
			PluggedIn:     data.pluggedIn,
		},
		Battery: statusBatteryJSON{
			CurrentChargePercent: data.currentCharge,
			State:                batteryStateString(data.batteryInfo.State, data.batteryInfo.ChargeRate),
			TimeToLimitMinutes:   computeTimeToLimit(data, cfg),
			FullCapacityMah:      data.batteryInfo.Design,
			ChargeRateWatts:      math.Round(float64(data.batteryInfo.ChargeRate)/1e3*10) / 10,
			VoltageVolts:         math.Round(data.batteryInfo.DesignVoltage*100) / 100,
		},
		Configuration: statusConfigJSON{
			Enabled:                 enabled,
			UpperLimitPercent:       upperLimit,
			LowerLimitPercent:       lowerLimit,
			PreventIdleSleep:        cfg.PreventIdleSleep(),
			DisableChargingPreSleep: cfg.DisableChargingPreSleep(),
			PreventSystemSleep:      cfg.PreventSystemSleep(),
			AllowNonRootAccess:      cfg.AllowNonRootAccess(),
			ControlMagSafeLed: statusMagSafeLedJSON{
				Enabled: mode != config.ControlMagSafeModeDisabled,
				Mode:    string(mode),
			},
		},
	}

	tr, err := apiClient.GetTelemetry(false, true)
	if err == nil && tr.Calibration != nil {
		cal := tr.Calibration

		var startedAt *time.Time
		if cal.Phase != calibration.PhaseIdle && !cal.StartedAt.IsZero() {
			startedAt = &cal.StartedAt
		}

		cron := cfg.Cron()
		sched := statusCalibrationSchedJSON{
			Enabled: cron != "",
			Cron:    cron,
		}
		if cron != "" && !cal.ScheduledAt.IsZero() {
			sched.ScheduledAt = &cal.ScheduledAt
		}

		out.Calibration = &statusCalibrationJSON{
			Phase:     string(cal.Phase),
			StartedAt: startedAt,
			Paused:    cal.Paused,
			CanPause:  cal.CanPause,
			CanCancel: cal.CanCancel,
			Message:   cal.Message,
			Schedule:  sched,
		}
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
