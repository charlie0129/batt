# Building

You need to install the command-line developer tools (by running `xcode-select --install`) and Go (follow the official instructions [here](https://go.dev/doc/install)).

### CLI

```shell
make
```

Simply running `make` in this repo should build the binary into `./bin/batt`. You can then follow [the upgrade guide](faq.md#how-to-upgrade) to install it (you just use the binary you have built, not downloading a new one, of course).

To build the CLI from source for macOS 12 Monterey (thanks @mvanhorn), set the deployment target:

```shell
MACOSX_DEPLOYMENT_TARGET=12.0 make
```

This target omits login-item integration, including registration, unregistration, and status checks. The default deployment target remains macOS 13.0; this source-build option does not change prebuilt release compatibility.

When switching deployment targets, add `GOFLAGS=-a` (for example, `MACOSX_DEPLOYMENT_TARGET=12.0 GOFLAGS=-a make`) to rebuild cached native code for the new target.

### GUI

```shell
make app
```

This will build the GUI version of `batt` into `./bin/batt.app`. Drag it to `/Applications` and run it.
### Tests

```shell
go test ./...
```

The tests need no hardware access and run on CI (`go test -race ./...`). The daemon tests in `pkg/daemon` drive the real daemon against a simulated Mac, so the behavior on a firmware you do not own can still be checked:

- `testenv_test.go` holds a catalog of firmwares (`firmwareLegacy`, `firmwareTahoe`, `firmwareManaged`, `firmwareGated`, `firmwareGatedNoAdapter`), described by the SMC keys they expose. The daemon decides its charge-control mode from those keys, exactly as on hardware.
- `newFixture(t, firmware, options...)` builds a `Daemon` for such a Mac with fakes for everything outside the process: the SMC (backed by gosmc's in-memory driver, with per-call fault injection), PowerUIAgent, the `SleepDisabled` setting, power assertions, sleep notifications, and IOKit battery telemetry. Options set the initial charge, plug state, adapter state, config, and so on.
- A test then calls the same entry points macOS and the CLI use: `f.put("/limit", "80")`, `f.d.loopOnce(now)`, `f.d.systemWillSleep()`, and inspects the simulated hardware with `f.smc.chargingEnabled(t)`, `f.smc.firmwareLimit(t)`, `f.sleep.disabled()`, and friends.

`simulation_test.go` shows the long form: a crude battery model fed through the main loop for hundreds of iterations, per firmware.
