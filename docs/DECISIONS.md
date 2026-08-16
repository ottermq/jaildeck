# DECISIONS.md

# Jail Deck — Architecture Decisions

This document records architectural decisions that have been intentionally locked for the project.

Its purpose is to avoid repeatedly revisiting the same topics and to provide a stable foundation for future development.

**Status note (2026-08-16):** This document was reset. The previous version (JD-001..JD-012) is superseded — see `git log docs/DECISIONS.md` for that history if needed. Jail Deck's scope grew substantially past the original MVP (jail creation, FreeBSD release templates, ZFS storage management, a Vue frontend) and the project is mid-refactor on the `refactor/ddd` branch to a domain-driven package structure. Decisions below that carry over from the old MVP unchanged are noted as such; several are genuinely new.

Each decision is identified by a unique ID.

---

## JD-001 — Runtime Privileges

**Status:** Locked (carried over, unchanged)

Jail Deck runs as **root** (in practice, launched via `doas jaildeck`).

### Rationale

Jail Deck is a system administration tool whose responsibilities require elevated privileges — jail lifecycle, ZFS dataset/snapshot management, and in-jail provisioning via `jexec` all need root. Running as root keeps the architecture simple and avoids a privileged-helper-process split.

---

## JD-002 — Binding Model

**Status:** Locked (carried over, unchanged)

The default bind address is:

```
127.0.0.1
```

Jail Deck manages jails on the host it runs on, not a fleet of machines — that is a scope statement, not an access restriction. The bind address is configurable (`JAILDECK_HOST`), and operators may bind to another interface or `0.0.0.0` at their own risk (e.g. `192.168.1.7` on isengard.local for LAN access during development).

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

**Status:** Locked (carried over, unchanged)

Jail Deck discovers jails through two sources, merged:

```
jls                                      # running jails, authoritative for status
/etc/jail.conf, /etc/jail.conf.d/*.conf  # configured jails, including stopped ones
```

A jail present in configuration but not in `jls` is shown as stopped; a jail present in `jls` but not found in configuration is still shown (with a warning logged). A **missing `/etc/jail.conf`** is treated as "zero jails configured there," not an error — some hosts (isengard.local included) keep all jail definitions under `jail.conf.d/` and never create the top-level file.

### Rationale

An operator needs to see a stopped jail in order to start it, so configuration parsing is a read path even before config editing exists. Treating a missing `jail.conf` as an error rather than an empty result was a real bug (500 on `/jails`), fixed 2026-08-16.

---

## JD-005 — Jail Lifecycle Operations

**Status:** Locked (carried over, unchanged)

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

**Status:** Locked (carried over; now actively implemented rather than aspirational)

Jail Deck requires **ZFS**. Jail root directories live inside ZFS datasets, and jail creation is built directly on ZFS primitives:

- a **release template** is a dataset holding a fetched, patched, updated FreeBSD userland, snapshotted once ready (`zfs snapshot pool/jails/templates/<release>@base`)
- a **new jail** is created by cloning that snapshot (`zfs clone pool/jails/templates/<release>@base pool/jails/containers/<name>`)

Support for UFS or plain-directory jail roots remains out of scope.

### Rationale

ZFS clone-from-snapshot is what makes jail creation fast and cheap (copy-on-write, not a full userland copy per jail), and is the workflow already in use manually on isengard.local. See `docs/SPEC.md` for the full template/jail creation workflow this formalizes.

---

## JD-007 — Jail Creation via Template Cloning

**Status:** Locked (new)

Creating a jail is a two-stage process:

1. **Release template preparation** (once per FreeBSD version): create a dataset, fetch `base.txz` for that release, extract it, patch in host `resolv.conf`/`localtime`, bring it to the current patch level with `freebsd-update`, snapshot it as `@base`. A template is only usable once its `@base` snapshot exists.
2. **Jail creation** (per jail, requires a ready template): clone the template snapshot into the jail's own dataset, generate the `jail.conf.d/<name>.conf` stanza, start the jail, and hand off to in-jail provisioning (`jexec` + `pkg`) for service-specific setup.

### Rationale

This formalizes Andre's existing manual process (see `docs/SPEC.md`) instead of inventing a new one. Templates are shared, immutable-once-snapshotted bases; jails are cheap clones of them.

---

## JD-008 — Long-running Operations

**Status:** Open — reopened from the old MVP's "synchronous only" decision

The old MVP assumption (synchronous request/response for every operation) no longer clearly holds: fetching `base.txz`, extracting it, and running `freebsd-update` can take real time. Whether this needs task queues, polling, or Server-Sent Events, versus staying synchronous with a longer timeout for now, is not yet decided.

### Rationale

Worth deciding deliberately once template preparation is actually being built, rather than guessing ahead of the real shape of the problem.

---

## JD-009 — Frontend: Vue SPA

**Status:** Locked (new — supersedes the old HTMX + server-rendered decision)

Jail Deck's UI moves from server-rendered HTML + HTMX to a **Vue** single-page application. The backend exposes a JSON API; handlers stop rendering HTML fragments for new work.

Production still ships as a single Go binary: the Vue build output is embedded via `embed.FS`, the same way templates and static assets are today. Node.js is a build-time dependency only, never a production one.

### Rationale

Andre is more familiar with Vue than the alternative considered (Svelte), and the growing feature set (templates, ZFS operations, jail creation flows) benefits from richer client-side interaction than HTMX fragment-swapping comfortably provides. Decided 2026-08-16; sequenced *after* the backend domain refactor and new domains (storage/templates/jail creation) land — see `docs/ROADMAP.md`. Existing HTMX routes keep working until the migration phase actually starts; don't build new features against a hypothetical future API shape in the meantime.

---

## JD-010 — Code Organization: Domain-Driven Packages

**Status:** Locked (new — supersedes the old layer-based `domain/`, `services/`, `system/` split). Package layout below reflects what actually landed on `refactor/ddd`, not the original proposal — see notes after the tree.

Code is organized by **bounded context**, not by technical layer. Each domain package owns its entity/value-object types, its repository interface (port), its application use cases, *and* its HTTP handler — there is no separate presentation-layer package:

```
internal/
  jails/      # Jail entity, JailName value object, JailSystem port, JailService, JailHandler
  storage/    # (not yet built) Dataset/Snapshot/Template entities, port, service, handler
  audit/      # operation history log (was internal/operations) — shared infrastructure, not a domain
  common/     # AppError + HandlerError — shared HTTP error translation, used by every domain's handler
  system/
    command.go, exec_runner.go, models.go   # CommandRunner primitives; Jail/JailStatus (see note)
    freebsd/                                 # adapter implementing jails.JailSystem
  views/       # (retired once the Vue migration, JD-009, lands)
```

Domain package names are **plural** (`jails`, and `storage`/`audit` etc. as they arrive) — a deliberate convention, not an inconsistency.

Repository ports are defined by the domain package that needs them (e.g. `jails.JailSystem` in `internal/jails/repository.go`), not by the infrastructure package that implements them.

### Rationale

The codebase outgrew "package by layer" once a second real domain (storage) was on the horizon with its own invariants (e.g. "cloning requires a `@base` snapshot to exist"). Organizing by domain keeps each bounded context's rules, contract, and orchestration logic in one place instead of spread across four folders. See `docs/ARCHITECTURE.md` for the full layout.

Two deviations from the original proposal, both Andre's deliberate calls: no separate `internal/handlers` package (folding the handler into the domain package went further toward "everything about Jails in one place" than initially proposed, and there was no real need for a presentation-layer package split for a single-handler domain); and `jails` (plural) as the naming convention for domain packages going forward.

**Known follow-up, not yet done:** `jails.JailSystem` currently returns `system.Jail` (defined in `internal/system/models.go`), and `JailService` maps that into its own `jails.Jail` on every call (`mapSystemToDomainJail`). This has the dependency direction backwards — the port should speak in the domain's own vocabulary, and the adapter (which already depends on `internal/jails` to implement the port) should build `jails.Jail` directly. Planned fix: move `Jail`/`JailStatus` into `internal/jails`, delete `system.Jail`, let `system/freebsd` and `system/fake.go` construct `jails.Jail` directly. `internal/system`'s execution primitives (`Command`, `CommandResult`, `CommandRunner`, `CommandError`) are unaffected — those are genuinely domain-agnostic and correctly live outside any domain package.

---

## JD-011 — Operation History

**Status:** Locked for now (carried over; storage direction is open, see below)

Every mutating jail operation is still recorded in an append-only JSON-lines log file (`internal/audit`, formerly `internal/operations`), same shape as before: timestamp, operation, target, command, exit code, success/failure, error summary.

**Open:** Andre wants a real structured application logger (Serilog-style) distinct from this audit trail, and expects persistence in general to move to a real database eventually rather than flat files — for operation history, but likely also for template/dataset/jail records as those domains grow state worth tracking beyond what ZFS/`jail.conf` already hold. Neither is scheduled or designed yet.

### Rationale

The flat-file audit log still works and nothing forces a change today. Flagging the direction here so it isn't lost, without prematurely picking a database or schema before the domains that would need it (storage, templates) exist in code.

---

## JD-012 — Native Integration

**Status:** Locked (carried over, unchanged, now broader in practice)

Jail Deck integrates existing FreeBSD facilities rather than replacing them:

```
service · jls · jail · jexec · zfs · pkg · fetch · freebsd-update · sysrc
```

### Rationale

Jail Deck is an orchestration and visualization layer built on top of FreeBSD, not an alternative implementation of its administration tools. The set of tools grew (`jexec`, `fetch`, `freebsd-update`) as jail creation and provisioning entered scope, but the principle is unchanged.

---

## JD-013 — Readability Over Cleverness

**Status:** Locked (carried over, unchanged)

The project favors explicit, predictable, maintainable implementations over clever abstractions: standard library before external dependencies, small focused libraries, straightforward project organization.

### Rationale

Long-term maintainability is valued over short-term convenience. This principle is also why the domain-driven refactor (JD-010) is scoped to what the code has actually shown a need for — two real domains — rather than speculative bounded-context ceremony.

---

## Decision Policy

Once a decision is marked as **Locked**, it should not be revisited without a clear technical or product justification. Architectural evolution is expected, but changes should be deliberate and documented — as this reset itself was — rather than occurring through incremental drift.
