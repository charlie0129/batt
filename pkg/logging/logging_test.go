package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	pkgerrors "github.com/pkg/errors"
)

func TestParseLevel(t *testing.T) {
	for input, want := range map[string]slog.Level{
		"trace": LevelTrace, "debug": slog.LevelDebug, "info": slog.LevelInfo, "INFO": slog.LevelInfo,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError,
		"fatal": slog.LevelError, "panic": slog.LevelError,
	} {
		got, err := ParseLevel(input)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("ParseLevel must reject unknown names")
	}
}

func TestHandlerWritesLogfmtWithLowercaseLevels(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, LevelTrace, "15:04"))
	logger.Info("config loaded", "upperLimit", 80, "path", "/etc/batt.json")
	logger.Log(t.Context(), LevelTrace, "read key", "key", "BUIC")
	logger.Warn("careful", "error", "disk full")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "level=info msg=\"config loaded\" upperLimit=80 path=/etc/batt.json") {
		t.Errorf("line 0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "level=trace msg=\"read key\" key=BUIC") {
		t.Errorf("line 1 = %q", lines[1])
	}
	if !strings.Contains(lines[2], "level=warn msg=careful error=\"disk full\"") {
		t.Errorf("line 2 = %q", lines[2])
	}
	if !strings.HasPrefix(lines[0], "time=") || len(strings.Fields(lines[0])[0]) != len("time=15:04") {
		t.Errorf("time not formatted with the given layout: %q", lines[0])
	}
}

func TestHandlerFiltersBelowLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelInfo, ""))
	logger.Debug("hidden")
	logger.Log(t.Context(), LevelTrace, "hidden")
	logger.Info("shown")
	if got := buf.String(); strings.Contains(got, "hidden") || !strings.Contains(got, "shown") {
		t.Fatalf("output = %q", got)
	}
}

func TestLevelName(t *testing.T) {
	for level, want := range map[slog.Level]string{
		LevelTrace: "trace", slog.LevelDebug: "debug", slog.LevelDebug + 1: "debug",
		slog.LevelInfo: "info", slog.LevelWarn: "warn", slog.LevelError: "error", slog.LevelError + 4: "error",
	} {
		if got := LevelName(level); got != want {
			t.Errorf("LevelName(%v) = %q, want %q", level, got, want)
		}
	}
}

func TestHandlerPrintsErrorsAsTheirMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelInfo, ""))
	logger.Error("request failed", "error", pkgerrors.Wrap(pkgerrors.New("daemon not running"), "failed to send request"))

	got := buf.String()
	if !strings.Contains(got, `error="failed to send request: daemon not running"`) {
		t.Fatalf("error not printed as its message: %q", got)
	}
	if strings.Contains(got, "\\n") || strings.Contains(got, ".go:") {
		t.Fatalf("stack trace leaked into the log line: %q", got)
	}
}
