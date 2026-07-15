#ifndef BATT_GUI_NATIVE_H
#define BATT_GUI_NATIVE_H

#include <stdbool.h>
#include <stdint.h>

typedef void *BattMenuRef;

typedef enum {
    BattItemPowerFlow = 1,
    BattItemPowerSystem,
    BattItemPowerAdapter,
    BattItemPowerBattery,
    BattItemUpgrade,
    BattItemInstall,
    BattItemState,
    BattItemCurrentLimit,
    BattItemQuickLimits,
    BattItemLimit50,
    BattItemLimit60,
    BattItemLimit70,
    BattItemLimit80,
    BattItemLimit90,
    BattItemAdvanced,
    BattItemMagSafe,
    BattItemMagSafeEnabled,
    BattItemMagSafeDisabled,
    BattItemMagSafeAlwaysOff,
    BattItemPreventIdleSleep,
    BattItemDisableChargingPreSleep,
    BattItemPreventSystemSleep,
    BattItemForceDischarge,
    BattItemAutoCalibration,
    BattItemCalibrationStatus,
    BattItemCalibrationStart,
    BattItemCalibrationPause,
    BattItemCalibrationResume,
    BattItemCalibrationCancel,
    BattItemVersion,
    BattItemUninstall,
    BattItemDisableLimit,
    BattItemDisableLimitCountdown,
    BattItemDisableLimitIndefinitely,
    BattItemDisableLimit1Hour,
    BattItemDisableLimit2Hours,
    BattItemDisableLimit4Hours,
    BattItemDisableLimit8Hours,
    BattItemDisableLimit12Hours,
    BattItemDisableLimit24Hours,
    BattItemDisableLimit2Days,
    BattItemDisableLimit3Days,
    BattItemDisableLimit7Days,
    BattItemQuit,
} BattMenuItem;

typedef enum {
    BattConfirmationForceDischarge = 1,
    BattConfirmationStartCalibration,
} BattConfirmation;

BattMenuRef batt_menu_create(uintptr_t handle, const char *version);
void batt_menu_destroy(BattMenuRef menu);
void batt_app_run(void);
void batt_app_terminate(void);

void batt_menu_set_title(BattMenuRef menu, int item, const char *title);
void batt_menu_set_tooltip(BattMenuRef menu, int item, const char *tooltip);
void batt_menu_set_hidden(BattMenuRef menu, int item, bool hidden);
void batt_menu_set_enabled(BattMenuRef menu, int item, bool enabled);
void batt_menu_set_checked(BattMenuRef menu, int item, bool checked);
void batt_menu_set_status_icon(BattMenuRef menu, bool installed, bool capable, bool needs_upgrade);
void batt_menu_set_power(BattMenuRef menu, int item, const char *label, double value);

void batt_show_alert(const char *message, const char *body);
bool batt_show_confirmation(int confirmation);
void batt_show_notification(const char *title, const char *body);

bool batt_register_login_item(void);
bool batt_unregister_login_item(void);
bool batt_is_login_item_registered(void);

// Implemented in Go and called only from the native menu controller.
extern void battMenuWillOpen(uintptr_t handle);
extern void battMenuTimerFired(uintptr_t handle);
extern void battMenuAction(uintptr_t handle, int item, bool checked);

#endif
