// Package powerui controls the manual charge limit through PowerUIAgent.
// On macOS 27 its slider setter clamps sub-80 targets, so the root daemon
// writes the agent's exact target preference and asks it to reload instead.
// A saved preference is not proof that the battery has stopped charging.
package powerui

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Controller is the subset of PowerUI charge control used by the daemon.
type Controller interface {
	Supported() bool
	AvailableLimits() ([]int, error)
	// SupportsExactTargets reports whether exact percentage targets are available.
	SupportsExactTargets() bool
	// Limit checks the target, feature switch and (on macOS 27) powerd
	// registration, not physical charging current.
	// A zero target with enabled=true means the saved target, agent or
	// registration disagree; callers must reconcile rather than assume a disabled limiter.
	Limit() (limit int, enabled bool, err error)
	// SetLimit requests a target and confirms its registration; 100 releases
	// the native limit. A target the agent has loaded but powerd has not
	// registered yet returns ErrRegistrationPending and needs no retry. Any
	// other unconfirmed target returns an error and is retried on the next
	// call. Slider-only backends verify through their setter.
	SetLimit(limit int) error
	Disable() error
	// NoDrainLimit reads batt's saved no-drain ceiling. enabled describes
	// the saved policy, not physical charging current.
	NoDrainLimit() (limit int, enabled bool, err error)
	// SetNoDrainLimit disables PowerUIAgent's competing limit and saves a
	// no-drain ceiling. It may restart powerd. The daemon checks for other
	// applications' sleep assertions immediately before calling it.
	SetNoDrainLimit(limit int) error
	// DisableNoDrain removes batt's ceiling and may restart powerd.
	DisableNoDrain() error
}

// ErrNoDrainUnsupported reports that this PowerUI backend does not expose
// powerd's exact no-drain policy (before macOS 27, or without root).
var ErrNoDrainUnsupported = errors.New("native no-drain charging requires the root batt daemon on macOS 27")

// ErrRegistrationPending reports a target that PowerUIAgent has loaded but
// powerd does not enforce yet. While the battery charges, the agent registers
// a reloaded target only once the battery level changes or charging stops.
// After 5 minutes on battery power it withdraws its registration until the
// Mac is connected to power again. Another reload changes neither.
var ErrRegistrationPending = errors.New("macOS has accepted the target but not applied it yet")

// The client chooses the preference path or the slider API. Tests replace
// these operations without calling Apple services or changing preferences.
type policyOperations interface {
	Policy() (int, bool, error)
	SetPolicy(int) error
}

type operations interface {
	Supported() bool
	AvailableLimits() ([]int, error)
	Limit() (int, bool, error)
	Disable() error
	CustomLimits() bool
	Preferences() (int, bool, error)
	Enable() error
	WritePreferences(int) error
	SetSliderLimit(int) error
	ManualLimitRegistered() (bool, error)
	RegisteredManualLimits() ([]int, error)
}

// On wall power, unless the battery is charging, the agent registers a
// reloaded target within milliseconds. SetLimit polls for it this many times,
// this far apart.
const (
	registrationChecks        = 10
	registrationCheckInterval = 100 * time.Millisecond
)

// Client serializes access to the native charge-limit service.
type Client struct {
	mu  sync.Mutex
	api operations
	// sleep waits between registration checks. Tests replace it.
	sleep func(time.Duration)
}

func (c *Client) wait(d time.Duration) {
	if c.sleep != nil {
		c.sleep(d)
		return
	}
	time.Sleep(d)
}

// New creates a client without probing or changing the system.
func New() *Client { return &Client{api: nativeBridge{}} }

func (c *Client) Supported() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.api.Supported()
}

func (c *Client) SupportsExactTargets() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.api.CustomLimits()
}

func (c *Client) AvailableLimits() ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api.CustomLimits() {
		limits := make([]int, 91)
		for i := range limits {
			limits[i] = i + 10
		}
		return limits, nil
	}
	limits, err := c.api.AvailableLimits()
	sort.Ints(limits)
	return limits, err
}

func (c *Client) Limit() (int, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api.CustomLimits() {
		state, err := c.customState()
		return state.limit, state.enabled, err
	}
	return c.api.Limit()
}

// customState combines the saved preferences, the agent and powerd's
// registrations.
type customState struct {
	// limit is the confirmed target, zero while the three disagree.
	limit   int
	enabled bool
	// pending is a target the agent has loaded while powerd enforces an
	// earlier one or none.
	pending int
}

func (c *Client) customState() (customState, error) {
	limit, enabled, err := c.api.Preferences()
	if err != nil {
		return customState{}, err
	}
	// A saved preference does not prove the agent has loaded it. Its getter
	// reports exact sub-80 targets on macOS 27 (unlike its clamping setter).
	// Keep retrying if the agent still has a previous target in memory.
	agentLimit, agentEnabled, err := c.api.Limit()
	if err != nil {
		return customState{}, err
	}
	if agentEnabled && (!enabled || agentLimit != limit) {
		// Preserve enabled=true so a request to disable still releases the
		// existing registration while the agent has yet to load a new target.
		return customState{enabled: true}, nil
	}
	if enabled && agentEnabled {
		registered, err := c.api.RegisteredManualLimits()
		if err != nil {
			return customState{enabled: true}, err
		}
		confirmed := len(registered) > 0
		for _, target := range registered {
			confirmed = confirmed && target == limit
		}
		if !confirmed {
			return customState{enabled: true, pending: limit}, nil
		}
	}
	return customState{limit: limit, enabled: enabled && agentEnabled}, nil
}

func (c *Client) SetLimit(limit int) error {
	if limit < 10 || limit > 100 {
		return fmt.Errorf("invalid native charge limit %d: expected 10 through 100", limit)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit == 100 {
		return c.disable()
	}
	if !c.api.CustomLimits() {
		return c.api.SetSliderLimit(limit)
	}
	state, err := c.customState()
	if err != nil {
		return fmt.Errorf("read native target before changing it: %w", err)
	}
	if state.enabled && state.limit == limit {
		return nil
	}
	// Rewriting a target the agent has already loaded would not make it
	// register the target any sooner.
	if state.pending == limit {
		return pendingError(limit)
	}
	// customState reports no target while the agent has stale settings.
	// That needs another preference notification, not an enable call.
	_, agentEnabled, err := c.api.Limit()
	if err != nil {
		return fmt.Errorf("read native feature state before changing it: %w", err)
	}
	// Explicitly re-engage the agent after a previous disable. A preference
	// notification alone can leave it in its ChargingToFull state. enableMCL
	// selects Apple's 80% default until the reload below replaces it, so skip
	// it when the agent is already enabled.
	if !agentEnabled {
		if err := c.api.Enable(); err != nil {
			return fmt.Errorf("enable native charge control: %w", err)
		}
	}
	if err := c.api.WritePreferences(limit); err != nil {
		return fmt.Errorf("write native charge target: %w", err)
	}
	saved, savedEnabled, err := c.api.Preferences()
	if err != nil {
		return fmt.Errorf("verify native charge target: %w", err)
	}
	if !savedEnabled || saved != limit {
		return fmt.Errorf("native target did not retain %d%% (read %d%%, enabled=%t)", limit, saved, savedEnabled)
	}
	// Allow the reload notification to reach the agent before confirming
	// registration. This checks policy, not the battery's physical response.
	for attempt := range registrationChecks {
		if attempt > 0 {
			c.wait(registrationCheckInterval)
		}
		state, err = c.customState()
		if err != nil {
			return fmt.Errorf("verify native charge registration: %w", err)
		}
		if state.enabled && state.limit == limit {
			return nil
		}
	}
	if state.pending == limit {
		return pendingError(limit)
	}
	return fmt.Errorf("native charge target %d%% registration is not confirmed; batt will retry", limit)
}

func pendingError(limit int) error {
	return fmt.Errorf("native charge target %d%%: %w", limit, ErrRegistrationPending)
}

func (c *Client) disable() error {
	// A disabled preference may have left a registration behind. The agent
	// only removes it through disableMCL while it believes it is enabled.
	if c.api.CustomLimits() {
		_, enabled, err := c.api.Limit()
		if err != nil {
			return err
		}
		if !enabled {
			_, savedEnabled, err := c.api.Preferences()
			if err != nil {
				return err
			}
			registered, err := c.api.ManualLimitRegistered()
			if err != nil {
				return err
			}
			if !savedEnabled && !registered {
				return nil
			}
			if err := c.api.Enable(); err != nil {
				return err
			}
		}
	}
	if err := c.api.Disable(); err != nil {
		return err
	}
	if c.api.CustomLimits() {
		_, enabled, err := c.api.Limit()
		if err != nil {
			return err
		}
		_, savedEnabled, err := c.api.Preferences()
		if err != nil {
			return err
		}
		registered, err := c.api.ManualLimitRegistered()
		if err != nil {
			return err
		}
		if enabled || savedEnabled || registered {
			return errors.New("native charge limit release is not yet confirmed")
		}
	}
	return nil
}

func (c *Client) Disable() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disable()
}

// noDrainAPI returns the optional powerd policy operations. Tests can use a
// PowerUI fake without also simulating the macOS 27 policy store.
func (c *Client) noDrainAPI() (policyOperations, error) {
	api, ok := c.api.(policyOperations)
	if !ok || !c.api.CustomLimits() {
		return nil, ErrNoDrainUnsupported
	}
	return api, nil
}

func (c *Client) NoDrainLimit() (int, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	api, err := c.noDrainAPI()
	if err != nil {
		return 100, false, err
	}
	return api.Policy()
}

func (c *Client) SetNoDrainLimit(limit int) error {
	if limit < 10 || limit >= 100 {
		return fmt.Errorf("invalid no-drain charge limit %d: expected 10 through 99", limit)
	}
	c.mu.Lock()
	api, err := c.noDrainAPI()
	if err == nil {
		// The agent re-registers its draining limit on power-source changes.
		// Release it before installing powerd's independent ceiling.
		err = c.disable()
	}
	if err == nil {
		err = api.SetPolicy(limit)
	}
	c.mu.Unlock()
	if err == nil {
		return nil
	}
	// Do not leave the ordinary limit disabled after a failed policy write.
	// SetPolicy is idempotent, so cleanup does not restart powerd when no
	// batt policy was saved. If the write did land, remove it before restoring
	// the draining target.
	var cleanupErr error
	if api != nil {
		c.mu.Lock()
		cleanupErr = api.SetPolicy(100)
		c.mu.Unlock()
	}
	restoreErr := c.SetLimit(limit)
	return errors.Join(fmt.Errorf("apply no-drain charge limit: %w", err), cleanupErr, restoreErr)
}

func (c *Client) DisableNoDrain() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	api, err := c.noDrainAPI()
	if err != nil {
		return err
	}
	return api.SetPolicy(100)
}
