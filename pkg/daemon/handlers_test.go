package daemon

import (
	"testing"
	"time"
)

func TestResolveDisableLimit(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name            string
		upper           int
		disableUntil    time.Time
		preDisableLimit int
		want            int
		wantOK          bool
	}{
		{
			name:   "limit is set",
			upper:  80,
			want:   80,
			wantOK: true,
		},
		{
			name:   "already disabled without a timer",
			upper:  100,
			want:   0,
			wantOK: false,
		},
		{
			name:            "already disabled with a pending timer",
			upper:           100,
			disableUntil:    now.Add(time.Hour),
			preDisableLimit: 80,
			want:            80,
			wantOK:          true,
		},
		{
			name:            "saved limit without a timer",
			upper:           100,
			preDisableLimit: 80,
			want:            0,
			wantOK:          false,
		},
		{
			name:            "pending timer with an invalid saved limit",
			upper:           100,
			disableUntil:    now.Add(time.Hour),
			preDisableLimit: 5,
			want:            0,
			wantOK:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &mockConf{
				upper:           tt.upper,
				disableUntil:    tt.disableUntil,
				preDisableLimit: tt.preDisableLimit,
			}

			got, gotOK := resolveDisableLimit(c)
			if got != tt.want || gotOK != tt.wantOK {
				t.Errorf("resolveDisableLimit() = (%d, %v), want (%d, %v)", got, gotOK, tt.want, tt.wantOK)
			}
		})
	}
}
