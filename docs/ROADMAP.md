# Jail Deck — Roadmap

## Status

Draft. Reset 2026-08-16. Still capability-based rather than date-based. The original Phase 0–8 sequence (research -> skeleton -> read-only visibility -> safe operations -> logs -> storage visibility -> in-jail services -> safer management -> packaging) is superseded: several of those phases already shipped (skeleton, read-only visibility, safe start/stop/restart, operation history), and the remaining shape of the project changed enough that re-sequencing from here made more sense than patching the old list. See `git log docs/ROADMAP.md` for the previous version.

## Roadmap philosophy

Unchanged: grow from visibility to safe operation to controlled management. Don't edit critical system configuration or invent complex abstractions before the code has shown a real need for them. This is why the domain-driven refactor (Phase 1 below) only introduces two domains — `jail` and `storage` — instead of speculative bounded contexts.

## Already shipped (pre-reset)

- HTTP server, Chi routing, embedded templates/static assets
- Jail listing, merging `jls` (running) with `jail.conf`/`jail.conf.d` (configured but stopped)
- Jail start/stop/restart via `service jail <action> <name>`
- Append-only operation history log (`internal/operations`, being renamed `internal/audit`)
- Operations page with filtering

This is the code the current refactor must not break.

## Phase 1 — Domain-driven refactor (in progress, branch `refactor/ddd`)

Goal: reshape the existing, working jail-management code into the new package structure with no behavior change, so new domains (storage) can be built in that shape from day one instead of being retrofitted later.

### Capabilities

No new user-facing capability — this phase is purely structural.

### Technical tasks

- move `Jail`, `JailStatus` into `internal/jail`, alongside a `JailRepository` port and application service
- flip `system.JailSystem` into `jail.JailRepository`, owned by the domain package
- make `freebsd.Adapter` implement `jail.JailRepository`
- rename `internal/operations` -> `internal/audit`
- turn `validJailName` into a `JailName` value object with validation at construction
- keep `internal/handlers` and `internal/views` working against the reshaped services throughout
- `go test ./...` stays green at every step

### Completion criteria

Jail listing and start/stop/restart work exactly as before, but the code is organized as `internal/jail` (entity + port + service) instead of spread across `domain/`, `services/`, `system/`.

## Phase 2 — ZFS storage domain

Goal: build the `storage` domain's foundation — dataset/snapshot primitives — since both template preparation and jail creation depend on it.

### Capabilities

- create a dataset
- clone a dataset from a snapshot
- create a snapshot
- list datasets / list snapshots

### Technical tasks

- define `Dataset`, `Snapshot` entities and the `StorageRepository` port in `internal/storage`
- implement `storage_adapter.go` in `internal/system/freebsd` (`zfs create/clone/snapshot/list`)
- parser/fixture tests for `zfs list` output, same pattern as the existing `jls` parser tests
- `storage.Service` with fakeable repository, unit tested without touching real ZFS

### Completion criteria

The app can create/clone/snapshot/list ZFS datasets programmatically, verified against isengard.local.

## Phase 3 — Release template lifecycle

Goal: automate the manual template-preparation workflow (see `docs/SPEC.md`).

### Capabilities

- create a template dataset for a FreeBSD release
- fetch that release's `base.txz`
- extract it into the template dataset
- patch in host `resolv.conf`/`localtime`
- bring the template to the current patch level via `freebsd-update`
- snapshot the template as `@base`

### Technical tasks

- `Template` entity in `internal/storage` with lifecycle state (created -> fetched -> extracted -> patched -> updated -> ready)
- extend `StorageRepository` (or a sibling port) with fetch/extract/patch/update operations
- resolve JD-008 (long-running operations) enough to actually run `freebsd-update` from a request without a naive indefinite blocking call
- resolve the open question on release version normalization (strip `-pN` from `freebsd-version` for the fetch URL) and, if in scope this phase, cross-version support / fetching the available release list from `download.freebsd.org`

### Completion criteria

A template for a given FreeBSD release can be prepared end-to-end from the UI or API, ending in a usable `@base` snapshot, without manual shell steps.

## Phase 4 — Jail creation

Goal: create a new jail from a ready template.

### Capabilities

- confirm a template's `@base` snapshot exists (precondition)
- clone the snapshot into the jail's own dataset
- generate `jail.conf.d/<name>.conf`
- start the jail
- surface clear, step-by-step failure state if any stage fails

### Technical tasks

- decide orchestration ownership across `jail`/`storage` (see `docs/ARCHITECTURE.md` open question)
- `jail.conf.d` config generation (likely `text/template`, not string concatenation)
- handle service-specific config needs (e.g. the Postgres case Andre mentioned) — exact mechanism (profiles? post-create hooks?) not yet designed
- partial-failure handling: a failed config write after a successful clone shouldn't leave an undiagnoseable half-created jail

### Completion criteria

A new jail can be created end-to-end (clone -> configure -> start) without manually touching `zfs` or `jail.conf.d`.

## Phase 5 — In-jail provisioning

Goal: reduce (not necessarily eliminate) the manual `jexec ... ; pkg update` step.

### Capabilities

- run a command inside a jail via `jexec`
- at minimum, trigger `pkg update`/`pkg install <pkg>` after creation
- service-specific provisioning (e.g. Postgres) as a distinct, later capability

### Technical tasks

- `jexec` wrapped through the existing `CommandRunner` pattern
- decide how much of "configure the service" is automated vs. left as a documented manual step per service type

### Completion criteria

A freshly created jail can have baseline packages installed without a manual `jexec` session, for at least the common case.

## Phase 6 — Vue frontend migration

Goal: replace server-rendered HTML/HTMX with the Vue SPA + JSON API (JD-009). Deliberately sequenced after Phases 1–5 so the API is designed against domains that actually exist, not guessed ahead of them.

### Capabilities

- all existing and newly-added capabilities (jails, storage, templates, jail creation, operations) available through a JSON API
- Vue SPA consuming that API, replacing every HTMX page/fragment

### Technical tasks

- settle the JSON API shape (see `docs/ARCHITECTURE.md` "API conventions")
- stand up the Vue project + build pipeline, `embed.FS` the build output
- port each existing page (jails list, operations) to the SPA before adding new ones
- retire `internal/views` and the HTMX templates once parity is reached

### Completion criteria

The HTMX/server-rendered UI is fully replaced; nothing in production depends on Node.js.

## Phase 7 — Logging and persistence evolution

Goal: address the two things flagged during the August 2026 debugging session — a real structured application logger distinct from the operations audit log, and persistence beyond flat files where justified.

### Capabilities

- structured application logging (Serilog-like), separate from the operation-history audit trail
- a real database, if by this point there's a concrete justification (template/dataset metadata, richer operation history, etc.) — not adopted speculatively

### Technical tasks

- not designed yet — this phase starts with a design discussion, not implementation

### Completion criteria

TBD at design time.

## Phase 8 — Packaging and distribution

Goal: make Jail Deck feel native to install and operate on FreeBSD. Unchanged from the original roadmap.

### Capabilities

- install binary, rc.d service script, default config
- document safe deployment modes
- provide an upgrade path
- prepare FreeBSD package/port work

### Completion criteria

Jail Deck can be installed on FreeBSD in a repeatable way and run as a service.

## Not planned

- multi-host management
- cluster orchestration
- bhyve/VM management
- complex user/team permissions
- full terminal emulator
- broad configuration management beyond what jail creation itself needs
- plugin system
- production Node.js dependency
- remote cloud control plane

## Cross-cutting work

Applies across every phase: keep docs current as domains land (don't let this reset go stale the way the old docs did), keep parser/fixture and service-level tests passing, keep reviewing privilege boundaries and destructive-action confirmation as storage/template/creation features add real teeth.

## Current highest-priority open questions

1. Who orchestrates the multi-step jail-creation flow across the `jail`/`storage` boundary?
2. How should long-running template operations (fetch, `freebsd-update`) be represented — sync with a long timeout, polling, task queue, SSE?
3. Is cross-FreeBSD-version jail support in scope soon, and does that imply a release picker sourced from `download.freebsd.org`?
4. How much in-jail provisioning gets automated vs. documented as a manual step, per service type?
5. What does the JSON API actually look like, once Phase 6 starts?
