package daemon

import (
	"sync"
	"testing"
	"time"
)

func TestMaintainLoopRecorder_GetRecordsIn(t *testing.T) {
	type fields struct {
		MaxRecordCount        int
		LastMaintainLoopTimes []time.Time
		mu                    *sync.Mutex
	}
	type args struct {
		last time.Duration
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
	}{
		{
			name: "test noncontinuous records",
			fields: fields{
				MaxRecordCount: 10,
				LastMaintainLoopTimes: []time.Time{
					time.Now().Add(-time.Second * 31).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 20).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 10).Add(-10 * time.Millisecond),
				},
				mu: &sync.Mutex{},
			},
			args: args{
				last: time.Second * 40,
			},
			want: 2,
		},
		{
			name: "test continuous records",
			fields: fields{
				MaxRecordCount: 10,
				LastMaintainLoopTimes: []time.Time{
					time.Now().Add(-time.Second * 70).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 60).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 40).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 30).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 20).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 10).Add(-10 * time.Millisecond),
				},
				mu: &sync.Mutex{},
			},
			args: args{
				last: time.Second * 50,
			},
			want: 4,
		},
		{
			name: "test continuous records 2",
			fields: fields{
				MaxRecordCount: 10,
				LastMaintainLoopTimes: []time.Time{
					time.Now().Add(-time.Second * 70).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 60).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 40).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 30).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 20).Add(-10 * time.Millisecond),
					time.Now().Add(-time.Second * 15).Add(-10 * time.Millisecond),
				},
				mu: &sync.Mutex{},
			},
			args: args{
				last: time.Second * 50,
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		loopInterval = time.Second * 10
		t.Run(tt.name, func(t *testing.T) {
			r := &TimeSeriesRecorder{
				MaxRecordCount:        tt.fields.MaxRecordCount,
				LastMaintainLoopTimes: tt.fields.LastMaintainLoopTimes,
				mu:                    tt.fields.mu,
			}
			if got := r.GetRecordsIn(tt.args.last); got != tt.want {
				t.Errorf("GetRecordsIn() = %v, want %v", got, tt.want)
			}
		})
	}
}
