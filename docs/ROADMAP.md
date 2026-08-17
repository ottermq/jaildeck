# Jail Deck — Roadmap

Capability-based rather than date-based.

## Roadmap philosophy

Grow from visibility to safe operation to controlled management. Don't edit critical system configuration or invent complex abstractions before the code has shown a real need for them. This is why the domain-driven organization (`docs/ARCHITECTURE.md`) introduces only the domains actually needed — `jails`, and `storage` next — instead of speculative bounded contexts.

## Implemented

- HTTP server, Chi routing
- Jail listing, merging `jls` (running) with `jail.conf`/`jail.conf.d` (configured but stopped)
- Jail start/stop/restart via `service jail <action> <name>`, idempotent (start/stop on a jail already in the desired state succeeds rather than erroring)
- Append-only operation audit log (`internal/audit`)
- Domain-driven package structure (`internal/jails`, `internal/audit`, `internal/common`, `internal/system` + adapters) — see `docs/ARCHITECTURE.md`
- JSON API for `internal/jails` and `internal/audit`; server-rendered HTML, `internal/views`, and `web/templates` are fully removed — the backend is JSON-only (Phase 1a below)
- Typed error mapping (`common.AppError`) from adapter-level failures to HTTP status codes, e.g. jail-not-found → 404, extensible to further typed errors without changing the translation boundary

## Phase 1a — JSON API for jails and audit — done

Goal: convert the jails and audit (operations) endpoints from server-rendered HTML to a JSON API, while these are still the only two handlers in the codebase. Every domain added afterward (storage, templates) is then built API-first from the start, avoiding a larger migration later.

### Capabilities

- jails list, start/stop/restart, and operation history available as a JSON API

### Technical tasks

- settle the JSON API shape for `internal/jails` and `internal/audit` (see `docs/ARCHITECTURE.md` "API conventions")
- convert `JailHandler` and `internal/audit`'s handler to serve JSON instead of HTML fragments
- retire `internal/views` and the HTML templates

### Completion criteria

Jails and operations are served over the JSON API; no HTML-fragment rendering remains in the backend. Met.

## Phase 1b — Minimal Svelte UI

Goal: build a minimal Svelte UI consuming the JSON API from Phase 1a, while jails/audit are still the only two domains to build a UI against. Every domain added afterward is then built against this same frontend pattern from the start.

### Capabilities

- a minimal Svelte UI: jails list with start/stop/restart actions and operation result feedback, plus an operations/history view

### Technical tasks

- stand up the Svelte project + build pipeline, `embed.FS` the build output
- minimal Svelte app covering the jails list and operations views

### Completion criteria

Jails and operations are served by the Svelte app over the JSON API from Phase 1a. The backend currently has no browser UI at all; this phase is what closes that gap.

## Phase 2 — ZFS storage domain

Goal: build the `storage` domain's foundation — dataset/snapshot primitives — since both template preparation and jail creation depend on it.

### Capabilities

- create a dataset
- clone a dataset from a snapshot
- create a snapshot
- list datasets / list snapshots

### Technical tasks

- define `Dataset`, `Snapshot` entities and the `StorageSystem` port in `internal/storage`
- implement `storage_adapter.go` in `internal/system/freebsd` (`zfs create/clone/snapshot/list`)
- parser/fixture tests for `zfs list` output, same pattern as the existing `jls` parser tests
- `storage.Service` with a fake repository implementation, unit tested without touching real ZFS
- expose the domain through the JSON API and minimal Svelte UI established in Phase 1a/1b

### Completion criteria

The app can create/clone/snapshot/list ZFS datasets programmatically, through the API and UI.

## Phase 3 — Release template lifecycle

Goal: automate the template-preparation workflow described in `docs/SPEC.md`.

### Capabilities

- create a template dataset for a FreeBSD release
- fetch that release's `base.txz`
- extract it into the template dataset
- patch in host `resolv.conf`/`localtime`
- bring the template to the current patch level via `freebsd-update`
- snapshot the template as `@base`

### Technical tasks

- `Template` entity in `internal/storage` with lifecycle state (created -> fetched -> extracted -> patched -> updated -> ready)
- extend `StorageSystem` (or a sibling port) with fetch/extract/patch/update operations
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

- decide orchestration ownership across `jails`/`storage` (see `docs/ARCHITECTURE.md` open question)
- `jail.conf.d` config generation (likely `text/template`, not string concatenation)
- handle service-specific config needs (e.g. database servers that need extra stanzas) — exact mechanism (profiles? post-create hooks?) not yet designed
- partial-failure handling: a failed config write after a successful clone shouldn't leave an undiagnosable half-created jail

### Completion criteria

A new jail can be created end-to-end (clone -> configure -> start) without manually touching `zfs` or `jail.conf.d`.

## Phase 5 — In-jail provisioning

Goal: reduce (not necessarily eliminate) the manual `jexec ... ; pkg update` step.

### Capabilities

- run a command inside a jail via `jexec`
- at minimum, trigger `pkg update`/`pkg install <pkg>` after creation
- service-specific provisioning as a distinct, later capability

### Technical tasks

- `jexec` wrapped through the existing `CommandRunner` pattern
- decide how much of "configure the service" is automated vs. left as a documented manual step per service type

### Completion criteria

A freshly created jail can have baseline packages installed without a manual `jexec` session, for at least the common case.

## Phase 6 — Logging and persistence evolution

Goal: a structured application logger distinct from the operations audit log, and persistence beyond flat files where justified.

### Capabilities

- structured application logging, separate from the operation-history audit trail
- a real database, if by this point there's a concrete justification (template/dataset metadata, richer operation history, etc.) — not adopted speculatively

### Technical tasks

- not designed yet — this phase starts with a design discussion, not implementation

### Completion criteria

To be defined at design time.

## Phase 7 — Packaging and distribution

Goal: make Jail Deck feel native to install and operate on FreeBSD.

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

Applies across every phase: keep docs current as domains land, keep parser/fixture and service-level tests passing, keep reviewing privilege boundaries and destructive-action confirmation as storage/template/creation features add real teeth.

## Current highest-priority open questions

1. Who orchestrates the multi-step jail-creation flow across the `jails`/`storage` boundary?
2. How should long-running template operations (fetch, `freebsd-update`) be represented — sync with a long timeout, polling, task queue, SSE?
3. Is cross-FreeBSD-version jail support in scope soon, and does that imply a release picker sourced from `download.freebsd.org`?
4. How much in-jail provisioning gets automated vs. documented as a manual step, per service type?
5. Exact JSON API shape for jails/audit — being settled in Phase 1.
