package hack

import _ "embed"

var (
	//go:embed cc.chlc.batt.plist
	LaunchDaemonPlistTemplate string
	//go:embed cc.chlc.battapp.plist
	LaunchAgentPlistTemplate string
)
