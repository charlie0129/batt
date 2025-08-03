package config

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"

	pkgerrors "github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/utils/ptr"
)

var (
	defaultFileConfig = &RawFileConfig{
		Limit:                   ptr.To(80),
		PreventIdleSleep:        ptr.To(true),
		DisableChargingPreSleep: ptr.To(true),
		PreventSystemSleep:      ptr.To(false),
		AllowNonRootAccess:      ptr.To(false),
		LowerLimitDelta:         ptr.To(2),
		// There are Macs without MagSafe LED. We only do checks when the user
		// explicitly enables this feature. In the future, we might add a check
		// that disables this feature if the Mac does not have a MagSafe LED.
		ControlMagSafeLED: ptr.To(false),
	}
)

var _ Config = &File{}

type File struct {
	c        *RawFileConfig
	mu       *sync.RWMutex
	filepath string
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

type RawFileConfig struct {
	Limit                   *int  `json:"limit,omitempty"`
	PreventIdleSleep        *bool `json:"preventIdleSleep,omitempty"`
	DisableChargingPreSleep *bool `json:"disableChargingPreSleep,omitempty"`
	PreventSystemSleep      *bool `json:"preventSystemSleep,omitempty"`
	AllowNonRootAccess      *bool `json:"allowNonRootAccess,omitempty"`
	LowerLimitDelta         *int  `json:"lowerLimitDelta,omitempty"`
	ControlMagSafeLED       *bool `json:"controlMagSafeLED,omitempty"`
}

func NewRawFileConfigFromConfig(c Config) (*RawFileConfig, error) {
	if c == nil {
		return nil, pkgerrors.New("config is nil")
	}

	rawConfig := &RawFileConfig{
		Limit:                   ptr.To(c.UpperLimit()),
		PreventIdleSleep:        ptr.To(c.PreventIdleSleep()),
		DisableChargingPreSleep: ptr.To(c.DisableChargingPreSleep()),
		PreventSystemSleep:      ptr.To(c.PreventSystemSleep()),
		AllowNonRootAccess:      ptr.To(c.AllowNonRootAccess()),
		LowerLimitDelta:         ptr.To(c.UpperLimit() - c.LowerLimit()),
		ControlMagSafeLED:       ptr.To(c.ControlMagSafeLED()),
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

func (f *File) ControlMagSafeLED() bool {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var controlMagSafeLED bool

	if f.c.ControlMagSafeLED != nil {
		controlMagSafeLED = *f.c.ControlMagSafeLED
	} else {
		controlMagSafeLED = *defaultFileConfig.ControlMagSafeLED
	}

	return controlMagSafeLED
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

func (f *File) SetControlMagSafeLED(b bool) {
	if f.c == nil {
		panic("config is nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.c.ControlMagSafeLED = &b
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
		return nil
	}

	conf := RawFileConfig{}
	err = json.Unmarshal(b, &conf)
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
	}
}
