# Jail Deck — Project Specification

## One-line description

Jail Deck is a lightweight, FreeBSD-first web dashboard for inspecting, operating, and creating FreeBSD jails through native system tools.

## Purpose

Jail Deck exists to make day-to-day jail administration more practical and pleasant without hiding FreeBSD behind an alien abstraction. It helps an operator see what is running, understand how each jail is configured, perform common actions safely, create new jails from prepared templates, and inspect related system resources such as networking, storage, services, logs, and snapshots.

The project should feel like a thin, helpful layer over FreeBSD rather than a separate platform.

## Origin and inspiration

Jail Deck is a personal project built to develop real Go and FreeBSD skills, not a product for general distribution. It is designed for a single operator's own use on their own host(s).

[Sylve](https://github.com/AlchemillaHQ/Sylve) is a more complete FreeBSD jail/VM management tool and serves as an inspiration, but Jail Deck is not trying to match it feature-for-feature. Features are added because they make sense for how Jail Deck is actually used, not for completeness — bhyve/VM management, for instance, is intentionally not a direction Jail Deck is pursuing.

## Product philosophy

### 1. FreeBSD first

Jail Deck is designed specifically for FreeBSD jails. It embraces FreeBSD terminology, file locations, service management, ZFS integration, and operational conventions.

It should not pretend that jails are Linux containers, virtual machines, or cloud workloads.

### 2. Integrate, do not replace

Jail Deck orchestrates and inspects existing tools instead of reimplementing them:

- `jls`, `jail`, `jexec`, `service`, `sysrc`, `pkg`
- `zfs`
- `fetch`, `freebsd-update`
- log files under `/var/log`
- `rc.conf` and jail configuration files

The UI should make native operations clearer, safer, and easier to repeat.

### 3. Minimal dependencies

Prefer the Go standard library and small, focused dependencies on the backend. Avoid runtime dependencies that complicate FreeBSD deployment unless they provide clear value.

### 4. Single binary as the default deployment model

The distribution model is a single Go binary with static assets embedded into it — including the built Vue frontend (see `docs/DECISIONS.md`, JD-009). Node.js is a build-time tool, never a production dependency.

Installation should eventually feel natural on FreeBSD, such as `pkg install jaildeck`, or `make install` during early development.

### 5. API-driven UI

The Vue frontend talks to the Go backend over a JSON API (JD-009).

### 6. Boring, domain-organized architecture

The application should be easy to understand, build, run, and debug. Code is organized by domain (jails, storage) rather than by technical layer — see `docs/ARCHITECTURE.md`. A contributor should be able to open a domain's package and see its whole story in one place.

### 7. Observable and explainable

Jail Deck should show what happened, what command or operation was attempted, whether it succeeded, and what the user can do next when something fails.

Errors should be understandable, not cryptic wrappers around command output.

### 8. Recoverable operations

Operations that modify the system should be designed with safety in mind. When possible, Jail Deck should detect risk, confirm destructive actions, preserve logs, and leave the system in a known state after failures. This matters for jail *creation* (ZFS clone, config generation, template preparation) as much as jail *operation*.

## Primary user

A single technical operator running Jail Deck on FreeBSD host(s) they administer directly. The product should remain useful to any technical FreeBSD operator who wants a practical local admin interface, but it is not designed for a broader or less technical audience.

## What Jail Deck is

- a web dashboard and control surface for FreeBSD jail lifecycle and creation
- a local or LAN-facing admin tool for a single operator
- a thin interface over native FreeBSD and ZFS tools
- a Go backend with a Vue frontend, shipped as one binary
- an operational UI for jails, storage, templates, logs, services, and system state
- a project that values clarity over automation magic, and real invariants over speculative flexibility

## What Jail Deck is not

- a replacement for FreeBSD jails
- a new container runtime
- a virtualization/bhyve platform
- a Kubernetes-like orchestrator
- a cloud management platform
- a multi-host or multi-tenant platform
- a full configuration management system
- a mandatory abstraction over FreeBSD concepts

## Baseline technical choices

| Layer | Choice | Notes |
| --- | --- | --- |
| Backend language | Go | Static binary, good fit for system tooling. |
| HTTP router | Chi | Small, idiomatic, compatible with `net/http`. |
| API style | JSON over HTTP | Consumed by the Vue frontend. |
| Frontend framework | Vue | Richer client-side interaction than HTMX fragment-swapping. |
| Frontend build | Node/Vite at build time only | Output embedded via Go `embed`; no Node in production. |
| Storage backend | ZFS (required) | Jail roots and templates live in ZFS datasets; clone-from-snapshot is the jail creation mechanism. |
| Operation audit log | Append-only JSON-lines file | A database is a likely future direction for broader persistence — see `docs/DECISIONS.md` JD-011. |

## Core domains

- Jails (list, inspect, start/stop/restart)
- Jail creation (clone template → configure → start → provision)
- Release templates (fetch/extract/patch/update/snapshot a FreeBSD userland per version)
- Storage / ZFS (datasets, clones, snapshots)
- Services inside jails (`jexec`-based inspection/provisioning)
- Networking
- Logs
- Operation history / audit
- Settings

These domains guide code organization (`docs/ARCHITECTURE.md`) and frontend navigation.

## Implementation status

**Implemented:** jail listing (merging `jls` and `jail.conf*`), start/stop/restart, operation audit log, and the domain-driven package structure (`docs/ARCHITECTURE.md`) that the rest of this scope is built on.

**Planned, in priority order:** converting the jails and audit endpoints to a JSON API with a minimal Vue UI, while they're still the only two domains/handlers to convert; a ZFS storage domain (create/clone/snapshot/list datasets and snapshots); release template lifecycle (fetch, extract, patch, `freebsd-update`, snapshot); jail creation (clone a template, generate `jail.conf.d/<name>.conf`, start, verify); in-jail provisioning. Each domain added after the API conversion is built API-first from the start. See `docs/ROADMAP.md` for the full phase breakdown.

### Explicitly deferred

- In-jail service management beyond manual `jexec` provisioning
- Thin jails, VNET jails
- Multi-host management, multi-user auth, role-based access
- Automatic edits to critical configuration files beyond what jail creation itself needs to write

## Design constraints

### No unsafe magic

Jail Deck should not silently modify system files, destroy datasets, remove snapshots, or execute broad commands without clear user intent. This is load-bearing once jail creation is implemented, since it involves real destructive-adjacent ZFS operations (clone, snapshot).

### No hiding native concepts

The UI may explain FreeBSD concepts, but it should not rename them into misleading generic terms. A jail is a jail. A dataset is a dataset. A snapshot is a snapshot.

### System state is the source of truth

Jail Deck should avoid inventing an internal desired-state model that drifts from reality. ZFS and `jail.conf*` remain authoritative for jail/dataset/snapshot state; persistence is for things FreeBSD itself doesn't track (operation history, eventually maybe cached metadata or app settings), not a shadow copy of system state.

## Open questions

These are genuinely undecided — track resolution in `docs/DECISIONS.md` as they settle.

### FreeBSD release support

Should Jail Deck support creating jails on a FreeBSD release other than the host's own version? Cross-version jails (older userland under a newer host kernel) are a normal, supported FreeBSD pattern, so this is expected to work, but is unverified in this project. If supported, should Jail Deck fetch and present the list of available releases from `download.freebsd.org`, or only support the host's current version initially?

### Long-running operations

Template preparation (fetching `base.txz`, `freebsd-update`) can take real time. Synchronous request/response may not be sufficient — see `docs/DECISIONS.md` JD-008.

### Storage domain granularity

Should ZFS datasets/snapshots and release templates live in one `storage` package, or should templates be split into their own bounded context given their distinct multi-step lifecycle?

### Structured application logging

A structured, leveled application logger, distinct from the operation-history audit log, is a likely future addition. Not designed yet.

### Packaging

How soon should FreeBSD packaging (`pkg install jaildeck`) be considered? Likely: keep the repo package-friendly, don't block on a formal port.
