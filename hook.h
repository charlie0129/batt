#pragma once

extern void canSystemSleepCallback();
extern void systemWillSleepCallback();
extern void systemWillPowerOnCallback();
extern void systemHasPoweredOnCallback();

int AllowPowerChange();
int CancelPowerChange();
int ListenNotifications();
