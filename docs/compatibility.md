## Compatibility Matrix

This table is for master branch, for release version, please check the [release page](https://github.com/charlie0129/batt/releases).

Check your firmware version by running `system_profiler SPHardwareDataType | grep -i firmware` in Terminal. Do NOT rely on macOS version.

| Firmware Version          | First macOS release  | GUI | CLI (Prebuilt) | CLI (Build from Source) | Note                               |
| ------------------------- | -------------------- | --- | -------------- | ----------------------- | ---------------------------------- |
| `6723.x.x`                | macOS 11 Big Sur     | ❌   | ❌              | ⚠️                       |                                    |
| `7429.x.x` / `7459.x.x`   | macOS 12 Monterey    | ❌   | ⚠️              | ✅                       |                                    |
| `8419.x.x` / `8422.x.x`   | macOS 13 Ventura     | ⚠️   | ⚠️              | ✅                       |                                    |
| `101xx.x.x`               | macOS 14 Sonoma      | ⚠️   | ⚠️              | ✅                       |                                    |
| `118xx.x.x`               | macOS 15 Sequoia     | ✅   | ✅              | ✅                       |                                    |
| `138xx.x.x` / `18xxx.x.x` | macOS 26 Tahoe       | ✅   | ✅              | ✅                       |                                    |
| `20356.0.0.0.15` ~ `20457.0.77.0.2`               | macOS 27 Golden Gate Developer Beta 1~3 | ✅   | ✅              | ✅                       | Firmware-managed limits; see below |
| `20457.0.125.0.2`+ | macOS 27 Golden Gate Developer Beta 4+ | ✅   | ✅     | ✅       | On macOS 27, **requires** the native or opt-in adapter backend. macOS 15.8 keeps firmware-managed limits. See below |
| Other                     | Unknown              | ❓   | ❓              | ❓                       |                                    |

- ❌: Unsupported
- ✅: Supported
- ⚠️: Not verified, but expected to work.
- ❓: Unknown, please raise an issue if you have tested it.

!!! note
    Firmware version is different from macOS version. Old macOS versions may have newer firmware, e.g. macOS 15 with firmware `138xx.x.x` (macOS Tahoe version).

`batt` probes usable SMC keys or PowerUIAgent ( macOS system service ) at runtime, including when an older macOS installation receives newer firmware. Exact native targets require macOS 27.

If you want to know which MacBooks I personally developed it on, I am using it on all my personal MacBooks every single day, including MacBook Air M1 2020 (A2337), MacBook Air M2 2022 (A2681), MacBook Pro 14' M1 Pro 2021 (A2442), MacBook Pro 16' M1 Max 2021 (A2485).

If you encounter any incompatibility, please raise an issue with your MacBook model, firmware version, batt version, macOS version, and batt logs (`/tmp/batt.log`, or `/var/log/batt.log` if installed v0.7.5+ using Homebrew).

### macOS 27 / 20xxx firmware behavior

On firmware exposing `bfF0`, `batt` writes the upper and lower percentages to `bfD0` and `bfE0`, activates the limit, and periodically verifies that all three values are still correct. Apple firmware then decides when charging starts and stops. The limit continues to work while macOS and the `batt` daemon are asleep, so `batt` does not register sleep notifications or run pre-sleep/post-wake charging hooks in this mode.

This mode has several intentional differences and current limitations:

- If the battery is above the upper limit, firmware can stop drawing wall power and use the battery to run the Mac. Unlike legacy control, the percentage may therefore fall.
- MagSafe LED control follows the actual charging state reported by macOS. Green means the battery is not charging, which can happen for reasons other than reaching the limit.
- Legacy sleep options are unavailable and unnecessary. Incompatible values left in the configuration after an upgrade are disabled by the daemon.

The daemon reports these capabilities to current CLI and GUI clients. Older daemons that do not provide detailed compatibility data retain the previous permissive client behavior.

### macOS 27 firmware with gated charge keys

Starting with macOS 27 Developer Beta 4 (firmware `20457.0.125.0.2`+), Apple gated the SMC charge-control keys behind a private entitlement (`com.apple.private.iokit.soc-limit`). Both the legacy keys (`CH0B`/`CH0C`/`CHTE`) and the firmware-limit keys (`bfF0`/`bfD0`/`bfE0`) now return `kIOReturnNotPrivileged` even to root, so neither the legacy nor the firmware backend works. See [#152](https://github.com/charlie0129/batt/issues/152).

macOS 15.8 updates bring the same firmware, but their SMC firmware-limit keys stay usable, so `batt` v0.7.4 and later use the firmware-managed mode above there ([#160](https://github.com/charlie0129/batt/issues/160), [#162](https://github.com/charlie0129/batt/issues/162)). macOS 26.7 most likely behaves the same, but has not been verified.

`batt` automatically uses the native macOS charge limit through PowerUIAgent when direct SMC control is unavailable. On macOS 27, `sudo batt limit 55` requests 55%, bypassing the slider's 80% minimum. Targets from 10–99% are supported; 100% releases the limit. Older native backends use the values offered by macOS.

Exact-target native mode supports `lower-limit-delta` when the configured band is wider than 5% and no-drain is disabled. While holding, batt moves the native target to the current charge; below the lower limit it restores the upper target until charging completes. Narrower bands and older native backends keep a single upper target. macOS retains the last target during sleep, but daemon-driven threshold transitions may wait until wake. Legacy sleep options are unsupported. MagSafe LED control follows the actual charging state reported by macOS, as in firmware-managed mode, so the LED stays green while macOS drains the battery toward the limit. On macOS 27, `batt limit` waits briefly for macOS to register the new target (`pmset -g battlimit` lists it). While the battery is charging, macOS registers a new target only once the battery level changes or charging stops, usually within a few minutes; `batt limit` and `batt status` report such a pending limit. After about 5 minutes on battery power, macOS withdraws the limit (`pmset -g battlimit` lists none) and registers it again as soon as the Mac is connected to power. Either way, the battery can take a minute or more to start or stop charging. **If the battery is above the limit, macOS may drain it toward the target even while the Mac is plugged in.** macOS may also temporarily charge fully; `batt status` and the GUI report Apple's full-charge override when available.

For users who need the opposite behavior while above the limit, macOS 27 native mode also has an experimental opt-in **no-drain** setting (`sudo batt no-drain enable`). It installs batt's no-drain charge policy in `powerd` instead of relying solely on PowerUIAgent's draining policy. Changing that policy requires a `powerd` restart. `batt` defers the restart while another application holds a sleep or display-sleep assertion, keeps the regular native limit active where possible, and reports the deferred state in `batt status`. If the policy cannot be read or written safely, batt turns the opt-in off and restores regular native limiting. This is separate from adapter mode and does not add a lower limit.

As an alternative, `batt` offers an opt-in **adapter mode**, enabled at runtime with `sudo batt adapter-mode enable`. The adapter (wall-power) SMC key is not entitlement-gated, so `batt` runs the same ThinkPad-style hysteresis loop it uses in legacy mode, but toggles wall power instead of the charge state:

- When the battery reaches the upper limit, `batt` cuts wall power (the Mac runs from the battery, even though the adapter is physically plugged in), so the charge falls.
- When the battery drops to the lower limit, `batt` restores wall power and the Mac charges again.

This accepts any upper limit from 10 to 100 and supports the lower-limit delta, exactly like legacy mode.

Intentional differences and limitations in adapter mode:

- The limit is enforced only while `batt` is awake. Just before sleep `batt` restores/holds a safe state (charging is disabled pre-sleep by default) to avoid overcharging while asleep; the sleep options apply as in legacy mode.
- In Clamshell mode (lid closed with an external display), cutting wall power makes the Mac sleep unless `batt prevent-sleep-on-adapter-disable` is enabled. This opt-in setting also applies to adapter mode; it prevents all system sleep while wall power is cut. See [Prevent sleep when adapter is disabled](usage.md#prevent-sleep-when-adapter-is-disabled) for safety warnings.
- Because `batt` owns the adapter to enforce the limit, the manual `batt adapter` / GUI Force Discharge feature and auto-calibration are disabled in this mode.
- MagSafe LED control follows wall power: orange while `batt` lets the Mac charge, green while it holds the limit.
- Native control remains the default when charge keys are gated; adapter mode must be enabled explicitly.

`batt` selects the backend from usable SMC keys and services; exact native targets require macOS 27.