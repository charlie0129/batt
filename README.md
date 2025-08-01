Note: Use table of contents of quickly navigate to the section you want, e.g., `Installation`. 👆↗

# batt

[![Go Checks](https://github.com/charlie0129/batt/actions/workflows/gochecks.yml/badge.svg)](https://github.com/charlie0129/batt/actions/workflows/gochecks.yml)[![Buind Test Binary](https://github.com/charlie0129/batt/actions/workflows/build-test-binary.yml/badge.svg)](https://github.com/charlie0129/batt/actions/workflows/build-test-binary.yml)

`batt` is a tool to control battery charging on Apple Silicon MacBooks.

## Why do you need this?

[This article](https://batteryuniversity.com/article/bu-808-how-to-prolong-lithium-based-batteries) might be helpful. TL;DR: keep your battery at 80% or lower when plugged in, and discharge it as shallowly as feasible.

Previously, before optimized battery charging is introduced, MacBooks are known to suffer from battery swelling when they are kept at 100% all the time, especially the 2015s. Even with optimized battery charging, the effect is not optimal (described [below](#but-macos-have-similar-features-built-in-is-it)).

`batt` can effectively alleviate this problem by limiting the battery charge level. It can be used to set a maximum charge level. For example, you can set it to 80%, and it will stop charging when the battery reaches 80%. Once it reaches the predefined level, your computer will use power from the wall _only_, leaving no strain on your battery.

Quick link to [installation guide](#installation).

## Features

`batt` tried to keep as simple as possible. Charging limiting is the only thing to care about for most users:

- Limit battery charge, with a lower and upper bound, like ThinkPads. [Docs](#limit-battery-charge)

However, if you are nerdy and want to dive into the details, it does have some advanced features for the computer nerds out there :)

- Control MagSafe LED (if present) according to charge status. [Docs](#control-magsafe-led)
- Cut power from the wall (even if the adapter is physically plugged in) to use battery power. [Docs](#enabledisable-power-adapter)
- It solves common sleep-related issues when controlling charging. [Docs1](#preventing-idle-sleep) [Docs2](#disabling-charging-before-sleep)

## How is it different from XXX?

**It is free and opensource**. It even comes with some features (like idle sleep preventions and pre-sleep stop charging) that are only available in paid counterparts. It comes with no ads, no tracking, no telemetry, no analytics, no bullshit. It is open source, so you can read the code and verify that it does what it says it does.

**It is simple but well-thought.** It only does charge limiting and does it well. For example, when using other free/unpaid tools, your MacBook will sometimes charge to 100% during sleep even if you set the limit to, like, 60%. `batt` have taken these edge cases into consideration and will behave as intended (in case you do encounter problems, please raise an issue so that we can solve it). Other features is intentionally limited to keep it simple. If you want some additional features, feel free to raise an issue, then we can discuss.

**It is light-weight.** As a command-line tool, it is light-weight by design. No electron GUIs hogging your system resources. However, a native GUI that sits in the menubar is a good addition.

## But macOS have similar features built-in, is it?

Yes, macOS have optimized battery charging. It will try to find out your charging and working schedule and prohibits charging above 80% for a couple of hours overnight. However, if you have an un-regular schedule, this will simply not work. Also, you lose predictability (which I value a lot) about your computer's behavior, i.e., by letting macOS decide for you, you, the one who knows your schedule the best, cannot control when to charge or when not to charge.

`batt` can make sure your computer does exactly what you want. You can set a maximum charge level, and it will stop charging when the battery reaches that level. Therefore, it is recommended to disable macOS's optimized charging when using `batt`.

## Compatibility Matrix

| Firmware Version        | GUI | CLI (Prebuilt) | CLI (Build from Source) |
|-------------------------|-----| -------------- | ----------------------- |
| `6723.x.x`              | ❌   | ❌              | ✅                       |
| `7429.x.x` / `7459.x.x` | ❌   | ✅              | ✅                       |
| `8419.x.x` / `8422.x.x` | ✅   | ✅              | ✅                       |
| `10151.x.x`             | ✅   | ✅              | ✅                       |
| `11881.x.x`             | ✅   | ✅              | ✅                       |
| `13822+`                | ⚠️  | ⚠️              | ⚠️                       |

- ❌: Unsupported
- ✅: Supported
- ⚠️: Partially supported, more tests are needed to verify the compatibility. Read [#34](https://github.com/charlie0129/batt/issues/34) for details.

> [!NOTE]
> Firmware version is different from macOS version. You can check your firmware version by running `system_profiler SPHardwareDataType | grep -i firmware` in Terminal.

If you want to know which MacBooks I personally developed it on, I am using it on all my personal MacBooks every single day, including MacBook Air M1 2020 (A2337), MacBook Air M2 2022 (A2681), MacBook Pro 14' M1 Pro 2021 (A2442), MacBook Pro 16' M1 Max 2021 (A2485).

If you encounter any incompatibility, please raise an issue with your MacBook model and macOS version.

## Installation (GUI Version)

GUI version is a native macOS menubar app. It's not as feature-complete as the command-line version, but it is a good choice if you are not comfortable with the command-line. The command-line version is also included if you have the GUI version.

1. Download `.dmg` file from [Releases](https://github.com/charlie0129/batt/releases) and open it (right-click open if macOS says it's damaged)
2. Drag `batt.app` to `Applications`
3. macOS may say it's damaged when you try to run it (it's NOT) and wants you to move it to trash. To fix it, run this in Terminal: `sudo xattr -r -d com.apple.quarantine /Applications/batt.app`.
4. Run `batt.app`. 
5. Follow the MenuBar UI to install or upgrade.
6. It is _highly_ recommended to disable macOS's optimized charging when using `batt`. To do so, open `System Settings` -> `Battery` -> `Battery Health` -> `i` -> Trun OFF `Optimized Battery Charging`

<img width="191" alt="SCR-20250624-lbmb-3" src="https://github.com/user-attachments/assets/4bef52d7-8483-49bd-b579-736b87c81a52" />
<img width="206" alt="SCR-20250624-lbmb-2" src="https://github.com/user-attachments/assets/f3731a00-d973-4d67-8b4a-d57595e3842f" />
<img width="450" alt="SCR-20250624-lbmb-4" src="https://github.com/user-attachments/assets/a9d3c2eb-5d41-400e-b042-b79f6e8decd0" />

> [!TIP]
> There are 3rd-party GUI versions built around `batt` by some amazing opensource developers:
> 1. [BattGUI](https://github.com/clzoc/BattGUI) by [@clzoc](https://github.com/clzoc)


## Installation (Command-Line Version)

> [!NOTE]
> Command-Line version is already included if you have installed the GUI version. You can run `batt` in Terminal to use it.

You have two choices to install the CLI version of `batt`:

1. Homebrew (If you prefer a package manager) [Docs](#homebrew)
2. Installation Script (Recommended) [Docs](#installation-script)

You can choose either one. Please do not use both at the same time to avoid conflicts.

### Homebrew

1. `brew install batt`
2. `sudo brew services start batt`
3. Please read [Notes](#notes).

> Thank you, [@Jerry1144](https://github.com/charlie0129/batt/issues/9#issuecomment-2165493285), for bootstrapping the Homebrew formula.

### Installation Script

1. (Optional) There is an installation script to help you quickly install batt (Internet connection required). Put this in your terminal: `bash <(curl -fsSL https://github.com/charlie0129/batt/raw/master/hack/install.sh)`. You may need to provide your login password (to control charging). This will download and install the latest _stable_ version for you. Follow the on-screen instructions, then you can skip to step 5.
<details>
<summary>Manual installation steps</summary>
    
2. Get the binary. For _stable_ and _beta_ releases, you can find the download link in the [release page](https://github.com/charlie0129/batt/releases). If you want development versions with the latest features and bug fixes, you can download prebuilt binaries from [GitHub Actions](https://github.com/charlie0129/batt/actions/workflows/build-test-binary.yml) (has a retention period of 3 months and you need to `chmod +x batt` after extracting the archive) or [build it yourself](#building) .
3. Put the binary somewhere safe. You don't want to move it after installation :). It is recommended to save it in your `$PATH`, e.g., `/usr/local/bin`, so you can directly call `batt` on the command-line. In this case, the binary location will be `/usr/local/bin/batt`.
4. Install daemon using `sudo batt install`. If you do not want to use `sudo` every time after installation, add the `--allow-non-root-access` flag: `sudo batt install --allow-non-root-access`. To uninstall: please refer to [How to uninstall?](#how-to-uninstall)
</details>

5. In case you have GateKeeper turned on, you will see something like _"batt is can't be opened because it was not downloaded from the App Store"_ or _"batt cannot be opened because the developer cannot be verified"_. If you don't see it, you can skip this step. To solve this, you can either 1. (recommended) Go to *System Settings* -> *Privacy & Security* --scroll-down--> *Security* -> *Open Anyway*; or 2. run `sudo spctl --master-disable` to disable GateKeeper entirely.

### Notes

- Test if it works by running `sudo batt status`. If you see your battery status, you are good to go!
- Time to customize. By default `batt` will set a charge limit to 60%. For example, to set the charge limit to 80%, run `sudo batt limit 80`.
- As said before, it is _highly_ recommended to disable macOS's optimized charging when using `batt`. To do so, open `System Settings` -> `Battery` -> `Battery Health` -> `i` -> Trun OFF `Optimized Battery Charging`
- If your current charge is above the limit, your computer will just stop charging and use power from the wall. It will stay at your current charge level, which is by design. You can use your battery until it is below the limit to see the effects.
- You can refer to [Usage](#usage) for additional configurations. Don't know what a command does? Run `batt help` to see all available commands. To see help for a specific command, run `batt help <command>`.
- To disable the charge limit, run `batt disable` or `batt limit 100`.
- [How to uninstall?](#how-to-uninstall) [How to upgrade?](#how-to-upgrade)

> Finally, if you find `batt` helpful, stars ⭐️ are much appreciated!

## Usage

### Limit battery charge

Make sure your computer doesn't charge beyond what you said.

Setting the limit to 10-99 will enable the battery charge limit, limiting the maximum charge to _somewhere around_ your setting. However, setting the limit to 100 will disable the battery charge limit.

By default, `batt` will set a 60% charge limit.

To customize charge limit, see `batt limit`. For example,to set the limit to 80%, run `batt limit 80`. To disable the limit, run `batt disable` or `batt limit 100`.

### Enable/disable power adapter

> [!NOTE]  
> This feature is CLI-only and is not available in the GUI version.

Cut or restore power from the wall. This has the same effect as unplugging/plugging the power adapter, even if the adapter is physically plugged in. 

This is useful when you want to use your battery to lower the battery charge, but you don't want to unplug the power adapter.

NOTE: if you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *cutting power will cause your Mac to go to sleep*. This is a limitation of macOS. There are ways to prevent this, but it is not recommended for most users.

To enable/disable power adapter, see `batt adapter`. For example, to disable the power adapter, run `sudo batt adapter disable`. To enable the power adapter, run `sudo batt adapter enable`.

### Check status

> [!NOTE]  
> This feature is CLI-only and is not available in the GUI version.

Check the current config, battery info, and charging status.

To do so, run `sudo batt status`.

## Advanced

These advanced features are not for most users. Using the default setting for these options should work the best.

### Preventing idle sleep

Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, `batt` will be paused when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, there is no way for batt to stop charging (since batt is paused by macOS) and the battery will charge to 100%. This option, together with disable-charging-pre-sleep, will prevent this from happening.

This option tells macOS NOT to go to sleep when the computer is in a charging session, so batt can continue to work until charging is finished. Note that it will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is completed.

However, this options does not prevent manual sleep (limitation of macOS). For example, if you manually put your computer to sleep (by choosing the Sleep option in the top-left Apple menu) or close the lid, batt will still be paused and the issue mentioned above will still happen. This is where disable-charging-pre-sleep comes in. See [Disabling charging before sleep](#disabling-charging-before-sleep).

To enable this feature, run `sudo batt prevent-idle-sleep enable`. To disable, run `sudo batt prevent-idle-sleep disable`.

### Disabling charging before sleep

Set whether to disable charging before sleep if charge limit is enabled.

As described in [Preventing idle sleep](#preventing-idle-sleep), batt will be paused by macOS when your computer goes to sleep, and there is no way for batt to continue controlling battery charging. This option will disable charging just before sleep, so your computer will not overcharge during sleep, even if the battery charge is below the limit.

To enable this feature, run `sudo batt disable-charging-pre-sleep enable`. To disable, run `sudo batt disable-charging-pre-sleep disable`.

### Prevent system sleep

Set whether to prevent system sleep during a charging session (experimental).

This option tells macOS to create power assertion, which prevents sleep, when all conditions are met:

1) charging is active
2) battery charge limit is enabled
3) computer is connected to charger.

So your computer can go to sleep as soon as a charging session is completed / charger disconnected.

Does similar thing to [Preventing idle sleep](#preventing-idle-sleep), but works for manual sleep too.

*Note*: Please disable [Preventing idle sleep](#preventing-idle-sleep) and [Disabling charging before sleep](#disabling-charging-before-sleep), while this feature is in use.

To enable this feature, run `sudo batt prevent-system-sleep enable`. To disable, run `sudo batt prevent-system-sleep disable`.

### Upper and lower charge limit

> [!NOTE]  
> This feature is CLI-only and is not available in the GUI version.

When you set a charge limit, for example, on a Lenovo ThinkPad, you can set two percentages. The first one is the upper limit, and the second one is the lower limit. When the battery charge is above the upper limit, the computer will stop charging. When the battery charge is below the lower limit, the computer will start charging. If the battery charge is between the two limits, the computer will keep whatever charging state it is in.

`batt` have similar features built-in. The charge limit you have set (using `batt limit`) will be used as the upper limit. By default, The lower limit will be set to 2% less than the upper limit. To customize the lower limit, use `batt lower-limit-delta`.

For example, if you want to set the lower limit to be 5% less than the upper limit, run `sudo batt lower-limit-delta 5`. So, if you have your charge (upper) limit set to 60%, the lower limit will be 55%.

### Control MagSafe LED

> Acknowledgement: [@exidler](https://github.com/exidler)

This option can make the MagSafe LED on your MacBook change color according to the charging status. For example: 

- Green: charge limit is reached and charging is stopped.
- Orange: charging is in progress.
- Off: just woken up from sleep, charing is disabled and batt is waiting before controlling charging.

Note that you must have a MagSafe LED on your MacBook to use this feature.

To enable MagSafe LED control, run `sudo batt magsafe-led enable`.

### Check logs

Logs are directed to `/tmp/batt.log`. If something goes wrong, you can check the logs to see what happened. Raise an issue with the logs attached, so we can debug together.

## Building

You need to install command line developer tools (by running `xcode-select --install`) and Go (follow the official instructions [here](https://go.dev/doc/install)).

### CLI

```shell
make
```

Simply running `make` in this repo should build the binary into `./bin/batt`. You can then follow [the upgrade guide](#how-to-upgrade) to install it (you just use the binary you have built, not downloading a new one, of course).

### GUI

```shell
make app
```

This will build the GUI version of `batt` into `./bin/batt.app`. Drag it to `/Applications` and run it.

## Architecture

You can think of `batt` like `docker`. It has a daemon that runs in the background, and a client (CLI or GUI) that communicates with the daemon. They communicate through unix domain socket as a way of IPC. The daemon does the actual heavy-lifting, and is responsible for controlling battery charging. The client is responsible for sending users' requirements to the daemon.

For example, when you run `sudo batt limit 80`, the client will send the requirement to the daemon, and the daemon will do its job to keep the charge limit to 80%.

## Motivation

I created this tool simply because I am not satisfied with existing tools 😐.

I have written and using similar utils (to limit charging) on Intel MacBooks for years. Finally I got hands on an Apple Silicon MacBook (yes, 2 years later since it is introduced 😅 and I just got my first one). The old BCLM way to limit charging doesn't work anymore. I was looking for a tool to limit charging on M1 MacBooks.

I have tried some alternatives, both closed source and open source, but I kept none of them. Some paid alternatives' licensing options are just too limited 🤔, a bit bloated, require periodic Internet connection (hmm?) and are closed source. It doesn't seem a good option for me. Some open source alternatives just don't handle edge cases well and I encountered issues regularly especially when sleeping (as of Apr 2023).

I want a _simple_ tool that does just one thing, and **does it well** -- limiting charging, just like the [Unix philosophy](https://en.wikipedia.org/wiki/Unix_philosophy). It seems I don't have any options but to develop by myself. So I spent a weekend developing an MVP, so here we are! `batt` is here!

## FAQ

### How to uninstall?

#### GUI version

Click `Uninstall Daemon...` to uninstall. After the daemon is uninstalled, you can remove the `batt.app` from your `Applications` folder.

<img width="528" alt="SCR-20250710-mjrd" src="https://github.com/user-attachments/assets/901c7840-bae7-4111-af07-4fc1b39444d0" />


#### CLI version

Note that you should choose the same method as you used to install `batt` to uninstall it.

If you don't remember how you installed it, you can check the binary location by running `which batt`. If it is in `/usr/local/bin`, you probably used the installation script. If it is in `/opt/homebrew/bin`, you probably used Homebrew.

Script-installed:

1. Run `sudo batt uninstall` to remove the daemon.
2. Remove the config by `sudo rm /etc/batt.json`.
3. Remove the `batt` binary itself by `sudo rm $(which batt)`.

Homebrew-installed:

1. Run `sudo batt disable` to restore the default charge limit.
2. Run `sudo brew services stop batt` to stop the daemon.
3. Run `brew uninstall batt` to uninstall the binary.
4. Remove the config by `sudo rm /opt/homebrew/etc/batt.json`.

### How to upgrade?

#### GUI version

Just follow the installation steps again. After you open the new version of batt.app, click `Upgrade Daemon...` upgrade to the new daemon.

<img width="206" alt="SCR-20250624-lbmb-2" src="https://github.com/user-attachments/assets/f3731a00-d973-4d67-8b4a-d57595e3842f" />

#### CLI version

Note that you should choose the same method as you used to install `batt` to upgrade it.

If you don't remember how you installed it, you can check the binary location by running `which batt`. If it is in `/usr/local/bin`, you probably used the installation script. If it is in `/opt/homebrew/bin`, you probably used Homebrew.

Script-installed:

```bash
bash <(curl -fsSL https://github.com/charlie0129/batt/raw/master/hack/install.sh)
```

Homebrew-installed:

```bash
sudo brew services stop batt
brew upgrade batt
sudo brew services start batt
```

Manual:

1. Run `sudo batt uninstall` to remove the old daemon.
2. Download the new binary. For _stable_ and _beta_ releases, you can find the download link in the [release page](https://github.com/charlie0129/batt/releases). If you want development versions with the latest features and bug fixes, you can download prebuilt binaries from [GitHub Actions](https://github.com/charlie0129/batt/actions/workflows/build-test-binary.yml) (has a retention period of 3 months and you need to `chmod +x batt` after extracting the archive) or [build it yourself](#building) .
3. Replace the old `batt` binary with the downloaded new one. `sudo cp ./batt $(where batt)`
4. Run `sudo batt install` to install the daemon again. Although most config is preserved, some security related config is intentionally reset during re-installation. For example, if you used `--allow-non-root-access` when installing previously, you will need to use it again like this `sudo batt install --allow-non-root-access`.

### Why is it Apple Silicon only?

You probably don't need this on Intel :p

On Intel MacBooks, you can control battery charging in a much, much easier way, simply setting the `BCLM` key in Apple SMC to the limit you need, and you are all done. There are many tools available. For example, you can use [smc-command](https://github.com/hholtmann/smcFanControl/tree/master/smc-command) to set SMC keys. Of course, you will lose some advanced features like upper and lower limit.

However, on Apple Silicon, the way how charging is controlled changed. There is no such key. Therefore, we have to use a much more complicated way to achieve the same goal, and handle more edge cases, hence `batt`.

### Will there be an Intel version?

Probably not. `batt` was made Apple-Silicon-only after some early development. I have tested batt on Intel during development (you can probably find some traces from the code :). Even though some features in batt are known to work on Intel, some are not. Development and testing on Intel requires additional effort, especially those feature that are not working. Considering the fact that Intel MacBooks are going to be obsolete in a few years and some similar tools already exist (without some advanced features), I don't think it is worth the effort.

### Why does my MacBook stop charging after I close the lid?

TL,DR; This is intended, and is the default behavior. It is described [here](#disabling-charging-before-sleep). You can turn this feature off by running `sudo batt disable-charging-pre-sleep disable` (not recommended, keep reading).

But it is suggested to keep the default behavior to make your charge limit work as intended. Why? Because when you close the lid, your MacBook will go into **forced sleep**, and `batt` will be paused by macOS. As a result, `batt` can no longer control battery charging. It will be whatever state it was before you close the lid. This is the problem. Let's say, if you close the lid when your MacBook is charging, since `batt` is paused by macOS, it will keep charging, ignoring the charge limit you have set. There is no way to prevent **forced sleep**. Therefore, the only way to solve this problem is to disable charging before sleep. This is what `batt` does. It will disable charging just before your MacBook goes to sleep, and re-enable it when it wakes up. This way, your Mac will not overcharge during sleep.

Not that you will encounter this **forced sleep** only if you, the user, forced the Mac to sleep, either by closing the lid or selecting the Sleep option in the Apple menu. If your Mac decide to sleep by itself, called **idle sleep**, i.e. when it is idle for a while, in this case, you will not experience this stop-charging-before-sleep situation.

So it is suggested to keep this feature on. But _What if I MUST let my Mac charge during a **forced sleep** without turing off `disable-charging-pre-sleep`, even if it may charge beyond the charge limit?_ This is simple, just disable charge limit `batt disable`. This way, when you DO want to enable charge limit again, `disable-charging-pre-sleep` will still be there to prevent overcharging. The rationale is: when you want to charge during a **forced sleep**, you actually want heavy use of your battery and don't want ANY charge limit at all, e.g. when you are on a long outside-event, and you want to charge your Mac when it is sitting in your bag, lid closed. Setting the charge limit to 100% is equivalent to disabling charge limit. Therefore, most `batt` features will be turned off and your Mac can charge as if `batt` is not installed.

### Why does it require root privilege?

It writes to SMC to control battery charging. This does changes to your hardware, and is a highly privileged operation. Therefore, it requires root privilege.

If you are concerned about security, you can check the source code [here](https://github.com/charlie0129/batt) to make sure it does not do anything funny.

### Why is it written in Go and C?

Since it is a hobby project, I want to balance effort and the final outcome. Go seems a good choice for me. However, C is required to register sleep and wake notifications using Apple's IOKit framework. Also, Go don't have any library to r/w SMC, so I have to write it myself ([charlie0129/gosmc](https://github.com/charlie0129/gosmc)). This part is also mainly written in C as it interacts with the hardware and uses OS capabilities. Thankfully, writing a library didn't slow down development too much.

### Why is there so many logs?

By default, `batt` daemon will have its log level set to `debug` for easy debugging. The `debug` logs are helpful when reporting problems since it contains useful information. So it is recommended to keep it as `debug`. You may find a lot of logs in `/tmp/batt.log` after you use your Mac for a few days. However, there is no need to worry about this. The logs will be cleaned by macOS on reboot. It will not grow indefinitely.

If you believe you will not encounter any problem in the future and still want to set a higher log level, you can achieve this by:

1. Stop batt: `sudo launchctl unload /Library/LaunchDaemons/cc.chlc.batt.plist` (batt must be stopped to change config so you can't skip this step)
2. Use your preferred editor to edit `/Library/LaunchDaemons/cc.chlc.batt.plist` and change the value of `-l=debug` to your preferred level. The default value is `debug`.
3. Start batt again: `sudo launchctl load /Library/LaunchDaemons/cc.chlc.batt.plist`

### Why does my Mac go to sleep when I disable the power adapter?

You are probably using Clamshell mode, i.e., using a Mac laptop with an external monitor and the lid closed. This is a limitation of macOS. Clamshell mode MUST have power connected, otherwise, the Mac will go to sleep.

If you want to prevent this, you can use a third-party app like [Amphetamine](https://apps.apple.com/us/app/amphetamine/id937984704?mt=12) to prevent sleep.

### My Mac does not start charging after waking up from sleep

This is expected. batt will prevent your Mac from charging temporarily if your Mac has just woken up from sleep. This is to prevent overcharging during sleep. Your Mac will start charging soon (at most 2 minutes).

If you absolutely need to charge your Mac _immediately_ after waking up from sleep, you can disable this feature by running `sudo batt disable-charging-pre-sleep disable`. However, this is not recommended (see [Disabling charging before sleep](#disabling-charging-before-sleep)).

## Acknowledgements

- [actuallymentor/battery](https://github.com/actuallymentor/battery) for various SMC keys.
- [hholtmann/smcFanControl](https://github.com/hholtmann/smcFanControl) for its C code to read/write SMC, which inspires [charlie0129/gosmc](https://github.com/charlie0129/gosmc).
- [Apple](https://developer.apple.com/library/archive/qa/qa1340/_index.html) for its guide to register and unregister sleep and wake notifications.
- [@exidler](https://github.com/exidler) for building the MagSafe LED controlling logic.
- [@pichxyaponn](https://github.com/pichxyaponn) for the initial version of GUI.

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=charlie0129/batt&type=Date)](https://www.star-history.com/#charlie0129/batt&Date)
