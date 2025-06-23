package smc

import (
	"github.com/charlie0129/gosmc"
	"github.com/sirupsen/logrus"
)

// AppleSMC is a wrapper of gosmc.Connection.
type AppleSMC struct {
	conn gosmc.Connection
}

// New returns a new AppleSMC.
func New() *AppleSMC {
	return &AppleSMC{
		conn: gosmc.New(),
	}
}

// NewMock returns a new mocked AppleSMC with prefill values.
func NewMock(prefillValues map[string][]byte) *AppleSMC {
	conn := gosmc.NewMockConnection()

	for key, value := range prefillValues {
		err := conn.Write(key, value)
		if err != nil {
			panic(err)
		}
	}

	return &AppleSMC{
		conn: conn,
	}
}

// Open opens the connection.
func (c *AppleSMC) Open() error {
	return c.conn.Open()
}

// Close closes the connection.
func (c *AppleSMC) Close() error {
	return c.conn.Close()
}

// Read reads a value from SMC.
func (c *AppleSMC) Read(key string) (gosmc.SMCVal, error) {
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

// Write writes a value to SMC.
func (c *AppleSMC) Write(key string, value []byte) error {
	logrus.WithFields(logrus.Fields{
		"key": key,
		"val": value,
	}).Trace("Trying to write to SMC")

	err := c.conn.Write(key, value)
	if err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"key": key,
		"val": value,
	}).Trace("Write to SMC succeed")

	return nil
}
