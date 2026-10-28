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
)

// Controller is the subset of PowerUI charge control used by the daemon.
type Controller interface {
	Supported() bool
	AvailableLimits() ([]int, error)
	// Limit reads the target and feature switch, not physical enforcement.
	// A zero target with enabled=true means the agent and saved settings
	// disagree; callers must reconcile rather than assume a disabled limiter.
	Limit() (limit int, enabled bool, err error)
	// SetLimit requests a target; 100 releases the native limit.
	SetLimit(limit int) error
	Disable() error
}

// ErrUnavailable indicates that the native charge-limit service is unavailable.
var ErrUnavailable = errors.New("PowerUI manual charge limit is unavailable")

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
}

// Client serializes access to the native charge-limit service.
type Client struct {
	mu  sync.Mutex
	api operations
}

// New creates a client without probing or changing the system.
func New() *Client { return &Client{api: nativeBridge{}} }

func (c *Client) Supported() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.api.Supported()
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
	// customLimit returns a zero target when the agent has stale settings. That
	// needs another preference notification, not an enable call that resets
	// the target to 80% and can release a lower hold.
	_, agentEnabled, err := c.api.Limit()
	if err != nil {
		return fmt.Errorf("read native feature state before changing it: %w", err)
	}
	// Explicitly re-engage the agent after a previous disable. A preference
	// notification alone can leave it in its ChargingToFull state.
	// enableMCL temporarily selects Apple's 80% default. Do not call it for
	// an already-enabled controller: it would release an existing lower hold.
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
	return nil
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
