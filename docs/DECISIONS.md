# Jail Deck — Architecture Decisions

This document records architectural decisions that have been intentionally locked for the project.

Its purpose is to avoid repeatedly revisiting the same topics and to provide a stable foundation for future development. Each decision is identified by a unique ID.

---

## JD-001 — Runtime Privileges

**Status:** Locked

Jail Deck runs as **root** (in practice, launched via `doas jaildeck`).

### Rationale

Jail Deck is a system administration tool whose responsibilities require elevated privileges — jail lifecycle, ZFS dataset/snapshot management, and in-jail provisioning via `jexec` all need root. Running as root keeps the architecture simple and avoids a privileged-helper-process split.

---

## JD-002 — Binding Model

**Status:** Locked

The default bind address is:

```
127.0.0.1
```

Jail Deck manages jails on the host it runs on, not a fleet of machines — that is a scope statement, not an access restriction. The bind address is configurable (`JAILDECK_HOST`), and operators may bind to another interface or `0.0.0.0` at their own risk — for example to reach the dashboard over the LAN during development.

### Rationale

Defaulting to local-only access eliminates authentication/authorization/TLS concerns for the common case, without blocking operators who knowingly need broader access.

---

## JD-003 — Authentication

**Status:** Locked for now — revisit if remote/multi-user access becomes real

Jail Deck implements **no authentication**.

### Rationale

The application is reachable only through localhost by default (JD-002). Authentication would add real implementation cost for little benefit at the current single-operator, single-host scope. This will need to change if Jail Deck is ever exposed beyond a trusted LAN or gains multiple users.

---

## JD-004 — Jail Discovery

**Status:** Locked

Jail Deck discovers jails through two sources, merged:

```
jls                                      # running jails, authoritative for status
/etc/jail.conf, /etc/jail.conf.d/*.conf  # configured jails, including stopped ones
```

A jail present in configuration but not in `jls` is shown as stopped; a jail present in `jls` but not found in configuration is still shown (with a warning logged). A missing `/etc/jail.conf` is treated as "zero jails configured there," not an error — jail definitions may live entirely under `jail.conf.d/` with no top-level file present.

### Rationale

An operator needs to see a stopped jail in order to start it, so configuration parsing is a read path even before config editing exists.

---

## JD-005 — Jail Lifecycle Operations

**Status:** Locked

Starting, stopping, and restarting an existing jail uses FreeBSD's standard service interface:

```
service jail start <name>
service jail stop <name>
service jail restart <name>
```

### Rationale

`service` is the standard administrative interface FreeBSD operators already know. Building on it keeps behavior predictable instead of reimplementing jail lifecycle management at a lower level.

---

## JD-006 — Storage Model

**Status:** Locked

Jail Deck requires **ZFS**. Jail root directories live inside ZFS datasets, and jail creation is built directly on ZFS primitives:

- a **release template** is a dataset holding a fetched, patched, updated FreeBSD userland, snapshotted once ready (`zfs snapshot pool/jails/templates/<release>@base`)
- a **new jail** is created by cloning that snapshot (`zfs clone pool/jails/templates/<release>@base pool/jails/containers/<name>`)

Support for UFS or plain-directory jail roots is out of scope.

### Rationale

ZFS clone-from-snapshot is what makes jail creation fast and cheap — copy-on-write, not a full userland copy per jail.

---

## JD-007 — Jail Creation via Template Cloning

**Status:** Locked

Creating a jail is a two-stage process:

1. **Release template preparation** (once per FreeBSD version): create a dataset, fetch `base.txz` for that release, extract it, patch in host `resolv.conf`/`localtime`, bring it to the current patch level with `freebsd-update`, snapshot it as `@base`. A template is only usable once its `@base` snapshot exists.
2. **Jail creation** (per jail, requires a ready template): clone the template snapshot into the jail's own dataset, generate the `jail.conf.d/<name>.conf` stanza, start the jail, and hand off to in-jail provisioning (`jexec` + `pkg`) for service-specific setup.

### Rationale

Templates are shared, immutable-once-snapshotted bases; jails are cheap clones of them. This keeps jail creation fast and avoids re-fetching/re-patching a userland per jail.

---

## JD-008 — Long-running Operations

**Status:** Open

Fetching `base.txz`, extracting it, and running `freebsd-update` can take real time. Whether this needs task queues, polling, or Server-Sent Events, versus staying synchronous with a longer timeout, is not yet decided.

### Rationale

Worth deciding deliberately once template preparation is actually being built, rather than guessing ahead of the real shape of the problem.

---

## JD-009 — Frontend: Vue SPA

**Status:** Locked

Jail Deck's UI is a **Vue** single-page application. The backend exposes a JSON API; handlers do not render HTML fragments.

Production still ships as a single Go binary: the Vue build output is embedded via `embed.FS`, the same way templates and static assets were previously. Node.js is a build-time dependency only, never a production one.

### Rationale

The growing feature set (templates, ZFS operations, jail creation flows) benefits from richer client-side interaction than server-rendered HTML fragment-swapping comfortably provides. The migration happens early — while `jails` and the audit log are the only existing domains, there is a small, well-understood surface to convert. Every domain added afterward (storage, templates) is built API-first from the start, avoiding a larger migration later — see `docs/ROADMAP.md`.

---

## JD-010 — Code Organization: Domain-Driven Packages

**Status:** Locked

Code is organized by **bounded context**, not by technical layer. Each domain package owns its entity/value-object types, its repository interface (port), its application use cases, and its HTTP handler — there is no separate presentation-layer package:

```
internal/
  jails/      # Jail entity, JailName value object, JailSystem port, JailService, JailHandler
  storage/    # Dataset/Snapshot/Template entities, port, service, handler
  audit/      # operation history log — shared infrastructure, not a domain
  common/     # AppError + HandlerError — shared HTTP error translation, used by every domain's handler
  system/
    command.go, exec_runner.go   # CommandRunner primitives
    freebsd/                      # adapter implementing jails.JailSystem
    fake/                          # in-memory adapter implementing jails.JailSystem, for non-FreeBSD dev
```

Domain package names are **plural** (`jails`, and `storage` etc. as they arrive). Repository ports are defined by the domain package that needs them (e.g. `jails.JailSystem` in `internal/jails/repository.go`) and speak in that domain's own types — not in a type owned by the infrastructure package that implements them. Adapters (`system/freebsd`, `system/fake`) depend on the domain package whose port they implement, never the reverse.

There is no separate `internal/handlers` package: a domain's HTTP handler lives in its own package alongside its entity, port, and service.

### Rationale

Organizing by domain keeps each bounded context's rules, contract, and orchestration logic in one place instead of spread across several folders, and scales cleanly as new domains (storage) are added. `internal/system`'s execution primitives (`Command`, `CommandResult`, `CommandRunner`, `CommandError`) are the one deliberate exception — they are domain-agnostic and correctly live outside any domain package.

---

## JD-011 — Operation History

**Status:** Locked for now — persistence direction is open, see below

Every mutating jail operation is recorded in an append-only JSON-lines log file (`internal/audit`): timestamp, operation, target, command, exit code, success/failure, error summary.

**Open:** a structured application logger distinct from this audit trail, and a move to database-backed persistence for operation history (and eventually template/dataset/jail records as those domains grow state worth tracking beyond what ZFS/`jail.conf` already hold) are anticipated directions. Neither is scheduled or designed.

### Rationale

The flat-file audit log works and nothing forces a change today. Flagging the direction here so it isn't lost, without prematurely picking a database or schema before the domains that would need it exist in code.

---

## JD-012 — Native Integration

**Status:** Locked

Jail Deck integrates existing FreeBSD facilities rather than replacing them:

```
service · jls · jail · jexec · zfs · pkg · fetch · freebsd-update · sysrc
```

### Rationale

Jail Deck is an orchestration and visualization layer built on top of FreeBSD, not an alternative implementation of its administration tools.

---

## JD-013 — Readability Over Cleverness

**Status:** Locked

The project favors explicit, predictable, maintainable implementations over clever abstractions: standard library before external dependencies, small focused libraries, straightforward project organization.

### Rationale

Long-term maintainability is valued over short-term convenience. This is also why the domain-driven organization (JD-010) is scoped to the domains the code has actually needed — not speculative bounded-context ceremony ahead of real requirements.

---

## Decision Policy

Once a decision is marked as **Locked**, it should not be revisited without a clear technical or product justification. Architectural evolution is expected, but changes should be deliberate and documented rather than occurring through incremental drift.
