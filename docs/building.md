# Building

You need to install the command-line developer tools (by running `xcode-select --install`) and Go (follow the official instructions [here](https://go.dev/doc/install)).

### CLI

```shell
make
```

Simply running `make` in this repo should build the binary into `./bin/batt`. You can then follow [the upgrade guide](faq.md#how-to-upgrade) to install it (you just use the binary you have built, not downloading a new one, of course).

### GUI

```shell
make app
```

This will build the GUI version of `batt` into `./bin/batt.app`. Drag it to `/Applications` and run it.