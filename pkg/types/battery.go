package types

// DetailedBatteryInfo holds comprehensive battery data for the GUI.
type DetailedBatteryInfo struct {
	CycleCount      int     `json:"cycle_count"`
	Condition       string  `json:"condition"`
	MaximumCapacity float64 `json:"maximum_capacity"`
}
