package hack

import _ "embed"

var (
	//go:embed cc.chlc.batt.plist
	LaunchDaemonPlistTemplate string
)
