package daemon

import "github.com/charlie0129/batt/pkg/powerui"

// SleepAssertion is a power assertion that a powerd restart would drop.
type SleepAssertion = powerui.SleepAssertion

// ForeignSleepAssertions includes batt's own assertions as well as other
// applications'. In particular, a calibration hold must delay a restart;
// powerd's own display assertion is the only one filtered out.
func (a *iokitAssertions) ForeignSleepAssertions() ([]SleepAssertion, error) {
	return powerui.ForeignSleepAssertions()
}
