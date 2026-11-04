## Installation (GUI Version)

GUI version is a native macOS menubar app. It's not as feature-complete as the command-line version, but it is a good choice if you are not comfortable with the command-line. The command-line version is also included if you have the GUI version.

1. Download `.dmg` file from [Releases](https://github.com/charlie0129/batt/releases) and open it (right-click open if macOS says it's damaged)
2. Drag `batt.app` to `Applications`
3. macOS may say it's damaged when you try to run it (it's NOT) and wants you to move it to trash. To fix it, run this in Terminal: `sudo xattr -r -d com.apple.quarantine /Applications/batt.app`.
4. Run `batt.app`.
5. Follow the MenuBar UI to install or upgrade.
6. It is _highly_ recommended to disable macOS's optimized charging when using `batt`. To do so, open `System Settings` -> `Battery` -> `Battery Health` -> `i` -> Turn OFF `Optimized Battery Charging`. Built-in charge limit also needs to be disabled if you are on macOS 26.4 or later.

<img width="191" alt="SCR-20250624-lbmb-3" src="https://github.com/user-attachments/assets/4bef52d7-8483-49bd-b579-736b87c81a52" />
<img width="206" alt="SCR-20250624-lbmb-2" src="https://github.com/user-attachments/assets/f3731a00-d973-4d67-8b4a-d57595e3842f" />
<img width="450" alt="SCR-20250624-lbmb-4" src="https://github.com/user-attachments/assets/a9d3c2eb-5d41-400e-b042-b79f6e8decd0" />

!!! tip
    There are 3rd-party GUI versions built around `batt` by some amazing opensource developers:
    1. [BattGUI](https://github.com/clzoc/BattGUI) by [@clzoc](https://github.com/clzoc)
    2. [batt-helper](https://github.com/zxw5775/batt-helper) by [@zxw5775](https://github.com/zxw5775)

## Installation (Command-Line Version)

!!! note
    Command-Line version is already included if you have installed the GUI version. You can run `batt` in Terminal to use it.

You have two choices to install the CLI version of `batt`:

1. Homebrew [Docs](#homebrew)
2. Installation Script (Recommended) [Docs](#installation-script)

You can choose either one. Please do not use both at the same time to avoid conflicts.

### Homebrew

!!! warning "Important"
    If you previously ran `batt` v0.7.3 or earlier with `sudo brew services`, follow the [one-time service-label migration](faq.md#homebrew-service-label-migration) before starting the service. When the Homebrew formula moved from v0.7.3 to v0.7.5, its service label changed from `cc.chlc.batt` to `homebrew.mxcl.batt`.

1. `brew install batt`
2. `sudo brew services start batt`
3. Please read [Notes](#notes).

> Thank you, [@Jerry1144](https://github.com/charlie0129/batt/issues/9#issuecomment-2165493285), for bootstrapping the Homebrew formula.

### Installation Script

1. (Optional) There is an installation script to help you quickly install batt (Internet connection required). Put this in your terminal: `bash <(curl -fsSL https://github.com/charlie0129/batt/raw/master/hack/install.sh)`. You may need to provide your login password (to control charging). This will download and install the latest _stable_ version for you. Follow the on-screen instructions, then you can skip to step 5.
<details>
<summary>Manual installation steps</summary>

2. Get the binary. For _stable_ and _beta_ releases, you can find the download link in the [release page](https://github.com/charlie0129/batt/releases). If you want development versions with the latest features and bug fixes, you can download prebuilt binaries from [GitHub Actions](https://github.com/charlie0129/batt/actions/workflows/build-test-binary.yml) (has a retention period of 3 months and you need to `chmod +x batt` after extracting the archive) or [build it yourself](building.md) .
3. Put the binary somewhere safe. You don't want to move it after installation :). It is recommended to save it in your `$PATH`, e.g., `/usr/local/bin`, so you can directly call `batt` on the command-line. In this case, the binary location will be `/usr/local/bin/batt`.
4. Install daemon using `sudo batt install`. If you do not want to use `sudo` every time after installation, add the `--allow-non-root-access` flag: `sudo batt install --allow-non-root-access`. To uninstall: please refer to [How to uninstall?](faq.md#how-to-uninstall)
</details>

5. In case you have GateKeeper turned on, you will see something like _"batt can't be opened because it was not downloaded from the App Store"_ or _"batt cannot be opened because the developer cannot be verified"_. If you don't see it, you can skip this step. To solve this, you can either 1. (recommended) Go to *System Settings* -> *Privacy & Security* --scroll-down--> *Security* -> *Open Anyway*; or 2. run `sudo spctl --master-disable` to disable GateKeeper entirely.

### Notes

- Test if it works by running `sudo batt status`. If you see your battery status, you are good to go!
- Time to customize. By default `batt` will set a charge limit to 60%. For example, to set the charge limit to 80%, run `sudo batt limit 80`.
- As said before, it is _highly_ recommended to disable macOS's optimized charging when using `batt`. To do so, open `System Settings` -> `Battery` -> `Battery Health` -> `i` -> Turn OFF `Optimized Battery Charging`. Built-in charge limit also needs to be disabled if you are on macOS 26.4 or later.
- If your current charge is above the limit, behavior depends on the detected backend. Firmware < `20xxx` stops charging and keeps running from the wall, so the percentage stays steady. `20xxx` firmware may run from the battery until it falls back into the configured range.
- You can refer to [Usage](usage.md) for additional configurations. Don't know what a command does? Run `batt help` to see all available commands. To see help for a specific command, run `batt help <command>`.
- To disable the charge limit, run `batt disable` or `batt limit 100`.
- [How to uninstall?](faq.md#how-to-uninstall) [How to upgrade?](faq.md#how-to-upgrade)

> if you find `batt` helpful, stars ⭐️ are much appreciated!