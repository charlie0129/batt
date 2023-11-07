package main

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
)

var timeFormat = "2006-01-02 15:04:05.000"

func setupLogger() error {
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return fmt.Errorf("failed to parse log level: %v", err)
	}
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: timeFormat,
		FullTimestamp:   true,
	})

	return nil
}

func main() {
	cmd := NewCommand()
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
