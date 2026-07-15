package gui

import (
	"testing"
	"time"
)

func TestTemporaryDisableDuration(t *testing.T) {
	tests := []struct {
		item menuItem
		want time.Duration
	}{
		{item: itemDisableLimit1Hour, want: time.Hour},
		{item: itemDisableLimit2Hours, want: 2 * time.Hour},
		{item: itemDisableLimit4Hours, want: 4 * time.Hour},
		{item: itemDisableLimit8Hours, want: 8 * time.Hour},
		{item: itemDisableLimit12Hours, want: 12 * time.Hour},
		{item: itemDisableLimit24Hours, want: 24 * time.Hour},
		{item: itemDisableLimit2Days, want: 2 * 24 * time.Hour},
		{item: itemDisableLimit3Days, want: 3 * 24 * time.Hour},
		{item: itemDisableLimit7Days, want: 7 * 24 * time.Hour},
	}

	for _, tt := range tests {
		if got := temporaryDisableDuration(tt.item); got != tt.want {
			t.Errorf("temporaryDisableDuration(%d) = %s, want %s", tt.item, got, tt.want)
		}
	}
}

func TestTemporaryDisableCountdownTitle(t *testing.T) {
	tests := []struct {
		name      string
		remaining time.Duration
		want      string
	}{
		{name: "days", remaining: 7 * 24 * time.Hour, want: "Restores to 80% in 7d"},
		{name: "composite", remaining: 49*time.Hour + time.Minute, want: "Restores to 80% in 2d 1h 1m"},
		{name: "rounds up partial minute", remaining: 30 * time.Second, want: "Restores to 80% in 1m"},
		{name: "elapsed", remaining: 0, want: "Restoring 80% limit…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := temporaryDisableCountdownTitle(80, tt.remaining); got != tt.want {
				t.Errorf("temporaryDisableCountdownTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
