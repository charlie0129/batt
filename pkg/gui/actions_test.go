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
		{item: itemForceDischarge1Hour, want: time.Hour},
		{item: itemForceDischarge2Hours, want: 2 * time.Hour},
		{item: itemForceDischarge4Hours, want: 4 * time.Hour},
		{item: itemForceDischarge8Hours, want: 8 * time.Hour},
	}

	for _, tt := range tests {
		if got := temporaryDisableDuration(tt.item); got != tt.want {
			t.Errorf("temporaryDisableDuration(%d) = %s, want %s", tt.item, got, tt.want)
		}
	}
}

func TestTemporaryAdapterDisableCountdownTitle(t *testing.T) {
	tests := []struct {
		remaining time.Duration
		want      string
	}{
		{remaining: 2*time.Hour + time.Minute, want: "Restores power adapter in 2h 1m"},
		{remaining: 30 * time.Second, want: "Restores power adapter in 1m"},
		{remaining: 0, want: "Restoring power adapter…"},
	}
	for _, tt := range tests {
		if got := temporaryAdapterDisableCountdownTitle(tt.remaining); got != tt.want {
			t.Errorf("temporaryAdapterDisableCountdownTitle(%s) = %q, want %q", tt.remaining, got, tt.want)
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

func TestChargeOnceLimitTitle(t *testing.T) {
	tests := []struct {
		limit int
		want  string
	}{
		{limit: 70, want: "Charge to 70% Now"},
		{limit: 50, want: "Charge to 50% Now"},
		// Without a limit there is nothing to charge to, so the item keeps a
		// neutral label until the daemon reports one.
		{limit: 100, want: "Charge to Limit Now"},
		{limit: 0, want: "Charge to Limit Now"},
	}

	for _, tt := range tests {
		if got := chargeOnceLimitTitle(tt.limit); got != tt.want {
			t.Errorf("chargeOnceLimitTitle(%d) = %q, want %q", tt.limit, got, tt.want)
		}
	}
}

func TestChargeOnceStatusTitle(t *testing.T) {
	tests := []struct {
		target        int
		currentCharge int
		want          string
	}{
		{target: 70, currentCharge: 58, want: "Charging to 70%, now 58%"},
		{target: 100, currentCharge: 71, want: "Charging to 100%, now 71%"},
		{target: 100, currentCharge: 0, want: "Charging to 100%…"},
	}

	for _, tt := range tests {
		if got := chargeOnceStatusTitle(tt.target, tt.currentCharge); got != tt.want {
			t.Errorf("chargeOnceStatusTitle(%d, %d) = %q, want %q", tt.target, tt.currentCharge, got, tt.want)
		}
	}
}
