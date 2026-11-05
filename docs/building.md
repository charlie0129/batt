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