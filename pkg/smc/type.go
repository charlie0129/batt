package smc

import (
	"github.com/charlie0129/gosmc"
)

type Connection interface {
	Open() error
	Close() error
	Read(key string) (gosmc.SMCVal, error)
	Write(key string, value []byte) error
}
