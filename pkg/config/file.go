package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	pkgerrors "github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/temperature"
	"github.com/charlie0129/batt/pkg/utils/ptr"
)

const (
	ctrlMagSafeModeEnabledStr   = "enabled"
	ctrlMagSafeModeDisabledStr  = "disabled"
	ctrlMagSafeModeAlwaysOffStr = "always-off"
	trayIconStyleFixedStr       = "fixed"
	trayIconStyleBatteryStr     = "battery"
	trayIconStylePercentageStr  = "percentage"
)

type ControlMagSafeMode string
type TrayIconStyle string

const (
	ControlMagSafeModeEnabled   ControlMagSafeMode = ctrlMagSafeModeEnabledStr
	ControlMagSafeModeDisabled  ControlMagSafeMode = ctrlMagSafeModeDisabledStr
	ControlMagSafeModeAlwaysOff ControlMagSafeMode = ctrlMagSafeModeAlwaysOffStr

	TrayIconStyleFixed      TrayIconStyle = trayIconStyleFixedStr
	TrayIconStyleBattery    TrayIconStyle = trayIconStyleBatteryStr
	TrayIconStylePercentage TrayIconStyle = trayIconStylePercentageStr
)

var (
	defaultFileConfig = &RawFileConfig{
		Limit:                   ptr.To(80),
		PreventIdleSleep:        ptr.To(true),
		DisableChargingPreSleep: ptr.To(true),
		PreventSystemSleep:      ptr.To(false),
		AllowNonRootAccess:      ptr.To(false),
		LowerLimitDelta:         ptr.To(2),

		CalibrationDischargeThreshold:          ptr.To(15),
		CalibrationHoldDurationMinutes:         ptr.To(120),
		TemperatureMonitoringEnabled:           ptr.To(false),
		TemperatureProtectionThresholdCelsius:  ptr.To(40),
		TrayIconStyle:                          ptr.To(TrayIconStylePercentage),

		// There are Macs without MagSafe LED. We only do checks when the user
		// explicitly enables this feature. In the future, we might add a check
		// that disables this feature if the Mac does not have a MagSafe LED.
		ControlMagSafeLED: ptr.To(ControlMagSafeModeDisabled),
	}
)

var _ Config = &File{}

type File struct {
	c                     *RawFileConfig
	temperatureReferences temperature.References
	mu                    *sync.RWMutex
	filepath              string
}

func NewFile(configPath string) (*File, error) {
	f := &File{
		filepath: configPath,
		mu:       &sync.RWMutex{},
	}
	err := f.Load()
	if err != nil {
		return nil, err
	}

	return f, nil
}

func NewFileFromConfig(c *RawFileConfig, configPath string) *File {
	if c == nil {
		c = defaultFileConfig
	}

	f := &File{
		c:        c,
		mu:       &sync.RWMutex{},
		filepath: configPath,
	}

	return f
}

func (c *ControlMagSafeMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch s {
		case ctrlMagSafeModeEnabledStr:
			*c = ControlMagSafeModeEnabled
		case ctrlMagSafeModeDisabledStr:
			*c = ControlMagSafeModeDisabled
		case ctrlMagSafeModeAlwaysOffStr:
			*c = ControlMagSafeModeAlwaysOff
		default:
			logrus.Warnf("invalid ControlMagSafeMode %q, falling back to %q", s, ControlMagSafeModeDisabled)
			*c = ControlMagSafeModeDisabled
		}
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			*c = ControlMagSafeModeEnabled
		} else {
			*c = ControlMagSafeModeDisabled
		}
		return nil
	}
	return nil
}

func ParseTrayIconStyle(s string) (TrayIconStyle, bool) {
	switch s {
	case trayIconStyleFixedStr:
		return TrayIconStyleFixed, true
	case trayIconStyleBatteryStr:
		return TrayIconStyleBattery, true
	case trayIconStylePercentageStr:
		return TrayIconStylePercentage, true
	default:
		return TrayIconStylePercentage, false
	}
}

func (s TrayIconStyle) IsValid() bool {
	_, ok := ParseTrayIconStyle(string(s))
	return ok
}

func (s *TrayIconStyle) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	style, ok := ParseTrayIconStyle(raw)
	if !ok {
		logrus.Warnf("invalid TrayIconStyle %q, falling back to %q", raw, TrayIconStylePercentage)
	}
	*s = style
	return nil
}

type RawFileConfig struct {
	Limit                   *int                `json:"limit,omitempty"`
	PreventIdleSleep        *bool               `json:"preventIdleSleep,omitempty"`
	DisableChargingPreSleep *bool               `json:"disableChargingPreSleep,omitempty"`
	PreventSystemSleep      *bool               `json:"preventSystemSleep,omitempty"`
	AllowNonRootAccess      *bool               `json:"allowNonRootAccess,omitempty"`
	LowerLimitDelta         *int                `json:"lowerLimitDelta,omitempty"`
	ControlMagSafeLED       *ControlMagSafeMode `json:"controlMagSafeLED,omitempty"`

	CalibrationDischargeThreshold         *int           `json:"calibrationDischargeThreshold,omitempty"`
	CalibrationHoldDurationMinutes        *int           `json:"calibrationHoldDurationMinutes,omitempty"`
	TemperatureMonitoringEnabled          *bool          `json:"temperatureMonitoringEnabled,omitempty"`
	TemperatureProtectionThresholdCelsius *int           `json:"temperatureProtectionThresholdCelsius,omitempty"`
	TrayIconStyle                         *TrayIconStyle `json:"trayIconStyle,omitempty"`
	Cron                                  *string        `json:"cron,omitempty"`
}

func NewRawFileConfigFromConfig(c Config) (*RawFileConfig, error) {
	if c == nil {
		return nil, pkgerrors.New("config is nil")
	}

	rawConfig := &RawFileConfig{
		Limit:                                  ptr.To(c.UpperLimit()),
		PreventIdleSleep:                       ptr.To(c.PreventIdleSleep()),
		DisableChargingPreSleep:                ptr.To(c.DisableChargingPreSleep()),
		PreventSystemSleep:                     ptr.To(c.PreventSystemSleep()),
		AllowNonRootAccess:                     ptr.To(c.AllowNonRootAccess()),
		LowerLimitDelta:                        ptr.To(c.UpperLimit() - c.LowerLimit()),
		ControlMagSafeLED:                      ptr.To(c.ControlMagSafeLED()),
		CalibrationDischargeThreshold:          ptr.To(c.CalibrationDischargeThreshold()),
		CalibrationHoldDurationMinutes:         ptr.To(c.CalibrationHoldDurationMinutes()),
		TemperatureMonitoringEnabled:           ptr.To(c.TemperatureMonitoringEnabled()),
		TemperatureProtectionThresholdCelsius:  ptr.To(c.TemperatureProtectionThresholdCelsius()),
		TrayIconStyle:                          ptr.To(c.TrayIconStyle()),
		Cron:                                   ptr.To(c.Cron()),
	}

	return rawConfig, nil
}

func (f *File) UpperLimit() int {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var limit int

	if f.c.Limit != nil {
		limit = *f.c.Limit
	} else {
		limit = *defaultFileConfig.Limit
	}

	return limit
}

func (f *File) LowerLimit() int {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var delta int

	if f.c.LowerLimitDelta != nil {
		delta = *f.c.LowerLimitDelta
	} else {
		delta = *defaultFileConfig.LowerLimitDelta
	}

	return f.UpperLimit() - delta
}

func (f *File) PreventIdleSleep() bool {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var preventIdleSleep bool

	if f.c.PreventIdleSleep != nil {
		preventIdleSleep = *f.c.PreventIdleSleep
	} else {
		preventIdleSleep = *defaultFileConfig.PreventIdleSleep
	}

	return preventIdleSleep
}

func (f *File) DisableChargingPreSleep() bool {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var disableChargingPreSleep bool

	if f.c.DisableChargingPreSleep != nil {
		disableChargingPreSleep = *f.c.DisableChargingPreSleep
	} else {
		disableChargingPreSleep = *defaultFileConfig.DisableChargingPreSleep
	}

	return disableChargingPreSleep
}

func (f *File) PreventSystemSleep() bool {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var preventSystemSleep bool

	if f.c.PreventSystemSleep != nil {
		preventSystemSleep = *f.c.PreventSystemSleep
	} else {
		preventSystemSleep = *defaultFileConfig.PreventSystemSleep
	}

	return preventSystemSleep
}

func (f *File) AllowNonRootAccess() bool {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var allowNonRootAccess bool

	if f.c.AllowNonRootAccess != nil {
		allowNonRootAccess = *f.c.AllowNonRootAccess
	} else {
		allowNonRootAccess = *defaultFileConfig.AllowNonRootAccess
	}

	return allowNonRootAccess
}

func (f *File) ControlMagSafeLED() ControlMagSafeMode {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var ControlMagSafeLED ControlMagSafeMode

	if f.c.ControlMagSafeLED != nil {
		ControlMagSafeLED = *f.c.ControlMagSafeLED
	} else {
		ControlMagSafeLED = *defaultFileConfig.ControlMagSafeLED
	}

	return ControlMagSafeLED
}

// CalibrationDischargeThreshold returns the discharge threshold percentage (< this value ends discharge phase).
// Default 15 if not set or invalid (<10 or > 50). We clamp to [10,50] to avoid pathological values.
func (f *File) CalibrationDischargeThreshold() int {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.c.CalibrationDischargeThreshold == nil {
		return 15
	}
	val := *f.c.CalibrationDischargeThreshold
	if val < 10 {
		return 10
	}
	if val > 50 { // upper sanity bound; calibration below 50% is reasonable
		return 50
	}
	return val
}

// CalibrationHoldDurationMinutes returns duration minutes to hold at full charge.
// Default 120 if not set or invalid (< 0 or > 1440).
func (f *File) CalibrationHoldDurationMinutes() int {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.c.CalibrationHoldDurationMinutes == nil {
		return 120
	}
	val := *f.c.CalibrationHoldDurationMinutes
	if val < 0 || val > 24*60 { // cap at 24h
		return 120
	}
	return val
}

func (f *File) TemperatureMonitoringEnabled() bool {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.c.TemperatureMonitoringEnabled == nil {
		return *defaultFileConfig.TemperatureMonitoringEnabled
	}
	return *f.c.TemperatureMonitoringEnabled
}

func (f *File) TemperatureProtectionThresholdCelsius() int {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.c.TemperatureProtectionThresholdCelsius == nil {
		return *defaultFileConfig.TemperatureProtectionThresholdCelsius
	}
	val := *f.c.TemperatureProtectionThresholdCelsius
	if val < 30 {
		return 30
	}
	if val > 55 {
		return 55
	}
	return val
}

func (f *File) TrayIconStyle() TrayIconStyle {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.c.TrayIconStyle == nil || !f.c.TrayIconStyle.IsValid() {
		return *defaultFileConfig.TrayIconStyle
	}
	return *f.c.TrayIconStyle
}

func (f *File) TemperatureReferences() temperature.References {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.temperatureReferences
}

func (f *File) SetUpperLimit(i int) {
	if f.c == nil {
		panic("config is nil")
	}

	delta := f.UpperLimit() - f.LowerLimit()
	if i > 100 || i-delta < 0 {
		panic("upper limit must be between 0 and 100 and greater than lower limit")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.c.Limit = &i
}

func (f *File) SetLowerLimit(i int) {
	if f.c == nil {
		panic("config is nil")
	}

	if i < 0 || i >= f.UpperLimit() {
		panic("lower limit must be between 0 and upper limit")
	}

	delta := f.UpperLimit() - i

	f.mu.Lock()
	defer f.mu.Unlock()
	f.c.LowerLimitDelta = &delta
}

func (f *File) SetPreventIdleSleep(b bool) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.c.PreventIdleSleep = &b
}

func (f *File) SetDisableChargingPreSleep(b bool) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.c.DisableChargingPreSleep = &b
}

func (f *File) SetPreventSystemSleep(b bool) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.c.PreventSystemSleep = &b
}

func (f *File) SetAllowNonRootAccess(b bool) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.AllowNonRootAccess = &b
}

func (f *File) SetControlMagSafeLED(mode ControlMagSafeMode) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.ControlMagSafeLED = ptr.To(mode)
}

func (f *File) Cron() string {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var cron string

	if f.c.Cron != nil {
		cron = *f.c.Cron
	}

	return cron
}

func (f *File) SetCron(cron string) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.Cron = ptr.To(cron)
}

func (f *File) SetCalibrationDischargeThreshold(i int) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.CalibrationDischargeThreshold = &i
}

func (f *File) SetCalibrationHoldDurationMinutes(i int) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.CalibrationHoldDurationMinutes = &i
}

func (f *File) SetTemperatureMonitoringEnabled(b bool) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.TemperatureMonitoringEnabled = &b
}

func (f *File) SetTemperatureProtectionThresholdCelsius(i int) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.TemperatureProtectionThresholdCelsius = &i
}

func (f *File) SetTrayIconStyle(style TrayIconStyle) {
	if f.c == nil {
		panic("config is nil")
	}

	if !style.IsValid() {
		style = TrayIconStylePercentage
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.TrayIconStyle = ptr.To(style)
}

func (f *File) SetTemperatureReference(s temperature.Scenario, value float64) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.temperatureReferences.Set(s, value)
}

func parseTemperatureReferences(configString string) temperature.References {
	refs := temperature.References{}
	scanner := bufio.NewScanner(strings.NewReader(configString))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		for _, scenario := range []temperature.Scenario{
			temperature.ScenarioIdleNotCharging,
			temperature.ScenarioIdleCharging,
			temperature.ScenarioActiveCharging,
		} {
			label := temperature.Label(scenario) + ":"
			if !strings.HasPrefix(line, label) {
				continue
			}

			valueString := strings.TrimSpace(strings.TrimPrefix(line, label))
			valueString = strings.TrimSuffix(valueString, "C")
			valueString = strings.TrimSuffix(valueString, "°")
			valueString = strings.TrimSpace(valueString)
			if valueString == "" || strings.EqualFold(valueString, "N/A") {
				continue
			}
			value, err := strconv.ParseFloat(valueString, 64)
			if err != nil || value <= 0 {
				continue
			}
			refs.Set(scenario, value)
		}
	}
	return refs
}

func stripConfigCommentLines(configString string) string {
	var builder strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(configString))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func writeTemperatureReferenceComments(w io.Writer, refs temperature.References) error {
	if _, err := fmt.Fprintln(w, "# Temperature references (auto-generated)"); err != nil {
		return err
	}

	for _, scenario := range []temperature.Scenario{
		temperature.ScenarioIdleNotCharging,
		temperature.ScenarioIdleCharging,
		temperature.ScenarioActiveCharging,
	} {
		value := refs.Value(scenario)
		if value == nil {
			if _, err := fmt.Fprintf(w, "# %s: N/A\n", temperature.Label(scenario)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "# %s: %.1f°C\n", temperature.Label(scenario), *value); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(w)
	return err
}

func (f *File) Load() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fp, err := os.Open(f.filepath)
	if err != nil {
		if os.IsNotExist(err) {
			// If the file does not exist, return the empty config.
			// Do not make f.c a nil.
			f.c = &RawFileConfig{}
			f.temperatureReferences = temperature.References{}
			return nil
		}
		return pkgerrors.Wrapf(err, "failed to open file %s", f.filepath)
	}
	defer func(fp *os.File) {
		err := fp.Close()
		if err != nil {
			logrus.Warnf("failed to close file %s", f.filepath)
		}
	}(fp)

	// Since we want to tell if the file is empty, using json.Decoder will
	// not work.
	b, err := io.ReadAll(fp)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to read file %s", f.filepath)
	}
	configString := string(b)

	if strings.TrimSpace(configString) == "" {
		// If the file is empty, return the empty config.
		// Do not make f.c a nil.
		f.c = &RawFileConfig{}
		f.temperatureReferences = temperature.References{}
		return nil
	}

	f.temperatureReferences = parseTemperatureReferences(configString)
	configString = stripConfigCommentLines(configString)
	if strings.TrimSpace(configString) == "" {
		f.c = &RawFileConfig{}
		return nil
	}

	conf := RawFileConfig{}
	err = json.Unmarshal([]byte(configString), &conf)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to unmarshal config from file %s", f.filepath)
	}
	f.c = &conf

	return nil
}

func (f *File) Save() error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.c == nil {
		return pkgerrors.New("config is nil")
	}

	shouldWriteTemperatureReferences := false
	if f.c.TemperatureMonitoringEnabled != nil && *f.c.TemperatureMonitoringEnabled {
		shouldWriteTemperatureReferences = true
	}
	if !f.temperatureReferences.Empty() {
		shouldWriteTemperatureReferences = true
	}

	fp, err := os.OpenFile(f.filepath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to open file %s", f.filepath)
	}
	defer func(fp *os.File) {
		err := fp.Close()
		if err != nil {
			logrus.Warnf("failed to close file %s", f.filepath)
		}
	}(fp)

	if shouldWriteTemperatureReferences {
		if err := writeTemperatureReferenceComments(fp, f.temperatureReferences); err != nil {
			return pkgerrors.Wrapf(err, "failed to write temperature references to file %s", f.filepath)
		}
	}

	enc := json.NewEncoder(fp)
	enc.SetIndent("", "  ")
	err = enc.Encode(f.c)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to encode config to file %s", f.filepath)
	}

	return nil
}

func (f *File) LogrusFields() logrus.Fields {
	if f.c == nil {
		panic("config is nil")
	}

	return logrus.Fields{
		"upperLimit":              f.UpperLimit(),
		"lowerLimit":              f.LowerLimit(),
		"preventIdleSleep":        f.PreventIdleSleep(),
		"disableChargingPreSleep": f.DisableChargingPreSleep(),
		"preventSystemSleep":      f.PreventSystemSleep(),
		"allowNonRootAccess":      f.AllowNonRootAccess(),
		"controlMagsafeLed":       f.ControlMagSafeLED(),
		"temperatureMonitoring":   f.TemperatureMonitoringEnabled(),
		"temperatureThreshold":    f.TemperatureProtectionThresholdCelsius(),
		"trayIconStyle":           f.TrayIconStyle(),
	}
}
