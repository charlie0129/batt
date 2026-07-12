package smc

import (
	"github.com/charlie0129/gosmc"
	"github.com/sirupsen/logrus"
)

// AppleSMC is a wrapper of gosmc.Client.
type AppleSMC struct {
	conn *gosmc.Client
	// capabilities is a map of SMC keys and their availability. Cached
	// after Open() call to avoid unnecessary SMC reads.
	capabilities map[string]bool
}

// New returns a new AppleSMC.
func New() *AppleSMC {
	return &AppleSMC{
		conn:         gosmc.New(),
		capabilities: make(map[string]bool),
	}
}

// NewMock returns a new mocked AppleSMC with prefill values.
func NewMock(prefillValues map[string][]byte) *AppleSMC {
	values := make([]gosmc.Value, 0, len(prefillValues))
	for key, value := range prefillValues {
		v, err := gosmc.NewValue(key, gosmc.DataType("hex_"), value)
		if err != nil {
			panic(err)
		}
		values = append(values, v)
	}

	return &AppleSMC{
		conn:         gosmc.NewMock(values...),
		capabilities: make(map[string]bool),
	}
}

// Open opens the connection and checks capabilities.
func (c *AppleSMC) Open() error {
	err := c.conn.Open()
	if err != nil {
		return err
	}

	for _, key := range allKeys {
		c.capabilities[key] = c.test(key)
	}

	return nil
}

// Close closes the connection.
func (c *AppleSMC) Close() error {
	return c.conn.Close()
}

// Read reads a value from SMC.
func (c *AppleSMC) Read(key string) (gosmc.Value, error) {
	logrus.WithFields(logrus.Fields{
		"key": key,
	}).Trace("Trying to read from SMC")

	v, err := c.conn.Read(key)
	if err != nil {
		return v, err
	}

	logrus.WithFields(logrus.Fields{
		"key": key,
		"val": v,
	}).Trace("Load from SMC succeed")

	return v, nil
}

// test tells whether the key exists in SMC.
func (c *AppleSMC) test(key string) bool {
	_, err := c.Read(key)
	return err == nil
}

// Write writes a value to SMC.
func (c *AppleSMC) Write(key string, value []byte) error {
	logrus.WithFields(logrus.Fields{
		"key": key,
		"val": value,
	}).Trace("Trying to write to SMC")

	err := c.conn.WriteBytes(key, value)
	if err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"key": key,
		"val": value,
	}).Trace("Write to SMC succeed")

	return nil
}
