---
title: "Package validation record"
description: "Native FreeBSD package checks exercised for issue #84."
---

# Package validation record

Validation uses a newly created, disposable `freebsd-jails` KVM VM, not an
existing Jail Deck deployment. The checkout is based on `c2de658` plus the
packaging changes. Host preparation and test-fixture creation are explicit test
actions, not package lifecycle hooks.

Environment: FreeBSD 15.1-RELEASE-p6 amd64, `pkg` 2.8.4,
Go 1.26.10, Node.js 24.21.0, npm 12.0.2. `pkg config ABI` reports
`FreeBSD:15:amd64`.

## Completed checks

- Native, unprivileged `make package VERSION=0.10.0` from a clean source checkout.
- Native `make -j4 package VERSION=0.10.1`, using FreeBSD's base BSD make.
- Compiled and packaged version `0.10.0`; native ABI and `/usr/local` prefix.
- Exactly the executable, rc.d script and sample configuration in the package.
- Built HTML, JavaScript, CSS and public assets present in the release executable.
- Full archive extraction and byte comparison against the staged files.
- Frontend-checker Go tests, including missing bundles and stale executables.
- Missing version and unsupported Linux build host rejected before building.
- Fourteen native build-failure cases: absent/malformed version, unsupported
  prefix, cross-compilation, ABI mismatch, missing Go/Node/npm, unbootstrapped
  pkg, frontend failure, successful-but-empty frontend output with a stale
  index present, compiler failure, staging failure, and `pkg create` failure.
  All returned nonzero without publishing a partial archive or leaving work
  directories.
- Linux `make check` and `make test-race`; FreeBSD `gmake check` (formatting,
  vet, build, generated API documentation and the full Go test suite).
- With Node.js/npm actually uninstalled and only `web/dist/.gitkeep` present,
  native `make build` and `TestSPAIsPublic` passed. No release-only build tags
  or runtime authentication/configuration changes were needed.

## Lifecycle verification

The opt-in [lifecycle test](https://github.com/ottermq/jaildeck/blob/dev/packaging/freebsd/test-lifecycle.sh) **passed**,
with both Go and Node.js/npm uninstalled before the run:

1. `pkg add jaildeck-0.10.0.pkg` registered the application. `pkg info`,
   `pkg check -s` and `jaildeck version` agreed with the archive metadata.
2. The three installed files had `root:wheel` ownership and modes `0555`,
   `0555`, `0644`. No live configuration, state or log directory was created
   by installation. The ZFS inventory, network interfaces and `/etc/rc.conf`
   stayed unchanged, and the service was not started.
3. Explicit test setup created a real operator credential and API token. The
   rc.d service started and stopped successfully, served the built HTML and
   referenced JavaScript, and returned `401` for unauthenticated `/api/jails`.
   The test used the fake adapter and port 18884.
4. `pkg add -f jaildeck-0.10.1.pkg` replaced the package and reported the new
   version. Checksums of the live configuration, real credential/token store,
   audit log and jail-data sentinel were unchanged. The ZFS snapshot retained
   its GUID; service enablement remained untouched and no service was started.
5. `pkg delete -y jaildeck` removed its registration and all three owned files.
   All operator-data checksums and the dataset/snapshot survived unchanged.

The test dataset is `zroot/jaildeck-package-test`, mounted at
`/usr/local/jails/package-test`, with snapshot `@preserve`. These fixtures and
the operator data intentionally remain in the disposable VM for inspection;
the application package was removed. Build prerequisites can be reinstalled
after this test without affecting the result.

The local artifacts are `bin/packages/jaildeck-0.10.0.pkg` and
`bin/packages/jaildeck-0.10.1.pkg`. Build, failure-path and lifecycle logs are
saved beside them under `bin/packages/validation/` in the working checkout;
generated packages and logs are not committed.

## Scope

Only FreeBSD 15.1 amd64 is exercised locally. The manifest declares the native
FreeBSD 15 ABI; this record does not establish testing on other minor releases
or architectures. Package hosting, signatures, Ports and CI publishing are not
part of this change.

The manual-install migration procedure is documented rather than automated;
no real manual deployment was replaced during these tests. No acceptance
criterion from #84 remains unverified on the tested FreeBSD 15.1 amd64 VM.
