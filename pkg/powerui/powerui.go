// Package powerui controls native charging. On macOS 27 a private powerd
// ceiling policy stops charging without requesting discharge or suppressing
// Apple's full-charge maintenance. Older systems use PowerUI's slider limit.
package powerui

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Controller is the subset of PowerUI charge control used by the daemon.
type Controller interface {
	Supported() bool
	AvailableLimits() ([]int, error)
	// SupportsExactTargets reports whether exact percentage targets are available.
	SupportsExactTargets() bool
	// Limit checks the stored policy and powerd registrations, not physical
	// charging current. A zero target with enabled=true requires reconciliation.
	Limit() (limit int, enabled bool, err error)
	// SetLimit reapplies a target and confirms its registration; 100 releases
	// the native limit. An unconfirmed target returns an error and is retried
	// on the next call. Slider-only backends verify through their setter.
	SetLimit(limit int) error
	Disable() error
}

// The client chooses the no-drain policy or the older slider API. Tests replace
// these operations without calling Apple services or changing preferences.
type operations interface {
	Supported() bool
	AvailableLimits() ([]int, error)
	Limit() (int, bool, error)
	Disable() error
	CustomLimits() bool
	Policy() (int, bool, error)
	SetPolicy(int) error
	SetSliderLimit(int) error
	Registrations() (targets []int, manual bool, err error)
	RefreshBattery() error
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
	limit, enabled, err := c.api.Policy()
	if err != nil {
		return 0, false, err
	}
	targets, manual, err := c.api.Registrations()
	if err != nil {
		return 0, enabled, err
	}
	if enabled != (len(targets) > 0) {
		return 0, true, nil
	}
	// An Apple manual limit can request discharge even when our ceiling agrees.
	// Leave it alone once batt's own policy has been released.
	if enabled && manual {
		return 0, true, nil
	}
	for _, target := range targets {
		if target != limit {
			return 0, true, nil
		}
	}
	return limit, enabled, nil
}

func (c *Client) SetLimit(limit int) error {
	if limit < 10 || limit > 100 {
		return fmt.Errorf("invalid native charge limit %d: expected 10 through 100", limit)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.api.CustomLimits() {
		if limit == 100 {
			return c.api.Disable()
		}
		return c.api.SetSliderLimit(limit)
	}
	wantEnabled := limit < 100
	_, enabled, err := c.customLimit()
	if err != nil {
		return fmt.Errorf("read native target before changing it: %w", err)
	}
	if !wantEnabled && !enabled {
		return nil
	}
	if err := c.api.SetPolicy(limit); err != nil {
		return fmt.Errorf("write native no-drain ceiling: %w", err)
	}
	// Let powerd load the policy before confirming all flags and registrations.
	// This verifies policy, not the battery's physical response.
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		current, enabled, err := c.customLimit()
		if err != nil {
			return fmt.Errorf("verify native charge registration: %w", err)
		}
		if enabled == wantEnabled && (!enabled || current == limit) {
			// Avoid displaying the pre-reload charging sample for another minute.
			if err := c.api.RefreshBattery(); err != nil {
				logrus.WithError(err).Warn("native target applied, but battery telemetry refresh failed")
			}
			return nil
		}
	}
	return fmt.Errorf("native charge target %d%% registration is not confirmed; batt will retry", limit)
}

func (c *Client) Disable() error {
	return c.SetLimit(100)
}
