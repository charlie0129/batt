# batt

`batt` is a tool to control battery charging on Apple Silicon MacBooks.

## Motivation

I created this tool simply because I am not satisfied with existing tools 😐.

I have written and using similar utils (to limit charging) on Intel MacBooks for years. Just since recently, I got hands on a Apple Silicon MacBook (yes, it is 2023, 2 years later since it is introduced 😅 and I just got one). The old BCLM way to limit charging doesn't work anymore. I was looking for a tool to limit charging on M1 MacBooks.

I have tried some alternatives, both closed source and open source, but I kept none of them. Some paid alternatives' licensing options are just too limited 🤔, a bit bloated, require periodic Internet connection (hmm?) and are closed source. It doesn't seem a good option for me. Some open source alternatives just don't handle edge cases well and I encountered issues sometimes especially when sleeping (as of Apr 2023).

I want a _simple_ tool that does just one thing, and **does it well** -- limiting charging, just like the [Unix philosophy](https://en.wikipedia.org/wiki/Unix_philosophy). It seems I don't have any options but to develop by myself. So I spent a weekend developing this tool, so here we are! `batt` is here!

## Why do you need this?

[This article](https://batteryuniversity.com/article/bu-808-how-to-prolong-lithium-based-batteries) might be helpful. TL;DR: keep your battery cool, keep it at 80% or lower when plugged in, and discharge it as shallowly as feasible.

`batt` does exactly that. It can be used to set a maximum charge level. For example, you can set it to 80% and it will stop charging when the battery reaches 80%.

## How is it different from XXX?

**It is free and opensource**. It even comes with some features (like idle sleep preventions and pre-sleep stop charging) that are only available in paid counterparts. It comes with no ads, no tracking, no telemetry, no analytics, no bullshit. It is open source, so you can read the code and verify that it does what it says it does.

**It is simple.** It only does charge limiting and does it well. For example, when using other free/unpaid tools, your MacBook will sometimes charge to 100% during sleep even if you set the limit to, like, 60%. `batt` have taken these edge cases into consideration and will behave as intended. Other features is intentionally limited to keep it simple. If you want some additional features, feel free to raise an issue, then we can discuss about it. 

**It is light-weight.** As a command-line tool, it is light-weight by design. No electron GUIs hogging your system resources. However, a native GUI that sits in the menubar is a good addition.

## But macOS have similar features built-in

Yes, macOS have optimized battery charging. It will try to find out your charging and working schedule and prohibits charging above 80% for a couple of hours overnight. However, if you have an un-regular schedule, this will simply not work. Also, you lose predictability (which I value a lot) about your computer's behavior. By letting macOS decide for you, you cannot control when to charge or when not to charge.

`batt` can make sure your computer does exactly what you want. You can set a maximum charge level, and it will stop charging when the battery reaches that level. Therefore, it is recommended to disable macOS's optimized charging when using `batt`.

## Installation

> Currently, it is command-line only. Some knowledge of the command-line is recommended. A native GUI is possible but not planned. If you want to build a GUI, you can ask me to put a link here to your project 

1. Get the binary. You can download from GitHub releases or build it yourself. See [Building](#Building) for more details.
2. Put the binary somewhere safe. You don't want to move it after installation. It is recommended to save it in your `$PATH`. For example, `/usr/local/bin`.
3. Run `batt` in terminal to see if it works. If it works, it will show help messages.
4. Install daemon. This component is what actually controls charging. Run `sudo batt install` to install the daemon. If you do not want to use `sudo` every time, e.g., when setting charge limits, add the `--allow-non-root-access` flag (but you will sacrifice security for convenience). To uninstall the daemon, run `sudo batt uninstall`.
5. Test if it works by running `sudo batt status`. If you see some JSON config, you are good to go! 
6. `batt` is now running! By default `batt` will set a charge limit to 60%.
7. Time to customize it a little. For example, to set the charge limit to 80%, run `sudo batt limit 80`.
8. As said before, it is recommended to disable macOS's optimized charging when using `batt`. To do so, open System Preferences, go to Battery, and uncheck "Optimize battery charging".

Notes:

- If your current charge is above the limit, your computer will just stop charging. To see any effect, you will need to use your battery until it is below the limit. You can use `sudo batt adapter disable` to force the computer to use battery even if it is plugged in.
- To disable the charge limit, run `sudo batt limit 100`.
- Don't know what a command does? Run `batt help` to see all available commands. To see help for a specific command, run `batt help <command>`.

> Finally, if you find `batt` helpful, stars ⭐️ are much appreciated!

## Usage

### Limiting charge

Make sure your computer doesn't charge beyond what you said.

Setting the limit to 10-99 will enable the battery charge limit, limiting the maximum charge to _somewhere around_ your setting. However, setting the limit to 100 will disable the battery charge limit. If you want to charge your MacBook to 100% or revert any changes, you can simply set the limit to 100%.

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

To enable this feature, run `sudo batt prevent-idle-sleep enable`. To disable, run `sudo batt prevent-idle-sleep disable`.

### Disabling charging before sleep

Set whether to disable charging before sleep during a charging session.

Due to macOS limitations, `batt` will pause when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, the battery charge limit will no longer function and the battery will charge to 100%. If you want the battery to stay below the charge limit, this behavior is probably not what you want. This option, together with prevent-idle-sleep, will prevent this from happening. prevent-idle-sleep can prevent idle sleep to keep the battery charge limit active. However, this does not prevent manual sleep. For example, if you manually put your computer to sleep or close the lid, batt will not prevent your computer from sleeping. This is a limitation of macOS.

To prevent such cases, you can use disable-charging-pre-sleep. This will disable charging just before your computer goes to sleep, preventing it from charging beyond the predefined limit. Once it wakes up, `batt` can take over and continue to do the rest work. This will only disable charging before sleep, when 1) charging is active 2) battery charge limit is enabled.

To enable this feature, run `sudo batt disable-charging-pre-sleep enable`. To disable, run `sudo batt disable-charging-pre-sleep disable`.

### Check logs

Logs are directed to `/tmp/batt.log`. If something goes wrong, you can check the logs to see what happened. Or raise an issue with the logs attached, so we can debug together.

## Building

Simply running `make` should build the binary into `./bin/batt`. You can then move it to your `$PATH`.

Of course, Go and other tool chain is required. You can solve dependencies by reading error messages. It doesn't have complicated dependencies, so it should be fairly straightforward.

## Architecture

You can think of `batt` like `docker`. It has a daemon that runs in the background, and a client that communicates with the daemon. They communicate through unix domain socket as a way of IPC. The daemon does the actual heavy-lifting, and is responsible for controlling battery charging. The client is responsible for sending users' requirements to the daemon.

For example, when you run `sudo batt limit 80`, the client will send the requirement to the daemon, and the daemon will do its job to keep the charge limit to 80%.

## FAQ

### Why is it Apple Silicon only?

Simply because you don't need this on Intel :p. 

On Intel MacBooks, you can control battery charging in a much, much easier way, simply setting the `BCLM` key in Apple SMC to the limit you need, and you are all done. There are many tools available. For example, you can use [smc-command](https://github.com/hholtmann/smcFanControl/tree/master/smc-command) to set SMC keys. 

However, on Apple Silicon, the way how charging is controlled changed. There is no such key. Therefore, we have to use a much more complicated way to achieve the same goal, and handle more edge cases, hence `batt`.

### Why does it require root privilege?

It writes to SMC to control battery charging. This does changes to your hardware, and is a highly privileged operation. Therefore, it requires root privilege.

It is also possible to run it without `sudo`. But I decided not to, because I want to make sure only you, the superuser, can control your computer, and to prevent accidental misuse.

If you want to use the cli without sudo, e.g. `sudo batt limit 80`, you can install the daemon with `--allow-non-root-access` flag, i.e., `sudo batt install --allow-non-root-access`. This will allow non-root users to access the daemon. However, this is not recommended from a security perspective.

If you are concerned about security, you can check the source code [here](https://github.com/charlie0129/batt) to make sure it does not do anything funny.

### Why is it written in Go and C?

Since it is a hobby project, I want to balance effort and the final outcome. Go seems a good choice for me. However, C is required to register sleep and wake notifications using Apple's IOKit framework. Also, Go don't have any library to r/w SMC, so I have to write it myself ([charlie0129/gosmc](https://github.com/charlie0129/gosmc)). This part is also mainly written in C as it interacts with the hardware and uses OS capabilities. Thankfully, writing a library didn't slow down development too much.

## Acknowledgements

- [actuallymentor/battery](https://github.com/actuallymentor/battery) for various SMC keys.
- [hholtmann/smcFanControl](https://github.com/hholtmann/smcFanControl) for its C code to read/write SMC, which inspires [charlie0129/gosmc](https://github.com/charlie0129/gosmc).
- Apple for its guide to register and unregister sleep and wake notifications.
