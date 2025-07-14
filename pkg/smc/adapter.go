package smc

import (
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	ErrNoAdapterCapability = errors.New("no adapter capability found")
)

// IsAdapterEnabled returns whether the adapter is enabled.
func (c *AppleSMC) IsAdapterEnabled() (bool, error) {
	logrus.Tracef("IsAdapterEnabled called")

	var ret bool
	var key string

	switch {
	case c.capabilities[AdapterKey1]:
		key = AdapterKey1
	case c.capabilities[AdapterKey2]:
		key = AdapterKey2
	case c.capabilities[AdapterKey3]: // Tahoe firmware versions.
		key = AdapterKey3
	default:
		return false, ErrNoAdapterCapability
	}

	v, err := c.Read(key)
	if err != nil {
		return false, err
	}

	ret = len(v.Bytes) == 1 && v.Bytes[0] == 0x0

	logrus.Tracef("IsAdapterEnabled returned %t", ret)

	return ret, nil
}

// EnableAdapter enables the adapter.
func (c *AppleSMC) EnableAdapter() error {
	logrus.Tracef("EnableAdapter called")

	switch {
	case c.capabilities[AdapterKey1]:
		return c.Write(AdapterKey1, []byte{0x0})
	case c.capabilities[AdapterKey2]:
		return c.Write(AdapterKey2, []byte{0x0})
	case c.capabilities[AdapterKey3]: // Tahoe firmware versions.
		return c.Write(AdapterKey3, []byte{0x0})
	default:
		return ErrNoAdapterCapability
	}
}

// DisableAdapter disables the adapter.
func (c *AppleSMC) DisableAdapter() error {
	logrus.Tracef("DisableAdapter called")

	switch {
	case c.capabilities[AdapterKey1]:
		return c.Write(AdapterKey1, []byte{0x1})
	case c.capabilities[AdapterKey2]:
		return c.Write(AdapterKey2, []byte{0x1})
	case c.capabilities[AdapterKey3]: // Tahoe firmware versions.
		return c.Write(AdapterKey3, []byte{0x8})
	default:
		return ErrNoAdapterCapability
	}
}
