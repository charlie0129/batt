package main

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "minutes", input: "30m", want: 30 * time.Minute},
		{name: "hours", input: "2h", want: 2 * time.Hour},
		{name: "composite", input: "1h30m", want: 90 * time.Minute},
		{name: "days", input: "1d", want: 24 * time.Hour},
		{name: "multiple days", input: "3d", want: 72 * time.Hour},
		{name: "weeks", input: "1w", want: 7 * 24 * time.Hour},
		{name: "surrounding spaces", input: " 1d ", want: 24 * time.Hour},
		{name: "zero days", input: "0d", want: 0},
		{name: "largest representable days", input: "106751d", want: 106751 * 24 * time.Hour},
		{name: "largest representable weeks", input: "15250w", want: 15250 * 7 * 24 * time.Hour},
		{name: "days overflow", input: "106752d", wantErr: true},
		{name: "weeks overflow", input: "15251w", wantErr: true},
		{name: "days beyond int range", input: "99999999999999999999d", wantErr: true},
		{name: "empty", input: "", wantErr: true},
		{name: "missing unit", input: "1", wantErr: true},
		{name: "unknown unit", input: "1y", wantErr: true},
		{name: "fractional days unsupported", input: "1.5d", wantErr: true},
		{name: "negative days unsupported", input: "-1d", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
