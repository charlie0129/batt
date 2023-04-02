# batt

`batt` is a tool to control battery charging on Apple Silicon MacBooks.

## Why do I need this?

[This article](https://batteryuniversity.com/article/bu-808-how-to-prolong-lithium-based-batteries) might be helpful. TL;DR: keep your battery cool, keep it at 80% or lower when plugged in, and discharge it as shallowly as feasible.

`batt` does exactly that. It can be used to set a maximum charge level. For example, you can set it to 80% and it will stop charging when the battery reaches 80%.

## How is it different than XXX?

**It is free and opensource**. It even comes with some features (like idle sleep preventions and pre-sleep stop charging) that are only available in paid counterparts. It comes with no ads, no tracking, no telemetry, no analytics, no bullshit. It is open source, so you can read the code and verify that it does what it says it does.

**It is simple.** It only does charge limiting and does it well. For example, when using other free/unpaid tools, your MacBook will sometimes charge to 100% during sleep even if you set the limit to, like, 60%. `batt` will handle such edge cases correctly and behave as intended. Other features is intentionally limited to keep it simple. If you want some additional features, feel free to raise an issue, and we can discuss. 

**It is light-weight.** As a command-line tool, it is light-weight by design. No electron GUIs hogging your system resources. However, a native GUI that sits in the menubar is a good addition.

## But macOS have similar features

macOS have optimized battery charging. It will try to find out your charging and working schedule and prohibits charging above 80% for a couple of hours overnight. However, if you have an unregular schedule, this will simply not work. 

`batt` can make sure your computer does exactly what you want. You can set a maximum charge level, and it will stop charging when the battery reaches that level. Therefore, it is recommended to disable macOS's optimized charging when using `batt`.

## Installation

> Currently, it is command-line only. Some knowledge of the command-line is recommended. A native GUI is possible but not planned.

1. Get the binary. You can download from GitHub releases or build it yourself. See [Building](#building) for more details.
2. Put the binary somewhere safe. You don't want to move it after installation. It is recommended to save it in your `$PATH`. For example, `/usr/local/bin`.
3. Run `batt` to see if it works (shows help messages)
4. Install daemon. This is required to make `batt` work. Run `sudo batt install` to install the daemon. You can run `sudo batt uninstall` to uninstall the daemon if you want.
5. Test if it works by running `sudo ./batt status`. If you see some JSON config, you are good to go! 
6. Now, time to get started. Run `batt help` to see all available commands. To see help for a specific command, run `batt help <command>`. For example, `batt help limit`.

> As said before, it is recommended to disable macOS's optimized charging when using `batt`. To do so, open System Preferences, go to Battery, and uncheck "Optimize battery charging".

## Usage

### Limiting charge

Make sure your computer doesn't charge beyond what you said.

Setting the limit to 10-99 will enable the battery charge limit. However, setting the limit to 100 will disable the battery charge limit.

By default, `batt` will set a 60% charge limit.

To customize charge limit, see `batt limit`. For example,to set the limit to 80%, run `sudo batt limit 80`. To disable the limit, run `sudo batt limit 100`.

### Check current config

Check the current config. This is useful to see if the config is set correctly.

To check the current config, run `sudo batt status`.

## Advanced

These advanced features are not for most users. Using the default setting for these options should work the best.

### Preventing idle sleep

Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, `batt` will pause when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, the battery charge limit will no longer function and the battery will charge to 100%. If you want the battery to stay below the charge limit, this behavior is probably not what you want. This option, together with disable-charging-pre-sleep, will prevent this from happening.

To prevent this, you can set `batt` to prevent idle sleep. This will prevent your computer from idle sleep while in a charging session. This will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is over.

However, this does not prevent manual sleep. For example, if you manually put your computer to sleep or close the lid, `batt` will not prevent your computer from sleeping. This is a limitation of macOS. To prevent such cases, see disable-charging-pre-sleep.

To enable this feature, run `sudo batt idle-sleep enable`. To disable, run `sudo batt idle-sleep disable`.

### Disabling charging before sleep

Set whether to disable charging before sleep during a charging session.

Due to macOS limitations, `batt` will pause when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, the battery charge limit will no longer function and the battery will charge to 100%. If you want the battery to stay below the charge limit, this behavior is probably not what you want. This option, together with idle-sleep, will prevent this from happening. idle-sleep can prevent idle sleep to keep the battery charge limit active. However, this does not prevent manual sleep. For example, if you manually put your computer to sleep or close the lid, batt will not prevent your computer from sleeping. This is a limitation of macOS.

To prevent such cases, you can use disable-charging-pre-sleep. This will disable charging just before your computer goes to sleep, preventing it from charging beyond the predefined limit. Once it wakes up, `batt` can take over and continue to do the rest work. This will only disable charging before sleep, when 1) charging is active 2) battery charge limit is enabled.

To enable this feature, run `sudo batt disable-charging-pre-sleep enable`. To disable, run `sudo batt disable-charging-pre-sleep disable`.

### Check logs

Logs are directed to `/tmp/batt.log`. If something goes wrong, you can check the logs to see what happened. Or raise an issue with the logs attached, so we can debug together.

## Building

Simply running `make` should build the binary into `./bin/batt`. You can then move it to your `$PATH`.

Of course, Go and other tool chain is required. You can solve dependencies by reading error messages. It shoud be fairly straightforward because it is a simple Go project.

## FAQ

### Why is it Apple Silicon only?

Simply because you don't need this on Intel. On Intel, you can control battery charging in a much easier way, simply setting the `BCLM` key in Apple SMC to the limit you need. There are many tools available. For example, you can use [smc-command](https://github.com/hholtmann/smcFanControl/tree/master/smc-command) to set SMC keys. However, on Apple Silicon, there is no such key. Therefore, we have to use a more complicated way to achieve the same goal, hence `batt`.

### Why does it require root privilege?

It writes to SMC to control battery charging. This does changes to your hardware, and is a highly privileged operation. Therefore, it requires root privilege.

It is also possible to run it without `sudo`. But I decided not to, because I want to make sure only you, the superuser, can control your computer, and to prevent accidental misuse.

If you are concerned about security, you can check the source code [here](https://github.com/charlie0129/batt) to make sure it does not do anything funny.
