# Usage

!!! tip
    It is _highly_ recommended to disable macOS's optimized charging when using `batt`. <br>
    To do so, open `System Settings` -> `Battery` -> `Battery Health` -> `i` -> Turn OFF `Optimized Battery Charging`. <br>
    Disable the built-in charge limit when using direct SMC control. In native mode, `batt` manages that setting itself.
    
### Limit battery charge

Set a maximum charge target. In native mode, macOS controls enforcement timing and full-charge overrides.

Setting the limit to 10-99 will enable the battery charge limit, limiting the maximum charge to _somewhere around_ your setting. However, setting the limit to 100 will disable the battery charge limit.

By default, `batt` will set a 60% charge limit.

To customize charge limit, see `batt limit`. For example, to set the limit to 80%, run `batt limit 80`. To disable the limit, run `batt disable` or `batt limit 100`.

In native mode, disabling releases the limit and leaves charging to macOS; it does not restore earlier Apple charge-limit settings.

To disable the limit only temporarily, use `batt disable --for=1d`. Your current limit is restored automatically once the duration has elapsed, even if your Mac reboots in the meantime. Durations accept minutes, hours, days and weeks, e.g. `30m`, `2h`, `1d`, `1w`. Setting a limit with `batt limit` before the duration elapses cancels the scheduled restore. `batt status` shows when the limit will be restored.

### Charge once

Charge the battery once without changing your charge limit.

- `batt charge now` starts charging right away and stops at your upper limit. Normally `batt` waits until the charge drops below the lower limit, so this is how you top up from inside that gap.
- `batt charge full` charges to 100% once. Your limit applies again as soon as the battery is full.
- `batt charge cancel` stops a running one-time charge.

Your charge limit and lower-limit delta stay as they are, so normal behavior resumes by itself. A one-time charge survives a daemon restart and a reboot. Setting a limit with `batt limit` cancels it too. `batt status` shows a running one-time charge.

A one-time charge and a disabled power adapter exclude each other, because cutting power stops the charge. `batt` refuses to start one while the other is active and names the conflict.

In firmware-managed mode, `batt charge now` hands the firmware the narrowest range that ends at your limit, and the firmware does not start charging from the top of that range. A one-time charge to the limit can therefore finish one percent short of it. `batt charge full` is exact, because it deactivates the limit instead of narrowing it.

Native mode does not support `batt charge now`, because macOS controls when charging resumes. `batt charge full` temporarily releases the native limit, then restores it at 100%. In adapter mode, both one-time actions use the adapter to control charging.

The GUI exposes the same actions in the menubar as **Charge to 70% Now** and **Charge to Full Once**, with the current progress and a **Cancel One-Time Charge** item while one is running.

### Enable/disable power adapter

Cut or restore power from the wall. This has the same effect as unplugging/plugging the power adapter, even if the adapter is physically plugged in.

This is useful when you want to use your battery to lower the battery charge, but you don't want to unplug the power adapter.

!!! note
    if you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *cutting power will cause your Mac to go to sleep* unless `batt prevent-sleep-on-adapter-disable` is enabled. See [Prevent sleep when adapter is disabled](#prevent-sleep-when-adapter-is-disabled) below for details and warnings.

To enable/disable the power adapter, see `batt adapter`. For example, to disable the power adapter, run `sudo batt adapter disable`. To enable the power adapter, run `sudo batt adapter enable`.

To run from the battery only temporarily, use `sudo batt adapter disable --for=2h`. The adapter is enabled automatically after the duration elapses, and the deadline survives daemon restarts and reboots. Durations accept minutes, hours, days and weeks, e.g. `30m`, `2h`, `1d`, `1w`. Running `batt adapter enable` or `batt adapter disable` before the duration elapses cancels the scheduled enable. `batt status` shows when the adapter will be enabled.

The GUI exposes the same behavior under **Advanced → Force Discharge...**, with regular-user presets of 1, 2, 4, or 8 hours and a separate indefinite option.

### Check status

!!! note
    This feature is CLI-only and is not available in the GUI version.

Check the current config, battery info, and charging status.

To do so, run `sudo batt status`. Example output:

```
Charging status:
  Allow charging: ✘ (refreshes can take up to 2 minutes)
    Your Mac will not charge, because your current charge is above the limit.
  Use power adapter: ✔

Battery status:
  Current charge: 65%
  State: not charging
  Full capacity: 8648 mAh / 8694 mAh (99%)
  Charge rate: +0.0 W
  Voltage: 11.89 V

Battery configuration:
  Upper limit: 60%
  Lower limit: 58%
  Prevent idle-sleep when charging: ✔
  Disable charging before sleep if charge limit is enabled: ✔
  Prevent system-sleep when charging: ✘
  Prevent sleep while adapter is disabled: ✘
  Allow non-root users to access the daemon: ✔
  Control MagSafe LED: ✔

Hardware compatibility:
  Charge control mode: legacy
  Sleep hooks: ✔
  MagSafe LED control: ✔
  Power adapter control: ✔
  Auto calibration: ✔

Calibration status:
  Phase: Idle
  Schedule: disabled
```

## Advanced

These advanced features are not for most users. Using the default setting for these options should work the best.

### Auto Calibration (experimental)

> Thanks @brookqin for implementing the initial version of this feature.

Automatically performs a full charging cycle to help calibrate reported battery percentage.

Phases: `Idle → DischargeToThreshold → ChargeToFull → HoldAfterFull → DischargeAfterHold → RestoreAndFinish → Idle`.

Flow:
1. Discharge below configured threshold (defaults to 15%). Charging is forced off.
2. Charge to 100%. Upper limit temporarily set to 100.
3. Hold at 100% for the configured duration (defaults to 120 minutes unless changed).
4. After hold, charging is disabled again and the battery is allowed to naturally discharge back down to the original upper limit snapshot.
5. Restore original upper/lower limits and adapter/charging states and return to Idle.

`batt` prevents idle system sleep for the entire calibration session, including while paused, and releases the assertion on completion, cancellation, or error. macOS can still force sleep when you close the lid or explicitly choose Sleep, so keep the lid open during calibration.

You can start, pause, resume, cancel via the GUI (Advanced → Auto Calibration) or CLI (`batt calibration start|pause|resume|cancel|status`) or HTTP API. Cancel restores original settings immediately.

To change the discharge threshold (defaults to 15%), run `batt calibration discharge-threshold <percentage>`.
To change the hold duration (defaults to 120 minutes), run `batt calibration hold-duration <minutes>`.

#### Scheduling automatic calibration

!!! note
    This feature is CLI-only and is not available in the GUI version.

Use `batt schedule` to let the daemon trigger calibration runs on a cron cadence. Examples:

```bash
batt schedule '0 10 * * 0'   # every Sunday at 10:00 (quote * so the shell doesn't expand it)
batt schedule '0 10 1 * *'   # At 10:00 on the first day of every month
batt schedule '0 10 1 */2 *' # At 10:00 on the first day of every two months
batt schedule disable        # remove the schedule and stop future runs
batt schedule postpone 90m   # push the next run back by 90 minutes (defaults to 1h if omitted)
batt schedule skip           # skip only the upcoming run and keep the schedule
```

After setting a cron expression, the CLI prints the next few run times returned by the daemon so you can verify the schedule. `postpone` and `skip` operate on the next scheduled execution only; `disable` clears the stored cron configuration.

Before a scheduled calibration actually begins, `batt` checks that the Mac is plugged into wall power. If it is not, the scheduler posts a reminder notification (GUI) and keeps waiting for roughly five minutes. If power is still unavailable after that grace period, the pending run is skipped automatically so later schedules are not blocked.

### Preventing idle sleep

!!! note
    This is a legacy-control option for older firmware < `20xxx`. It is unavailable and unnecessary with `20xxx` firmware, where the firmware enforces limits during sleep.

Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, `batt` will be paused when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, there is no way for batt to stop charging (since batt is paused by macOS) and the battery will charge to 100%. This option, together with disable-charging-pre-sleep, will prevent this from happening.

This option tells macOS NOT to go to sleep when the computer is in a charging session, so batt can continue to work until charging is finished. Note that it will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is completed.

However, this option does not prevent manual sleep (limitation of macOS). For example, if you manually put your computer to sleep (by choosing the Sleep option in the top-left Apple menu) or close the lid, batt will still be paused and the issue mentioned above will still happen. This is where disable-charging-pre-sleep comes in. See [Disabling charging before sleep](#disabling-charging-before-sleep).

To enable this feature, run `sudo batt prevent-idle-sleep enable`. To disable, run `sudo batt prevent-idle-sleep disable`.

### Disabling charging before sleep

!!! note
    This is a legacy-control option for older firmware < `20xxx`. It is unavailable and unnecessary with `20xxx` firmware, where the firmware enforces limits during sleep.

Set whether to disable charging before sleep if charge limit is enabled.

As described in [Preventing idle sleep](#preventing-idle-sleep), batt will be paused by macOS when your computer goes to sleep, and there is no way for batt to continue controlling battery charging. This option will disable charging just before sleep, so your computer will not overcharge during sleep, even if the battery charge is below the limit.

To enable this feature, run `sudo batt disable-charging-pre-sleep enable`. To disable, run `sudo batt disable-charging-pre-sleep disable`.

### Prevent system sleep

!!! note
    This is a legacy-control option for older firmware < `20xxx`. It is unavailable with `20xxx` firmware.

Set whether to prevent system sleep during a charging session (experimental).

This option tells macOS to create power assertion, which prevents sleep, when all conditions are met:

1) charging is active
2) battery charge limit is enabled
3) computer is connected to charger.

So your computer can go to sleep as soon as a charging session is completed / charger disconnected.

Does similar thing to [Preventing idle sleep](#preventing-idle-sleep), but works for manual sleep too.

!!! note
    Please disable [Preventing idle sleep](#preventing-idle-sleep) and [Disabling charging before sleep](#disabling-charging-before-sleep), while this feature is in use.

To enable this feature, run `sudo batt prevent-system-sleep enable`. To disable, run `sudo batt prevent-system-sleep disable`.

### Prevent sleep when adapter is disabled

!!! warning
    This is an advanced, opt-in feature. When enabled, your Mac will not sleep at all while the adapter is disabled, even with the lid closed. If placed in a bag while discharging, your Mac can dangerously overheat or drain its battery to 0%. Always monitor your Mac while using this feature, and prefer timed discharge over indefinite discharge.

Disabling the power adapter makes macOS behave as if the power cord was unplugged. In Clamshell mode (using a MacBook with an external display and the lid closed), macOS ends Clamshell mode when power is cut, causing your external display and Mac to sleep immediately. This affects manual force discharge (`batt adapter disable`), the discharge phases of Auto Calibration, and charge limiting in adapter mode.

This option suppresses system sleep entirely while the adapter is disabled by temporarily setting the global macOS `SleepDisabled` power setting (equivalent to `pmset disablesleep 1`), keeping Clamshell mode alive.

- `batt` durably records the previous system sleep setting before changing it.
- As soon as the adapter is re-enabled, the prior system sleep setting is automatically restored.
- If the daemon restarts while the adapter is still disabled, protection is reconciled immediately without a sleep gap.
- If the daemon is uninstalled or shuts down, the adapter is re-enabled before releasing holds and restoring the prior setting.
- If batt logs an unusable sleep snapshot (`/etc/batt.sleep.json` by default), the daemon still starts, but it cannot hold sleep until you fix or remove that file. If your Mac no longer sleeps, run `sudo pmset -a disablesleep 0`.

To enable this feature, run:
```bash
sudo batt prevent-sleep-on-adapter-disable enable
```

To disable:
```bash
sudo batt prevent-sleep-on-adapter-disable disable
```

The GUI also exposes this setting under **Advanced → Prevent Sleep when Adapter is Disabled**.

### Upper and lower charge limit

!!! note
    This feature is CLI-only and is not available in the GUI version.

When you set a charge limit, for example, on a Lenovo ThinkPad, you can set two percentages. The first one is the upper limit, and the second one is the lower limit. When the battery charge is above the upper limit, the computer will stop charging. When the battery charge is below the lower limit, the computer will start charging. If the battery charge is between the two limits, the computer will keep whatever charging state it is in.

Native mode uses only the upper target and ignores any saved lower-limit delta. The following applies to legacy, firmware-managed and adapter modes.

`batt` has similar features built-in. The charge limit you have set (using `batt limit`) will be used as the upper limit. By default, the lower limit will be set to 2% less than the upper limit. To customize the lower limit, use `batt lower-limit-delta`.

For example, if you want to set the lower limit to be 5% less than the upper limit, run `sudo batt lower-limit-delta 5`. So, if you have your charge (upper) limit set to 60%, the lower limit will be 55%.

### Control MagSafe LED

> Acknowledgement: [@exidler](https://github.com/exidler)

This option makes the MagSafe LED reflect the charging state of your MacBook (or force it off).

- Green: charging is stopped.
- Orange: charging is in progress.
- Off: forced off, or temporarily after wake with active charging control.

Note that you must have a MagSafe LED on your MacBook to use this feature.

With firmware-managed charging, `batt` uses the charging state reported by macOS. While the Mac is asleep, the LED is updated after it wakes. Legacy direct charging and adapter modes continue to use the charging state controlled by `batt`; the native macOS limit does not support LED control.

To enable MagSafe LED control, run `sudo batt magsafe-led enable`.

To force the MagSafe LED to stay off, run `sudo batt magsafe-led always-off`.

### Check logs

Logs are directed to `/tmp/batt.log`, or `/var/log/batt.log` if installed v0.7.5+ using Homebrew. If something goes wrong, you can check the logs to see what happened. Raise an issue with the logs attached.
