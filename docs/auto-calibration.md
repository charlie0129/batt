# Auto Calibration - Design Document

Last updated: 2025-11-11
Owner branch: auto-calibration

## Goals

Provide a one-click "Auto Calibration" flow from the GUI that:
- Discharges the battery to a configurable threshold (default 15%).
- Charges to 100%.
- Holds at full charge for a configurable duration (default 120 minutes).
- Discharges back down to the previous charging limit after the hold completes.
- Restores the user’s original charging settings afterwards.

This feature is initiated from the GUI Advanced submenu and orchestrated by the daemon via new APIs. It relies on configuration values stored in the existing config file; there is no GUI for editing these values.

## Non-Goals / Constraints

- Do not interfere with sleep/idle behavior during the discharge phase. The computer should naturally discharge; no sleep assertions will be created.
- No automatic failure/retry strategies. If something goes wrong, the user can Cancel and restore the previous state.
- No new GUI controls for calibration parameters; they are edited manually in the config file.

## Configuration

New config values in `RawFileConfig` (JSON keys):
- `calibrationDischargeThreshold` (int) — default: 15
  - Battery percentage threshold to reach during the discharge phase.
- `calibrationHoldDurationMinutes` (int) — default: 120
  - Minutes to hold at 100% before restoring settings.

Behavior:
- If the keys are missing, defaults are used.
- The values are read once at StartCalibration.
- Threshold is clamped to [5, 95].
- Hold duration is clamped to [10, 1440] minutes.

Example snippet:
```json
{
  "limit": 80,
  "calibrationDischargeThreshold": 15,
  "calibrationHoldDurationMinutes": 120
}
```

## Daemon Design

### State Machine

Idle → DischargeToThreshold → ChargeToFull → HoldAfterFull → DischargeAfterHold → RestoreAndFinish → Idle

- Pause/Resume/Cancel supported; no automatic retries.

### Phase Semantics

- DischargeToThreshold
  - Goal: Reach < threshold (default 15%).
  - Method: Prefer `SetAdapter(false)` or `SetCharging(false)` to prevent charging. Do not create sleep assertions. If adapter control is unavailable, the flow can continue; the GUI may suggest unplugging the adapter for faster discharge.

- ChargeToFull
  - Temporarily set upper limit to 100 and enable charging. Wait until the battery reaches 100%.

- HoldAfterFull
  - After reaching 100%, hold for `calibrationHoldDurationMinutes` (default 120 minutes). Do not create sleep assertions.
  - If paused during Hold, the effective end time is extended by the paused duration (hold time does not count while paused).

- DischargeAfterHold
  - Goal: After holding at 100%, discharge back down to the previous upper limit (the user’s snapshot maintain limit before calibration). If that snapshot is invalid, fall back to the current configured upper limit.
  - Method: Disable charging to allow natural discharge. When charge percentage drops to the target (snapshot upper limit), proceed to RestoreAndFinish.

- RestoreAndFinish
  - Restore the user’s original configuration (upper limit, adapter/charging states, etc.), clear any temporary state, and transition to Idle.

### Persistence & Recovery

- Persist calibration state and original configuration snapshot to disk.
- On daemon restart, recover to a safe paused state; user can Resume or Cancel from the GUI.

### Public API (daemon)

Add RPC endpoints (names illustrative):
- `StartCalibration()` → `{ ok: true } | error`
  - Reads the threshold and duration from config.
- `GetCalibrationStatus()` →
  ```json
  {
    "phase": "Idle|DischargeToThreshold|ChargeToFull|HoldAfterFull|DischargeAfterHold|RestoreAndFinish|Error",
    "chargePercent": 72,
    "pluggedIn": true,
    "remainingHoldSeconds": 3600,
    "startedAt": "2025-11-08T03:21:00Z",
    "paused": false,
    "canPause": true,
    "canCancel": true,
    "message": "optional human-friendly hint",
    "targetPercent": 80
  }
  ```
  - `targetPercent` is present during `DischargeAfterHold` to indicate the numeric discharge target (typically the snapshot upper limit). It may be omitted in other phases.
- `PauseCalibration()` → `{ ok: true } | error`
- `ResumeCalibration()` → `{ ok: true } | error`
- `CancelCalibration()` → `{ ok: true } | error` (must restore snapshot)

### Safety

- If the battery is already below the threshold at start, skip to ChargeToFull.
- If adapter control is missing or charging cannot be toggled, proceed but surface hints to the GUI.
- No sleep prevention; clamshell/idle/sleep may slow the process naturally.

## GUI Design

### Menu Placement

- Advanced submenu, immediately after "Force Discharge…"
- Entry name: "Auto Calibration…" (ellipsis indicates a confirmation dialog)

### Interactions

- Clicking "Auto Calibration…" shows a confirmation dialog:
  - Explains the flow and the constraints (no sleep interference; needs to keep the Mac awake for faster completion, etc.).
  - Buttons: Start (default) / Cancel

- Once started, the menu entry changes to a statusful item, e.g.:
  - "Auto Calibration (In Progress)" / "Auto Calibration (Paused)"
  - Sub-items:
    - A disabled status line: current phase, battery %, remaining hold time (if applicable)
    - "Pause" / "Resume"
    - "Cancel" (restores original settings and stops)

### Status Updates

- Poll `GetCalibrationStatus()` every 10 seconds in the background.
- Also refresh immediately when the Advanced menu is opened.
- During `DischargeAfterHold`, the status line displays the numeric target, e.g., "Discharging 86% → 80%".
- On completion/cancellation/error: show a single user notification and revert the menu back to the idle entry.

### Error Handling

- Do not auto-retry. If any operation fails, present the error message and suggest "Cancel" to restore the previous state.

## Implementation Plan (high level)

1) Config
- Add fields to `RawFileConfig` and defaults in `defaultFileConfig`.
- Expose getters on `File` with default fallback.

2) Daemon
- Implement state machine, persistence, and new RPC handlers.
- Snapshot and restore original settings on Start/Cancel/Finish.

3) Client/GUI
- Add client methods for the new RPC endpoints.
- Insert the new menu item in `pkg/gui/cmd.go` after "Force Discharge…".
- Implement status polling and dynamic menu text.
- Confirmation dialog and minimal notifications.

## Open Questions (Resolved by product decision)

- Sleep/idle prevention during discharge: Not desired; do nothing.
- Failure auto-retry policy: None; user cancels manually.
- GUI editability of parameters: None; edit config file directly.
