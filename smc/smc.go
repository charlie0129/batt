package smc

import (
	"github.com/charlie0129/gosmc"
	"github.com/sirupsen/logrus"
)

// Connection is a wrapper of gosmc.Connection.
type Connection struct {
	*gosmc.Connection
}

// New returns a new Connection.
func New() *Connection {
	return &Connection{
		Connection: gosmc.New(),
	}
}

// Open opens the connection.
func (c *Connection) Open() error {
	return c.Connection.Open()
}

// Close closes the connection.
func (c *Connection) Close() error {
	return c.Connection.Close()
}

// Read reads a value from SMC.
func (c *Connection) Read(key string) (gosmc.SMCVal, error) {
	logrus.Tracef("trying to read %s", key)

	v, err := c.Connection.Read(key)
	if err != nil {
		return v, err
	}

	logrus.Tracef("read %s succeed, value=%#v", key, v)

	return v, nil
}

// Write writes a value to SMC.
func (c *Connection) Write(key string, value []byte) error {
	logrus.Tracef("trying to write %#v to %s", value, key)

	err := c.Connection.Write(key, value)
	if err != nil {
		return err
	}

	logrus.Tracef("write %#v to %s succeed", value, key)

	return nil
}
