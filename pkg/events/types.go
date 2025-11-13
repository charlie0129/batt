package events

import "encoding/json"

// Event name constants
const (
	CalibrationPhase = "calibration.phase"
)

// Event is a generic SSE event from daemon.
type Event struct {
	Name string          // SSE event name
	Data json.RawMessage // Raw JSON payload
}

// CalibrationPhaseEvent is the typed payload for calibration.phase.
type CalibrationPhaseEvent struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Message string `json:"message,omitempty"`
	Ts      int64  `json:"ts"`
}

// DecodeAs decodes the event payload into the caller-specified generic type T.
// It ignores the event name and simply unmarshals Data into T. If Data is empty,
// it returns the zero value of T with a nil error.
//
// Example:
//
//	payload, err := events.DecodeAs[events.CalibrationPhaseEvent](ev)
//	if err != nil { /* handle */ }
//	fmt.Println(payload.From, payload.To)
func DecodeAs[T any](e Event) (T, error) {
	var zero T
	if len(e.Data) == 0 {
		return zero, nil
	}
	var v T
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return zero, err
	}
	return v, nil
}
