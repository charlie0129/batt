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
	// the native limit. An unconfirmed target returns an error and is retried
	// on the next call. Slider-only backends verify through their setter.
	SetLimit(limit int) error
	Disable() error
}

// The client chooses the preference path or the slider API. Tests replace
// these operations without calling Apple services or changing preferences.
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

// The agent registers a reloaded target within milliseconds. SetLimit polls
// for it this many times, this far apart, before reporting a failure.
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
		return c.customLimit()
	}
	return c.api.Limit()
}

func (c *Client) customLimit() (int, bool, error) {
	limit, enabled, err := c.api.Preferences()
	if err != nil {
		return 0, false, err
	}
	// A saved preference does not prove the agent has loaded it. Its getter
	// reports exact sub-80 targets on macOS 27 (unlike its clamping setter).
	// Keep retrying if the agent still has a previous target in memory.
	agentLimit, agentEnabled, err := c.api.Limit()
	if err != nil {
		return 0, false, err
	}
	if agentEnabled && (!enabled || agentLimit != limit) {
		// Preserve enabled=true so a request to disable still releases the
		// existing registration while a new target is pending.
		return 0, true, nil
	}
	if enabled && agentEnabled {
		registered, err := c.api.RegisteredManualLimits()
		if err != nil {
			return 0, true, err
		}
		if len(registered) == 0 {
			return 0, true, nil
		}
		for _, target := range registered {
			if target != limit {
				return 0, true, nil
			}
		}
	}
	return limit, enabled && agentEnabled, nil
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
	current, enabled, err := c.customLimit()
	if err != nil {
		return fmt.Errorf("read native target before changing it: %w", err)
	}
	if enabled && current == limit {
		return nil
	}
	// customLimit returns a zero target while the agent has stale settings.
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
	current, enabled, err = c.api.Preferences()
	if err != nil {
		return fmt.Errorf("verify native charge target: %w", err)
	}
	if !enabled || current != limit {
		return fmt.Errorf("native target did not retain %d%% (read %d%%, enabled=%t)", limit, current, enabled)
	}
	// Allow the reload notification to reach the agent before confirming
	// registration. This checks policy, not the battery's physical response.
	for attempt := range registrationChecks {
		if attempt > 0 {
			c.wait(registrationCheckInterval)
		}
		current, enabled, err = c.customLimit()
		if err != nil {
			return fmt.Errorf("verify native charge registration: %w", err)
		}
		if enabled && current == limit {
			return nil
		}
	}
	return fmt.Errorf("native charge target %d%% registration is not confirmed; batt will retry", limit)
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
