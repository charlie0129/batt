# batt

`batt` is a tool to control battery charging on Apple Silicon MacBooks.

## Why do you need this?

[This article](https://batteryuniversity.com/article/bu-808-how-to-prolong-lithium-based-batteries) might be helpful. TL;DR: keep your battery at 80% or lower when plugged in, and discharge it as shallowly as feasible.

Previously, before optimized battery charging is introduced, MacBooks are known to suffer from battery swelling when they are kept at 100% all the time, especially the 2015s. Even with optimized battery charging, the effect is not optimal (described [below](#but-macos-have-similar-features-built-in-is-it)).

`batt` can effectively alleviate this problem by limiting the battery charge level. It can be used to set a maximum charge level. For example, you can set it to 80%, and it will stop charging when the battery reaches 80%.

## Features

`batt` tried to keep as simple as possible. Charging limiting is the only thing to care about for most users:

- Limit battery charge, with a lower and upper bound, like ThinkPads. [Docs](#limit-battery-charge)

However, if you are nerdy and want to dive into the details, it does have some advanced features for the computer nerds out there :)

- Control MagSafe LED (if present) according to charge status. [Docs](#control-magsafe-led)
- Cut power from the wall (even if the adapter is physically plugged in) to use battery power. [Docs](#enabledisable-power-adapter)
- It solves common sleep-related issues when controlling charging. [Docs1](#preventing-idle-sleep) [Docs2](#disabling-charging-before-sleep)

## How is it different from XXX?

**It is free and opensource**. It even comes with some features (like idle sleep preventions and pre-sleep stop charging) that are only available in paid counterparts. It comes with no ads, no tracking, no telemetry, no analytics, no bullshit. It is open source, so you can read the code and verify that it does what it says it does.

**It is simple but well-thought.** It only does charge limiting and does it well. For example, when using other free/unpaid tools, your MacBook will sometimes charge to 100% during sleep even if you set the limit to, like, 60%. `batt` have taken these edge cases into consideration and will behave as intended (in case you do encounter problems, please raise an issue so that we can solve it). Other features is intentionally limited to keep it simple. If you want some additional features, feel free to raise an issue, then we can discuss this.

**It is light-weight.** As a command-line tool, it is light-weight by design. No electron GUIs hogging your system resources. However, a native GUI that sits in the menubar is a good addition.

## But macOS have similar features built-in, is it?

Yes, macOS have optimized battery charging. It will try to find out your charging and working schedule and prohibits charging above 80% for a couple of hours overnight. However, if you have an un-regular schedule, this will simply not work. Also, you lose predictability (which I value a lot) about your computer's behavior, i.e., by letting macOS decide for you, you, the one who knows your schedule the best, cannot control when to charge or when not to charge.

`batt` can make sure your computer does exactly what you want. You can set a maximum charge level, and it will stop charging when the battery reaches that level. Therefore, it is recommended to disable macOS's optimized charging when using `batt`.

## Installation

> Currently, it is command-line only. Some knowledge of the command-line is required. A native GUI is possible but not planned. If you want to build a GUI, you can ask me to put a link here to your project 

1. (Optional) If you are lazy, there is an install script to help you get the first 3 steps done: `curl -fsSL https://github.com/charlie0129/batt/raw/master/hack/install.sh | bash`. After running this, you can skip to step 5.
2. Get the binary. You can download stable versions from [GitHub releases](https://github.com/charlie0129/batt/releases), extract the tar archive, and you will get a `batt` binary. If you want unstable versions with the latest features and bug fixes, you can download prebuilt binaries from [GitHub Actions](https://github.com/charlie0129/batt/actions/workflows/build-test-binary.yml) (has a retention period of 3 months) or [build it yourself](#building) .
3. Put the binary somewhere safe. You don't want to move it after installation :). It is recommended to save it in your `$PATH`, e.g., `/usr/local/bin`, so you can directly call `batt` on the command-line.
4. Install daemon using `sudo batt install`. This component is what actually controls charging. If you do not want to use `sudo` every time after installation, add the `--allow-non-root-access` flag: `sudo batt install --allow-non-root-access`. 
5. In case you have GateKeeper turned on, you will see something like _"batt is can't be opened because it was not downloaded from the App Store"_ or _"batt cannot be opened because the developer cannot be verified"_. To solve this, you can either 1. (recommended) Go to System Preferences -> Security & Privacy -> Open Anyway; or 2. run `sudo spctl --master-disable` to disable GateKeeper entirely.
6. Test if it works by running `sudo batt status`. If you see some status info, you are good to go!
7. Time to customize. By default `batt` will set a charge limit to 60%. For example, to set the charge limit to 80%, run `sudo batt limit 80`. 
8. As said before, it is highly recommended to disable macOS's optimized charging when using `batt`. To do so, open _System Preferences_, go to _Battery_, and uncheck _Optimized battery charging_.

Notes:

- Don't know what a command does? Run `batt help` to see all available commands. To see help for a specific command, run `batt help <command>`.
- If your current charge is above the limit, your computer will just stop charging and stays at your current charge level, which is by design. You can use your battery until it is below the limit to see the effects.
- To disable the charge limit, run `sudo batt limit 100`.

> Finally, if you find `batt` helpful, stars ⭐️ are much appreciated!

## Usage

### Limit battery charge

Make sure your computer doesn't charge beyond what you said.

Setting the limit to 10-99 will enable the battery charge limit, limiting the maximum charge to _somewhere around_ your setting. However, setting the limit to 100 will disable the battery charge limit. If you want to charge your MacBook to 100% or revert any changes, you can simply set the limit to 100%.

By default, `batt` will set a 60% charge limit.

To customize charge limit, see `batt limit`. For example,to set the limit to 80%, run `sudo batt limit 80`. To disable the limit, run `sudo batt limit 100`.

### Enable/disable power adapter

Cut or restore power from the wall. This has the same effect as unplugging/plugging the power adapter, even if the adapter is physically plugged in. 

This is useful when you want to use your battery to lower the battery charge, but you don't want to unplug the power adapter.

To enable/disable power adapter, see `batt adapter`. For example, to disable the power adapter, run `sudo batt adapter disable`. To enable the power adapter, run `sudo batt adapter enable`.

### Check status

Check the current config, battery status, and charging status.

To do so, run `sudo batt status`.

## Advanced

These advanced features are not for most users. Using the default setting for these options should work the best.

### Preventing idle sleep

Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, `batt` will pause when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, the battery charge limit will no longer function and the battery will charge to 100%. If you want the battery to stay below the charge limit, this behavior is probably not what you want. This option, together with disable-charging-pre-sleep, will prevent this from happening.

To prevent this, you can set `batt` to prevent idle sleep. This will prevent your computer from idle sleep while in a charging session. This will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is over.

However, this does not prevent manual sleep. For example, if you manually put your computer to sleep or close the lid, `batt` will not prevent your computer from sleeping. This is a limitation of macOS. To prevent such cases, see disable-charging-pre-sleep.

To enable this feature, run `sudo batt prevent-idle-sleep enable`. To disable, run `sudo batt prevent-idle-sleep disable`.

### Disabling charging before sleep

Set whether to disable charging before sleep if charge limit is enabled.

Due to macOS limitations, `batt` will pause when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, the battery charge limit will no longer function and the battery will charge to 100%. If you want the battery to stay below the charge limit, this behavior is probably not what you want. This option, together with prevent-idle-sleep, will prevent this from happening. prevent-idle-sleep can prevent idle sleep to keep the battery charge limit active. However, this does not prevent manual sleep. For example, if you manually put your computer to sleep or close the lid, batt will not prevent your computer from sleeping. This is a limitation of macOS.

To prevent such cases, you can use disable-charging-pre-sleep. This will disable charging just before your computer goes to sleep, preventing it from charging beyond the predefined limit. Once it wakes up, `batt` can take over and continue to do the rest work. It will only disable charging before sleep if battery charge limit is enabled.

To enable this feature, run `sudo batt disable-charging-pre-sleep enable`. To disable, run `sudo batt disable-charging-pre-sleep disable`.

### Upper and lower charge limit

When you set a charge limit, for example, on a Lenovo ThinkPad, you can set two percentages. The first one is the upper limit, and the second one is the lower limit. When the battery charge is above the upper limit, the computer will stop charging. When the battery charge is below the lower limit, the computer will start charging. If the battery charge is between the two limits, the computer will keep whatever charging state it is in.

`batt` have similar features built-in (since `v0.1.0`). The charge limit you have set (using `batt limit`) will be used as the upper limit. By default, The lower limit will be set to 2% less than the upper limit. Same as using 'batt lower-limit-delta 2'. To customize the lower limit, use `batt lower-limit-delta`.

For example, if you want to set the lower limit to be 5% less than the upper limit, run `sudo batt lower-limit-delta 5`. So, if you have your charge (upper) limit set to 60%, the lower limit will be 55%.

### Control MagSafe LED

> Only available after (not including) `v0.1.0`. It is disabled by default.
>
> Acknowledgement: [@exidler](https://github.com/exidler)

This setting can make the MagSafe LED behave like a normal device, i.e., it will turn green when charge limit is reached (not charging). By default, on a MagSafe-compatible device, the MagSafe LED will always be orange (charging) even if charge limit is reached and charging is disabled by batt, due to Apple's limitations. You cannot enable this feature on a non-MagSafe-compatible device.

One thing to note: this option is purely cosmetic. batt will still function even if you disable this option.

To enable MagSafe LED control, run `sudo batt magsafe-led enable`.

### Check logs

Logs are directed to `/tmp/batt.log`. If something goes wrong, you can check the logs to see what happened. Or raise an issue with the logs attached, so we can debug together.

## Building

You need to install [Go](https://go.dev/doc/install) and command line developer tools (by running `xcode-select --install`).

Simply running `make` should build the binary into `./bin/batt`. You can then move it to your `$PATH`.

## Architecture

You can think of `batt` like `docker`. It has a daemon that runs in the background, and a client that communicates with the daemon. They communicate through unix domain socket as a way of IPC. The daemon does the actual heavy-lifting, and is responsible for controlling battery charging. The client is responsible for sending users' requirements to the daemon.

For example, when you run `sudo batt limit 80`, the client will send the requirement to the daemon, and the daemon will do its job to keep the charge limit to 80%.

## Motivation

I created this tool simply because I am not satisfied with existing tools 😐.

I have written and using similar utils (to limit charging) on Intel MacBooks for years. Just since recently, I got hands on an Apple Silicon MacBook (yes, it is 2023, 2 years later since it is introduced 😅 and I just got one). The old BCLM way to limit charging doesn't work anymore. I was looking for a tool to limit charging on M1 MacBooks.

I have tried some alternatives, both closed source and open source, but I kept none of them. Some paid alternatives' licensing options are just too limited 🤔, a bit bloated, require periodic Internet connection (hmm?) and are closed source. It doesn't seem a good option for me. Some open source alternatives just don't handle edge cases well and I encountered issues sometimes especially when sleeping (as of Apr 2023).

I want a _simple_ tool that does just one thing, and **does it well** -- limiting charging, just like the [Unix philosophy](https://en.wikipedia.org/wiki/Unix_philosophy). It seems I don't have any options but to develop by myself. So I spent a weekend developing this tool, so here we are! `batt` is here!

## FAQ

### How to uninstall?

1. Run `sudo batt uninstall` to remove the daemon.
2. Remove the config by `sudo rm /etc/batt.json`.
3. Remove the `batt` binary itself by `sudo rm $(where batt)`.

### How to upgrade?

Automatic:

```bash
curl -fsSL https://github.com/charlie0129/batt/raw/master/hack/install.sh | bash
```

Manual:

1. Run `sudo batt uninstall` to remove the old daemon.
2. Replace the old `batt` binary with the downloaded new one. `sudo cp ./batt $(where batt)`
3. Run `sudo batt install` to install the daemon again. Although most config is preserved, some security related config is intentionally reset during re-installation. For example, if you used `--allow-non-root-access` when installing previously, you will need to use it again.

### Why is it Apple Silicon only?

Simply because you don't need this on Intel :p. 

On Intel MacBooks, you can control battery charging in a much, much easier way, simply setting the `BCLM` key in Apple SMC to the limit you need, and you are all done. There are many tools available. For example, you can use [smc-command](https://github.com/hholtmann/smcFanControl/tree/master/smc-command) to set SMC keys. 

However, on Apple Silicon, the way how charging is controlled changed. There is no such key. Therefore, we have to use a much more complicated way to achieve the same goal, and handle more edge cases, hence `batt`.

### Why does my MacBook stop charging after I close the lid?

TL,DR; This is intended, and is the default behavior. It is described [here](#disabling-charging-before-sleep). You can turn this feature off by running `sudo batt disable-charging-pre-sleep disable` (not recommended, keep reading).

But it is suggested to keep the default behavior to make your charge limit work as intended. Why? Because when you close the lid, your MacBook will go into **forced sleep**, and `batt` will be paused by macOS. As a result, `batt` can no longer control battery charging. It will be whatever state it was before you close the lid. This is the problem. Let's say, if you close the lid when your MacBook is charging, since `batt` is paused by macOS, it will keep charging, ignoring the charge limit you have set. There is no way to prevent **forced sleep**. Therefore, the only way to solve this problem is to disable charging before sleep. This is what `batt` does. It will disable charging just before your MacBook goes to sleep, and re-enable it when it wakes up. This way, your Mac will not overcharge during sleep.

Not that you will encounter this **forced sleep** only if you, the user, forced the Mac to sleep, either by closing the lid or selecting the Sleep option in the Apple menu. If your Mac decide to sleep by itself, called **idle sleep**, e.g. when it is idle for a while, in this case, you will not experience this stop-charging-before-sleep situation.

So you suggested not turning of this feature. But _What if I MUST let my Mac charge during a **forced sleep** without turing off `disable-charging-pre-sleep`, even if it may charge beyond the charge limit?_ This is simple, just disable charge limit by setting it to 100% `sudo batt limit 100`. This way, when you DO want to enable charge limit again, `disable-charging-pre-sleep` will still be there to prevent overcharging. The rationale is: when you want to charge during a **forced sleep**, you actually want heavy use of your battery and don't want ANY charge limit at all, e.g. when you are on a long outside-event, and you want to charge your Mac when it is sitting in your bag, lid closed. Setting the charge limit to 100% is equivalent to disabling charge limit. Therefore, most `batt` features will be turned off and your Mac can charge as if `batt` is not installed.

### Why does it require root privilege?

It writes to SMC to control battery charging. This does changes to your hardware, and is a highly privileged operation. Therefore, it requires root privilege.

It is also possible to run it without `sudo`. But I decided not to, because I want to make sure only you, the superuser, can control your computer, and to prevent accidental misuse.

If you want to use the cli without sudo, e.g. `sudo batt limit 80`, you can install the daemon with `--allow-non-root-access` flag, i.e., `sudo batt install --allow-non-root-access`. This will allow non-root users to access the daemon. However, this is not recommended from a security perspective.

If you are concerned about security, you can check the source code [here](https://github.com/charlie0129/batt) to make sure it does not do anything funny.

### Why is it written in Go and C?

Since it is a hobby project, I want to balance effort and the final outcome. Go seems a good choice for me. However, C is required to register sleep and wake notifications using Apple's IOKit framework. Also, Go don't have any library to r/w SMC, so I have to write it myself ([charlie0129/gosmc](https://github.com/charlie0129/gosmc)). This part is also mainly written in C as it interacts with the hardware and uses OS capabilities. Thankfully, writing a library didn't slow down development too much.

### Why is there so many logs?

By default, `batt` daemon will have its log level set to `debug` for easy debugging. The `debug` logs are helpful when reporting problems since it contains useful information. So it is recommended to keep it as `debug`. You may find a lot of logs in `/tmp/batt.log` after you use your Mac for a few days. However, there is no need to worry about this. The logs will be cleaned by macOS on reboot. It will not grow indefinitely.

If you `believe you will not encou`nter any problem in the future and still want to set a higher log level, you can achieve this by editing `/Library/LaunchDaemons/cc.chlc.batt.plist` and then restart the daemon by loading and unloading the LaunchDaemon.

## Acknowledgements

- [actuallymentor/battery](https://github.com/actuallymentor/battery) for various SMC keys.
- [hholtmann/smcFanControl](https://github.com/hholtmann/smcFanControl) for its C code to read/write SMC, which inspires [charlie0129/gosmc](https://github.com/charlie0129/gosmc).
- Apple for its guide to register and unregister sleep and wake notifications.
- [@exidler](https://github.com/exidler) for building the MagSafe LED controlling logic.
