#pragma once

// Bridge to PowerUI.framework's PowerUISmartChargeClient, which talks to
// PowerUIAgent (com.apple.powerui.smartChargeManager) over XPC. This is the
// same path System Settings uses for the built-in charge limit. The framework
// is private, so it is loaded at runtime and called through the ObjC runtime
// instead of linking against headers.
//
// Every function returns 0 on success and -1 on failure. On failure *err is
// set to a malloc'd description that the caller must free().
// A policy write can also return one of these states after saving:
#define BATT_POWERUI_RESTART_DEFERRED (-2)
#define BATT_POWERUI_RELOAD_PENDING (-3)

int batt_powerui_supported(void);
int batt_powerui_available_limits(int *limits, int max, int *count, char **err);
int batt_powerui_get_limit(int *limit, int *enabled, char **err);
int batt_powerui_set_limit(int limit, char **err);
int batt_powerui_disable(char **err);

// macOS 27's slider setter clamps targets below 80%. The agent's root-owned
// preferences accept the actual target, followed by its reload notification.
int batt_powerui_custom_limits(void);
int batt_powerui_get_preferences(int *limit, int *enabled, char **err);
int batt_powerui_enable(char **err);
int batt_powerui_write_preferences(int limit, char **err);

// Read powerd's full-charge override without changing charging policy.
int batt_powerui_full_charge_override(int *active, char **err);

// The no-drain ceiling is powerd's persistent charge policy, rather than
// PowerUIAgent's manual limit. It is available only to the root daemon on
// macOS 27. Setting it may restart powerd; callers must check for foreign
// sleep assertions first. A limit of 100 removes batt's policy. Reading and
// writing preserve every other policy and reject unknown archive fields.
int batt_powerui_get_policy(int *limit, int *enabled, char **err);
int batt_powerui_set_policy(int limit, char **err);

// Reload a previously saved policy. The assertion check is repeated here,
// immediately before powerd is signalled, including for cleanup and retries.
int batt_powerui_reload_policies(char **err);
