// Package logging configures the process-wide slog logger: logfmt output,
// the level names batt has always accepted, and a trace level below debug
// for the SMC key traffic.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// LevelTrace is finer than slog.LevelDebug. It carries every SMC read and
// write and the per-request access log.
const LevelTrace = slog.LevelDebug - 4

// ParseLevel accepts trace, debug, info, warn, warning, error, fatal and
// panic. slog has no fatal or panic level, so both mean error.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error", "fatal", "panic":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

// LevelName is the lowercase logfmt name of a level: trace, debug, info,
// warn or error.
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelDebug:
		return "trace"
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

// NewHandler returns a logfmt handler writing records at or above level to
// w. timeFormat formats the time key; empty leaves slog's RFC 3339 default.
func NewHandler(w io.Writer, level slog.Level, timeFormat string) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Errors print as their message. The default is %+v, which for
			// github.com/pkg/errors values dumps a stack trace into the line.
			if err, ok := a.Value.Any().(error); ok {
				return slog.String(a.Key, err.Error())
			}
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.LevelKey:
				if lvl, ok := a.Value.Any().(slog.Level); ok {
					a.Value = slog.StringValue(LevelName(lvl))
				}
			case slog.TimeKey:
				if t, ok := a.Value.Any().(time.Time); ok && timeFormat != "" {
					a.Value = slog.StringValue(t.Format(timeFormat))
				}
			}
			return a
		},
	})
}

// Setup installs NewHandler's logger as the default logger.
func Setup(w io.Writer, level slog.Level, timeFormat string) {
	slog.SetDefault(slog.New(NewHandler(w, level, timeFormat)))
}

// Trace logs msg at LevelTrace with the default logger.
func Trace(msg string, args ...any) {
	slog.Log(context.Background(), LevelTrace, msg, args...)
}

// Fatal logs msg at error level and exits the process with status 1.
func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}
