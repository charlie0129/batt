package gui

const (
	quitTooltipInstalled = `Quit the batt menubar app, but keep the batt daemon running.

Since the batt daemon is still running, batt can continue to control charging. This is useful if you don't want the menubar icon to show up, but still want to use batt. When the client is not running, you can change batt settings using the command line interface (batt). To prevent the menubar app from starting at login, you can remove it in System Settings -> General -> Login Items & Extensions -> remove batt.app from the list (do NOT remove the batt daemon).

If you want to stop batt completely (menubar app and the daemon), you can use the "Disable Charging Limit" command. To uninstall, you can use the "Uninstall Daemon" command in the Advanced menu.`
	quitTooltipNotInstalled = `Quit the batt menubar app.`
)
