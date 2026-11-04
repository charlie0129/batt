package daemon

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Logger is the logrus logger handler
func ginLogger(logger logrus.FieldLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// other handler can change c.Path so:
		path := c.Request.URL.Path
		start := time.Now()
		c.Next()
		stop := time.Since(start)
		latency := int(math.Ceil(float64(stop.Nanoseconds()) / 1000000.0))
		statusCode := c.Writer.Status()
		dataLength := c.Writer.Size()
		if dataLength < 0 {
			dataLength = 0
		}

		entry := logger.WithFields(logrus.Fields{
			"statusCode": statusCode,
			"latency":    latency, // time to process
			"method":     c.Request.Method,
			"path":       path,
			"dataLength": dataLength,
		})

		if len(c.Errors) > 0 {
			entry.Error(c.Errors.ByType(gin.ErrorTypePrivate).String())
		} else {
			msg := fmt.Sprintf("%s %s %d (%dms)", c.Request.Method, path, statusCode, latency)
			//nolint:gocritic
			if statusCode >= http.StatusInternalServerError {
				entry.Error(msg)
			} else if statusCode >= http.StatusBadRequest {
				entry.Warn(msg)
			} else {
				entry.Trace(msg)
			}
		}
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)

	totalHours := int64(d / time.Hour)
	minutes := int64((d % time.Hour) / time.Minute)

	days := totalHours / 24
	hours := totalHours % 24

	var parts []string

	if days > 0 {
		if days == 1 {
			parts = append(parts, "1 day")
		} else {
			parts = append(parts, fmt.Sprintf("%d days", days))
		}
	}
	if hours > 0 {
		if hours == 1 {
			parts = append(parts, "1 hour")
		} else {
			parts = append(parts, fmt.Sprintf("%d hours", hours))
		}
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d min", minutes))
	}

	if len(parts) == 0 {
		return "0 min"
	}

	return strings.Join(parts, " ")
}

// TimeSeriesRecorder records the last N maintain loop times.
type TimeSeriesRecorder struct {
	MaxRecordCount        int
	LastMaintainLoopTimes []time.Time
	mu                    *sync.Mutex
}

// NewTimeSeriesRecorder returns a new TimeSeriesRecorder.
func NewTimeSeriesRecorder(maxRecordCount int) *TimeSeriesRecorder {
	return &TimeSeriesRecorder{
		MaxRecordCount:        maxRecordCount,
		LastMaintainLoopTimes: make([]time.Time, 0),
		mu:                    &sync.Mutex{},
	}
}

// AddRecordNow adds a new record with the current time.
func (r *TimeSeriesRecorder) AddRecordNow() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) >= r.MaxRecordCount {
		r.LastMaintainLoopTimes = r.LastMaintainLoopTimes[1:]
	}
	// Round to strip monotonic clock reading.
	// This will prevent time.Since from returning values that are not accurate (especially when the system is in sleep mode).
	r.LastMaintainLoopTimes = append(r.LastMaintainLoopTimes, time.Now().Round(0))
}

// ClearRecords clears all records.
func (r *TimeSeriesRecorder) ClearRecords() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.LastMaintainLoopTimes = make([]time.Time, 0)
}

// GetRecords returns the records.
func (r *TimeSeriesRecorder) GetRecords() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.LastMaintainLoopTimes
}

// GetRecordsString returns the records in string format.
func (r *TimeSeriesRecorder) GetRecordsString() []string {
	records := r.GetRecords()
	var recordsString []string
	for _, record := range records {
		recordsString = append(recordsString, record.Format(time.RFC3339))
	}
	return recordsString
}

// AddRecord adds a new record.
func (r *TimeSeriesRecorder) AddRecord(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Strip monotonic clock reading.
	t = t.Round(0)

	if len(r.LastMaintainLoopTimes) >= r.MaxRecordCount {
		r.LastMaintainLoopTimes = r.LastMaintainLoopTimes[1:]
	}
	r.LastMaintainLoopTimes = append(r.LastMaintainLoopTimes, t)
}

// GetRecordsIn returns the number of continuous records in the last duration.
func (r *TimeSeriesRecorder) GetRecordsIn(last time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	// The last record must be within the last duration.
	if len(r.LastMaintainLoopTimes) > 0 && time.Since(r.LastMaintainLoopTimes[len(r.LastMaintainLoopTimes)-1]) >= loopInterval+time.Second {
		return 0
	}

	// Find continuous records from the end of the list.
	// Continuous records are defined as the time difference between
	// two adjacent records is less than loopInterval+1 second.
	count := 0
	for i := len(r.LastMaintainLoopTimes) - 1; i >= 0; i-- {
		record := r.LastMaintainLoopTimes[i]
		if time.Since(record) > last {
			break
		}

		theRecordAfter := record
		if i+1 < len(r.LastMaintainLoopTimes) {
			theRecordAfter = r.LastMaintainLoopTimes[i+1]
		}

		if theRecordAfter.Sub(record) >= loopInterval+time.Second {
			break
		}
		count++
	}

	return count
}

// GetLastRecords returns the time differences between the records and the current time.
func (r *TimeSeriesRecorder) GetLastRecords(last time.Duration) []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) == 0 {
		return nil
	}

	var records []time.Time
	for i := len(r.LastMaintainLoopTimes) - 1; i >= 0; i-- {
		record := r.LastMaintainLoopTimes[i]
		if time.Since(record) > last {
			break
		}
		records = append(records, record)
	}

	return records
}

//nolint:unused // .
func formatTimes(times []time.Time) []string {
	var timesString []string
	for _, t := range times {
		timesString = append(timesString, t.Format(time.RFC3339))
	}
	return timesString
}

func formatRelativeTimes(times []time.Time) []string {
	var timesString []string
	for _, t := range times {
		timesString = append(timesString, fmt.Sprintf("%.2fs", time.Since(t).Seconds()))
	}
	return timesString
}

// GetLastRecord returns the last record.
func (r *TimeSeriesRecorder) GetLastRecord() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.LastMaintainLoopTimes) == 0 {
		return time.Time{}
	}

	return r.LastMaintainLoopTimes[len(r.LastMaintainLoopTimes)-1]
}
