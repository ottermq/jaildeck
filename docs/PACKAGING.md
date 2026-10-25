---
title: "Native FreeBSD packages"
description: "Build, inspect, install, upgrade and remove a locally built Jail Deck package."
---

# Native FreeBSD packages

`make package VERSION=0.10.0` builds `bin/packages/jaildeck-0.10.0.pkg` using
FreeBSD's `pkg create`. It builds the Svelte frontend, compiles the versioned Go
executable, and reuses `make install DESTDIR=...` to stage the files. No Ports
tree, package repository, publishing workflow or additional build framework is
involved. The package name and selected files are deterministic; archive bytes
and build timestamps are not promised to be reproducible.

## Build from a clean checkout

Build **natively on FreeBSD 15 amd64**, with Go 1.26 or newer and Node.js/npm
compatible with the pinned frontend dependencies. Node.js 24 is a suitable
choice. Bootstrap `pkg` if it is not installed, then install the build tools as
root:

```sh
pkg bootstrap
pkg install go node24 npm-node24 git-lite
```

Build as a normal user; package creation does not require root:

```sh
git clone https://github.com/ottermq/jaildeck.git
cd jaildeck
# Check out the commit you intend to package before building.
make package VERSION=0.10.0
```

The version is required and must have the form `X.Y.Z`, without a leading `v`.
The same value is used in the manifest, archive filename and `jaildeck version`.
The target supports only `/usr/local`; a different `PREFIX` is rejected. It
checks the native OS and architecture, the Go target, and `pkg config ABI`
(`FreeBSD:15:amd64`) before building. Cross-packaging is deliberately unsupported.

`npm ci` runs on every package build, using `web/package-lock.json`; the Go
toolchain fetches any uncached modules. Network access is needed for uncached
dependencies. Do not run concurrent frontend/package builds in one checkout.

The frontend build finishes before `make install` compiles the executable,
including with `make -j package`. The package build requires a nonempty
`web/dist/index.html` referencing a built JavaScript bundle, verifies referenced
assets exist, and checks that the index and every built asset occur in the
compiled executable. This check lives in a build tool, not in runtime startup.
`make build` still works with Go alone and retains the intentional development
fallback when the frontend has not been built (JD-015).

Each invocation uses a fresh temporary build/staging directory under
`bin/packages`. It checks the staged inventory, package metadata, packaged
inventory and complete archive payload before moving the archive to its final
name. A failed build returns nonzero and removes its temporary work; if a
previous successful package of that version exists, it remains untouched. A
successful rebuild replaces it. Other versions are left in place. `make clean`
removes `bin`, including the packages.

## Inspect before installation

On a FreeBSD machine with `pkg`, these commands read the archive without
installing it:

```sh
pkg info -F bin/packages/jaildeck-0.10.0.pkg
pkg query -F bin/packages/jaildeck-0.10.0.pkg '%n %v %q %p'
pkg query -F bin/packages/jaildeck-0.10.0.pkg '%Fp'
pkg info --raw -F bin/packages/jaildeck-0.10.0.pkg
tar -tvf bin/packages/jaildeck-0.10.0.pkg
```

The manifest is UCL, with `sysutils/jaildeck` as the local package origin. That
origin is a package identity, not a claim that a FreeBSD port exists. The ABI is
read from the native `pkg` installation rather than assigned to a cross-built
binary. The explicit packing list sets `root:wheel` ownership independently of
the account that built the package:

| Package-owned path | Mode |
| --- | --- |
| `/usr/local/bin/jaildeck` | `0555` |
| `/usr/local/etc/rc.d/jaildeck` | `0555` |
| `/usr/local/etc/jaildeck/jaildeck.env.sample` | `0644` |

The executable is built with `CGO_ENABLED=0` and the baseline amd64 CPU target.
Go and Node.js are build dependencies, not runtime dependencies. Native FreeBSD
tools provide the application operations. Optional disk-health reporting uses
`smartmontools` when the operator installs it; its absence is handled by the
existing runtime and does not make it a mandatory package dependency.

## Install and configure

Copy the archive to the FreeBSD host. As root:

```sh
pkg add ./jaildeck-0.10.0.pkg
pkg info jaildeck
jaildeck version
pkg check -s jaildeck
```

Installation displays instructions but does not configure or start anything.
For a **new deployment**, copy and edit the sample, then load the same settings
the service will use before creating the operator credential:

```sh
cp -n /usr/local/etc/jaildeck/jaildeck.env.sample /usr/local/etc/jaildeck/jaildeck.env
vi /usr/local/etc/jaildeck/jaildeck.env
set -a
. /usr/local/etc/jaildeck/jaildeck.env
set +a
jaildeck passwd
sysrc jaildeck_enable=YES
service jaildeck start
```

Loading the environment matters when `JAILDECK_STATE_DIR` differs from its
default: credential setup and the service must use the same directory. Keep the
file in the existing shell-compatible `KEY=value` format. An existing
deployment keeps its configuration and credential; do not repeat first-run
setup during an upgrade.

Host preparation remains an explicit operator action. Review readiness and
the selected pool before running `jaildeck init`. Package installation never
initializes ZFS, changes networking, creates credentials, enables the service
or starts it. The default UI bind remains `127.0.0.1:8888`; reach it remotely
with the SSH forwarding described in [the README](../README.md#first-run).

## Upgrade or replace a local package

Read the release notes and back up operator data before upgrading. Stop the
running service explicitly, then replace the registered package:

```sh
service jaildeck stop
pkg add -f ./jaildeck-0.10.1.pkg
pkg info jaildeck
jaildeck version
pkg check -s jaildeck
# Review changes to jaildeck.env.sample and merge them into your live config.
service jaildeck start
```

`pkg add -f` performs local replacement, including a same-version rebuild; it
does not check a package repository for updates. The package has no lifecycle
scripts, including no automatic stop/restart. If the service is disabled, omit
the stop/start steps; use `onestop`/`onestart` only for deliberate one-off runs.

Only the sample is packaged. There is no `@sample` directive or registered
configuration file that could silently create, merge or replace
`jaildeck.env`. The live configuration, `/var/db/jaildeck` (credentials, API
tokens and other state), `/var/log/jaildeck` (audit history), and jail datasets
are all outside the package inventory.

## Remove the application

Stop the service first and remove its enablement explicitly if you no longer
want it enabled:

```sh
service jaildeck stop
sysrc -x jaildeck_enable
pkg delete jaildeck
```

`pkg delete` removes the three package-owned files. It does not remove the live
configuration, state, logs, jail files, ZFS datasets or snapshots. Those remain
for later reinstallation or an operator-directed cleanup. The configuration
directory remains when it contains the live configuration.

## Moving from a manual installation

`make install` does not register a package. `pkg` cannot reliably identify which
version produced an unregistered executable or service script, and installation
is not an adoption or backup mechanism for local modifications.

Stop the manual service, back up its binary, service script, sample, live config
and operator data, and check `pkg which` on the three application paths. If a
different package owns them, resolve that conflict first. For an unregistered
manual install, move **only those three application files** out of the way
before `pkg add`; leave the live config, state, logs and datasets in place.
Do not use broad `pkg -f` overrides to hide ownership conflicts. Review local
changes to the service script before replacing it with the packaged version.

After migration, use `pkg` for upgrades and removal. Running `make install` over
a packaged deployment bypasses the package database and invalidates its file
checksums; it is not a package upgrade.

## Validation

The release frontend checker has Go tests and runs as part of the package
build. Ordinary project checks still apply (`gmake check` and `gmake test-race`
for the full existing GNU Make workflow on FreeBSD).

`sh packaging/freebsd/test-build.sh` exercises packaging failures on the native
build host without installing anything: missing prerequisites, invalid target
settings, stale frontend output, and build/staging/package-creation errors. It
uses temporary command wrappers to inject failures and checks that no partial
archive or work directory is left. It rebuilds `web/dist`, as packaging does.

An opt-in lifecycle test is included for a **fresh disposable FreeBSD VM**.
It refuses an existing Jaildeck installation or operator-data paths. Build two
versions first, then run as root, supplying an existing ZFS pool:

```sh
make package VERSION=0.10.0
make package VERSION=0.10.1
JAILDECK_TEST_DISPOSABLE=YES sh packaging/freebsd/test-lifecycle.sh \
  bin/packages/jaildeck-0.10.0.pkg bin/packages/jaildeck-0.10.1.pkg zroot
```

The test performs real `pkg add`, replacement and `pkg delete`; checks package
registration, versions, file ownership/modes, service opt-in, embedded HTML/JS
and the authentication gate; and compares live configuration, real credential
and token storage, audit data and a ZFS snapshot across upgrade and removal.
The HTTP smoke test explicitly uses the fake adapter. It leaves its operator
data, `zroot/jaildeck-package-test` dataset/snapshot and evidence directory for
inspection. Destroy the disposable VM or clean up these known test fixtures
before rerunning. Never run this test on a real deployment.

See [the validation record](PACKAGING-VALIDATION.md) for the environment and
checks actually exercised. Compatibility with other architectures or FreeBSD
major versions, Ports, repositories, signing and automated publishing are
outside this workflow.

## References

- [Issue #84](https://github.com/ottermq/jaildeck/issues/84)
- [pkg-create(8)](https://man.freebsd.org/cgi/man.cgi?query=pkg-create&sektion=8): metadata, packing lists and staging roots.
- [FreeBSD configuration-file conventions](https://docs.freebsd.org/en/books/porters-handbook/plist/#plist-config): explicitly installing only a sample when the operator must configure the application.
