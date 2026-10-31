# FAQ

### How to uninstall?

#### GUI version

Click `Uninstall Daemon...` to uninstall. After the daemon is uninstalled, you can remove the `batt.app` from your `Applications` folder.

<img width="528" alt="SCR-20250710-mjrd" src="https://github.com/user-attachments/assets/901c7840-bae7-4111-af07-4fc1b39444d0" />


#### CLI version

Note that you should choose the same method as you used to install `batt` to uninstall it.

If you don't remember how you installed it, you can check the binary location by running `which batt`. If it is in `/usr/local/bin`, you probably used the installation script. If it is in `/opt/homebrew/bin`, you probably used Homebrew.

Script-installed:

1. Run `sudo batt uninstall` to remove the daemon.
2. Remove the config by `sudo rm -f /etc/batt*`.
3. Remove the `batt` binary itself by `sudo rm $(which batt)`.

Homebrew-installed:

1. Run `sudo batt disable` to restore the default charge limit.
2. Run `sudo brew services stop batt` to stop the daemon.
3. Run `brew uninstall batt` to uninstall the binary.
4. Remove the config by `sudo rm -f /opt/homebrew/etc/batt.json && sudo rm -f /etc/batt*`.

### How to upgrade?

#### GUI version

Just follow the installation steps again. After you open the new version of batt.app, click `Upgrade Daemon...` upgrade to the new daemon.

<img width="206" alt="SCR-20250624-lbmb-2" src="https://github.com/user-attachments/assets/f3731a00-d973-4d67-8b4a-d57595e3842f" />

#### CLI version

Note that you should choose the same method as you used to install `batt` to upgrade it.

If you don't remember how you installed it, you can check the binary location by running `which batt`. If it is in `/usr/local/bin`, you probably used the installation script. If it is in `/opt/homebrew/bin`, you probably used Homebrew.

**Script-installed:**

```bash
bash <(curl -fsSL https://github.com/charlie0129/batt/raw/master/hack/install.sh)
```

**Homebrew-installed:**

##### Homebrew service-label migration

If you previously ran `batt` v0.7.3 or earlier with `sudo brew services`, use the following commands once. When the Homebrew formula moved from v0.7.3 to v0.7.5, its service label changed from `cc.chlc.batt` to `homebrew.mxcl.batt`, and `brew services stop batt` may not stop the job registered under the old label. The old job must be booted out before its plist is removed; deleting the plist alone does not unregister a running job from `launchd`.

```bash
sudo brew services stop batt
if sudo launchctl print system/cc.chlc.batt >/dev/null 2>&1; then
  sudo launchctl bootout system/cc.chlc.batt
fi
sudo rm -f /Library/LaunchDaemons/cc.chlc.batt.plist
brew upgrade batt
sudo brew services start batt
```

No uninstall or reinstall is needed. For later Homebrew upgrades, use the normal procedure:

```bash
sudo brew services stop batt
brew upgrade batt
sudo brew services start batt
```

**Manual:**

1. Run `sudo batt uninstall` to remove the old daemon.
2. Download the new binary. For _stable_ and _beta_ releases, you can find the download link in the [release page](https://github.com/charlie0129/batt/releases). If you want development versions with the latest features and bug fixes, you can download prebuilt binaries from [GitHub Actions](https://github.com/charlie0129/batt/actions/workflows/build-test-binary.yml) (has a retention period of 3 months and you need to `chmod +x batt` after extracting the archive) or [build it yourself](building.md) .
3. Replace the old `batt` binary with the downloaded new one. `sudo cp ./batt $(which batt)`
4. Run `sudo batt install` to install the daemon again. Although most config is preserved, some security related config is intentionally reset during re-installation. For example, if you used `--allow-non-root-access` when installing previously, you will need to use it again like this `sudo batt install --allow-non-root-access`.

### Why is it Apple Silicon only?

You probably don't need this on Intel :p

On Intel MacBooks, you can control battery charging in a much, much easier way, simply setting the `BCLM` key in Apple SMC to the limit you need, and you are all done. There are many tools available. For example, you can use [smc-command](https://github.com/hholtmann/smcFanControl/tree/master/smc-command) to set SMC keys. Of course, you will lose some advanced features like upper and lower limit.

However, on Apple Silicon, the way how charging is controlled changed. There is no such key. Therefore, we have to use a much more complicated way to achieve the same goal, and handle more edge cases, hence `batt`.

### Will there be an Intel version?

Probably not. `batt` was made Apple-Silicon-only after some early development. I have tested batt on Intel during development (you can probably find some traces from the code :). Even though some features in batt are known to work on Intel, some are not. Development and testing on Intel requires additional effort, especially those feature that are not working. Considering the fact that Intel MacBooks are going to be obsolete in a few years and some similar tools already exist (without some advanced features), I don't think it is worth the effort.

### Why does my MacBook stop charging after I close the lid?

!!! note
    This FAQ applies only to older firmware < `20xxx` using direct charging control. On `20xxx` firmware, the charge limit persists through sleep and `batt` does not disable charging before sleep.

TL;DR: This is intended, and is the default behavior. It is described [here](usage.md#disabling-charging-before-sleep). You can turn this feature off by running `sudo batt disable-charging-pre-sleep disable` (not recommended, keep reading).

But it is suggested to keep the default behavior to make your charge limit work as intended. Why? Because when you close the lid, your MacBook will go into **forced sleep**, and `batt` will be paused by macOS. As a result, `batt` can no longer control battery charging. It will be whatever state it was before you close the lid. This is the problem. Let's say, if you close the lid when your MacBook is charging, since `batt` is paused by macOS, it will keep charging, ignoring the charge limit you have set. There is no way to prevent **forced sleep**. Therefore, the only way to solve this problem is to disable charging before sleep. This is what `batt` does. It will disable charging just before your MacBook goes to sleep, and re-enable it when it wakes up. This way, your Mac will not overcharge during sleep.

Not that you will encounter this **forced sleep** only if you, the user, forced the Mac to sleep, either by closing the lid or selecting the Sleep option in the Apple menu. If your Mac decide to sleep by itself, called **idle sleep**, i.e. when it is idle for a while, in this case, you will not experience this stop-charging-before-sleep situation.

So it is suggested to keep this feature on. But _What if I MUST let my Mac charge during a **forced sleep** without turing off `disable-charging-pre-sleep`, even if it may charge beyond the charge limit?_ This is simple, just disable charge limit `batt disable`. This way, when you DO want to enable charge limit again, `disable-charging-pre-sleep` will still be there to prevent overcharging. The rationale is: when you want to charge during a **forced sleep**, you actually want heavy use of your battery and don't want ANY charge limit at all, e.g. when you are on a long outside-event, and you want to charge your Mac when it is sitting in your bag, lid closed. Setting the charge limit to 100% is equivalent to disabling charge limit. Therefore, most `batt` features will be turned off and your Mac can charge as if `batt` is not installed.

### Why does it require root privileges?

It writes to SMC to control battery charging. This changes your hardware, and is a highly privileged operation. Therefore, it requires root privileges.

If you are concerned about security, you can check the source code [here](https://github.com/charlie0129/batt) to make sure it does not do anything funny.

### Why is it written in Go and C?

Since it is a hobby project, I want to balance effort and the final outcome. Go seems a good choice for me. However, C is required to register sleep and wake notifications using Apple's IOKit framework. Also, Go don't have any library to r/w SMC, so I have to write it myself ([charlie0129/gosmc](https://github.com/charlie0129/gosmc)). This part is also mainly written in C as it interacts with the hardware and uses OS capabilities. Thankfully, writing a library didn't slow down development too much.

### Why are there so many logs?

By default, `batt` daemon will have its log level set to `debug` for easy debugging. The `debug` logs are helpful when reporting problems since it contains useful information. So it is recommended to keep it as `debug`. You may find a lot of logs in `/tmp/batt.log` after you use your Mac for a few days. However, there is no need to worry about this. The logs will be cleaned by macOS on reboot. It will not grow indefinitely.

If you believe you will not encounter any problem in the future and still want to set a higher log level, you can achieve this by:

1. Stop batt: `sudo launchctl unload /Library/LaunchDaemons/cc.chlc.batt.plist` (batt must be stopped to change config so you can't skip this step)
2. Use your preferred editor to edit `/Library/LaunchDaemons/cc.chlc.batt.plist` and change the value of `-l=debug` to your preferred level. The default value is `debug`.
3. Start batt again: `sudo launchctl load /Library/LaunchDaemons/cc.chlc.batt.plist`

### Why does my Mac go to sleep when I disable the power adapter?

You are probably using Clamshell mode, i.e., using a Mac laptop with an external monitor and the lid closed. This is a limitation of macOS. Clamshell mode MUST have power connected, otherwise, the Mac will go to sleep.

If you want to prevent this, you need to disable sleep globally using pmset, or you can use a third-party app like [Amphetamine](https://apps.apple.com/us/app/amphetamine/id937984704?mt=12) to prevent sleep.

### My Mac does not start charging after waking up from sleep

!!! note
    This FAQ applies only to older firmware < `20xxx` using legacy direct charging control. `20xxx` firmware has no `batt` post-wake delay.

This is expected. batt will prevent your Mac from charging temporarily if your Mac has just woken up from sleep. This is to prevent overcharging during sleep. Your Mac will start charging soon (at most 2 minutes).

If you absolutely need to charge your Mac _immediately_ after waking up from sleep, you can disable this feature by running `sudo batt disable-charging-pre-sleep disable`. However, this is not recommended (see [Disabling charging before sleep](usage.md#disabling-charging-before-sleep)).

### Why does my Mac start charging after entering sleep?

!!! note
    This FAQ applies only to older firmware < `20xxx` using legacy direct charging control. `20xxx` firmware continues enforcing the configured range during sleep.

If you are using the default batt settings (i.e., `prevent-idle-sleep` and `disable-charging-pre-sleep` are both enabled), this should not happen.

However, there are some cases like [#101](https://github.com/charlie0129/batt/issues/101) where hibernation mode is unexpectedly changed by the user. Your Mac may enter hibernation immediately instead of sleep, and Mac firmware resets SMC keys after hibernation, causing batt to lose control of charging.

To see if you are in always-hibernate mode, run `pmset -g | grep hibernatemode`. If the value is not `0` nor `3`, you are in always-hibernate mode. To change it back to sleep mode, run `sudo pmset -a hibernatemode 3` (factory default for MacBooks, hibernates after several hours) or `sudo pmset -a hibernatemode 0` (never hibernate). After changing the mode, reboot your Mac for the change to take effect.

### Can I quit the GUI app?

Yes.

As in [#62](https://github.com/charlie0129/batt/issues/62), you can quit the GUI app and expect batt to continue working, provided that you have installed the daemon during the first run.

You may also want to remove the GUI app from *System Settings* -> *General* -> *Login Items & Extensions* to prevent it from starting automatically on login. Important: only remove the GUI app from *Open at Login*, do NOT remove the daemon from *Allow in Background*.

The reason behind is that the GUI app does not do the actual battery charge controlling. The daemon does. As long as the daemon is not uninstalled, charging control will work. The GUI app is just a client to communicate with the daemon. Quitting the GUI app will not affect the daemon. You can save some menu bar space if you rarely change settings.

### Will batt work after shutdown?

No. batt only works when macOS is running. After shutdown, there is no way to control battery charging until macOS boots up again.
