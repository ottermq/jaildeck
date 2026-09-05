# Jail Deck — Roadmap

Capability-based rather than date-based.

## Scope

Jail Deck is a single-host operations dashboard for FreeBSD jails, covering the full native jail lifecycle — template preparation, creation, lifecycle control, destruction, and observability — built on `jls`, `service`, `jail.conf`, and `zfs`.

Not bhyve. Not multi-host. Not OCI.

## Roadmap philosophy

Grow from visibility to safe operation to controlled management. Don't edit critical system configuration or invent complex abstractions before the code has shown a real need for them. This is why the domain-driven organization (`docs/ARCHITECTURE.md`) introduces only the domains actually needed, instead of speculative bounded contexts.

The target is stated most plainly as: **make Jail Deck do what is already done by hand.** The manual procedure is the specification; automating it is what forces genuine understanding of each step.

## A note on milestone names

Earlier revisions of this document used numbered phases, and the numbers were reused across two generations of the roadmap — there were two different "Phase 2"s and two "Phase 7"s. Issues written against one generation silently pointed at the other.

**Milestones here are named, not numbered.** Order is expressed by this document, not encoded in the name, so a milestone can be resequenced without invalidating anything written against it.

## Implemented

- HTTP server, Chi routing
- Jail listing, merging `jls` (running) with `jail.conf`/`jail.conf.d` (configured but stopped)
- Jail start/stop/restart via `service jail <action> <name>`, idempotent
- Append-only operation audit log (`internal/audit`), with filtering
- Domain-driven package structure (`internal/jails`, `internal/audit`, `internal/common`, `internal/system` + adapters)
- JSON API for jails and audit; server-rendered HTML, `internal/views`, and `web/templates` fully removed — the backend is JSON-only
- Typed error mapping (`common.AppError`) from adapter failures to HTTP status codes

---

## Milestone: Access Control

**Depends on:** nothing. **Required by:** everything — authentication is unconditional (JD-003), so no deployment of Jail Deck is complete without it.

Goal: a single operator authenticates before reaching any part of the application, on every bind address including loopback.

### Capabilities

- mandatory single-operator authentication; no unauthenticated mode and no flag to disable it
- session login and logout from the browser
- optional long-lived API token for scripting the JSON API

### Technical tasks

- delete the `if ip == "" { ip = "0.0.0.0" }` fallback in `cmd/jaildeck/main.go` — the mechanism by which the intended bind default was previously lost
- opaque random session token, stored server-side, delivered as an `HttpOnly; Secure; SameSite=Strict` cookie; an identity endpoint for the SPA to branch on; logout deletes the server-side entry
- argon2id for the stored operator password
- **`jaildeck passwd` subcommand** — prompts on the tty with echo disabled, or reads from stdin with `--stdin` for unattended installs. The only writer of the credential; hashes with argon2id and writes the state file atomically with restrictive permissions. Requires root
- **refuse to start when no credential is configured**, with an error naming `jaildeck passwd`. Fail closed
- changing the credential invalidates live sessions. No password-recovery flow is needed — root on the host reruns `jaildeck passwd`
- middleware applied to every route except login and static assets; verify no handler is reachable without it
- warn at startup when bound non-loopback without TLS in front, without refusing — a reverse proxy cannot be detected
- document the reverse-proxy path for TLS and the SSH `LocalForward` path for ad-hoc access

### Completion criteria

No endpoint returns data or performs an action without a valid session or API token, on any bind address. There is no configuration in which Jail Deck starts unauthenticated — with no credential set it refuses to start at all.

The documented install sequence works end to end:

```sh
pkg install jaildeck
jaildeck passwd
sysrc jaildeck_enable=YES
service jaildeck start
```

---

## Milestone: Web UI

**Depends on:** Access Control for the login view; the rest can proceed in parallel. **This is the top gap** — the backend currently has no browser interface at all, since the HTML was removed before its replacement was built.

Goal: a Svelte SPA consuming the existing JSON API, while jails and audit are still the only two domains to build against. Every domain added afterwards is then built against an established frontend pattern.

### Capabilities

- jails list with start/stop/restart and operation result feedback
- operations/history view with the existing filters
- a login view, and session expiry handled gracefully rather than as a broken page
- a landing view, and an empty state that routes a fresh install toward template preparation

### Technical tasks

- scaffold the Svelte project and build pipeline; embed the build output via `embed.FS` so production stays a single Go binary (JD-009)
- Vite dev server with an `/api` proxy to the Go process, so development and production are same-origin and the auth model never differs between them
- decide how `//go:embed` handles a missing build directory on a fresh clone — placeholder file, or build tags switching between embedded and on-disk assets
- serve `index.html` for unknown page paths (client-side routing) while `/api/*` returns JSON errors

### Completion criteria

Jails and operations are usable in a browser, served by the single binary.

---

## Milestone: Host Setup

**Depends on:** nothing. **Precondition for:** Templates, Jail Provisioning.

Goal: Jail Deck establishes the dataset layout its own jail and template work depends on, instead of assuming an operator followed a runbook first.

### Capabilities

- discover the ZFS pool rather than assuming a name
- ensure `<pool>/jails` exists, mounted at `/usr/local/jails`, with `media/`, `templates/`, `containers/`

### Technical tasks

- enumerate candidate pools (`zpool list -H -o name`); require explicit configuration when there is more than one
- **ensure, not create** — existing hosts already have this layout; detect and adopt, never clobber, safe to re-run
- decide the mechanism: an explicit `init` step is preferred over a `pkg` post-install side effect, which would surprise operators and leave `pkg delete` unclean

### Completion criteria

A host with ZFS but no jail layout can be brought to a ready state by Jail Deck itself, and a host that already has the layout is detected and left alone.

---

## Milestone: Storage Visibility

**Depends on:** Web UI (to display it).

Goal: build the `storage` domain's read side — the primitives both template preparation and jail creation depend on.

### Capabilities

- list relevant ZFS datasets, with space used/available and mountpoints
- list snapshots

### Technical tasks

- define `Dataset` and `Snapshot` entities and the `StorageSystem` port in `internal/storage` (JD-010)
- implement the adapter in `internal/system/freebsd`; a single batched `zfs list -H -o name,used,avail,refer,mountpoint <paths...>` answers datasets, space and mountpoints together, in the same "one call, then match" shape as the `jls` adapter
- parser/fixture tests, same pattern as the existing `jls` parser tests
- `storage.Service` unit tested against a fake repository, without touching real ZFS

### Completion criteria

Dataset and snapshot state is visible through the API and the UI. Read-only; creation and rollback are not in this milestone.

---

## Milestone: Templates

**Depends on:** Host Setup, Storage Visibility.

Goal: automate the template-preparation workflow described in `docs/SPEC.md`. **Template preparation is an explicit, operator-initiated step** — not part of setup, and not folded into jail creation.

### Capabilities

- prepare a release template end to end: create dataset, fetch `base.txz`, extract, patch in host `resolv.conf`/`localtime`, bring to patch level with `freebsd-update`, snapshot as `@base`
- list templates and their readiness
- remove a template

### Technical tasks

- `Template` entity with an explicit lifecycle state (created → fetched → extracted → patched → updated → ready); a template is usable only once its `@base` snapshot exists
- **release identifier normalization:** use the release, never the patch level. `freebsd-version -u | sed -E 's/-p[0-9]+$//'`. The mirror publishes no `-pN` directories — `.../15.1-RELEASE-p3/base.txz` returns 404 while `.../15.1-RELEASE/base.txz` returns 200. `freebsd-update --currently-running` is the deliberate exception and wants the full `-pN` form
- **version ceiling:** a jail's userland must be the same as or older than the host kernel, so the host's own release is both the default and the upper bound. Any release picker may offer versions at or below the host only; cross-version support goes downward
- note that `freebsd-update -b` requires `--currently-running` alongside it
- resolve JD-008 for this domain — a progress mechanism for a handful of endpoints, not a general async strategy
- removal must refuse while any jail is still cloned from the template's snapshot

### Completion criteria

A template can be prepared end to end from the UI, ending in a usable `@base` snapshot, with progress visible and without manual shell steps.

---

## Milestone: Jail Provisioning

**Depends on:** Templates.

Goal: create and destroy jails.

### Capabilities

- create a jail from a ready template
- destroy a jail

### Technical tasks

- confirm the template's `@base` snapshot exists (precondition), then clone it into the jail's own dataset
- generate `jail.conf.d/<name>.conf` with `text/template`, not string concatenation
- start the jail; surface clear step-by-step failure state — a failed config write after a successful clone must not leave an undiagnosable half-created jail
- destruction: stop, remove config, destroy the dataset. The sharpest edge in the tool — explicit confirmation, a clear audit entry, and a refusal or warning when the dataset has dependent snapshots or clones
- decide orchestration ownership across the `jails`/`storage` boundary
- service-specific config needs (e.g. a database jail needing extra stanzas) — mechanism not yet designed

### Completion criteria

A jail can be created end to end (clone → configure → start) and destroyed again, without manually touching `zfs` or `jail.conf.d`.

---

## Milestone: Metrics

**Depends on:** Web UI.

Goal: the observability half of the stated scope — see load and memory, for the host and per jail.

### Capabilities

- host summary: CPU, memory, load average, disk usage
- per-jail: CPU, memory, disk, process count

### Technical tasks

- decide the data source. `rctl` gives per-jail accounting but requires `kern.racct.enable`, so a fallback via `procstat`/`ps` is needed where racct is off
- decide sampling: on request, or a background sampler with a short retention window

### Completion criteria

Host and per-jail resource usage are visible in the dashboard.

---

## Milestone: Packaging and Distribution

**Depends on:** nothing functionally; most useful once the above is worth installing.

Goal: make Jail Deck feel native to install and operate on FreeBSD.

### Capabilities

- install binary, rc.d service script, sample config
- documented safe deployment modes
- an upgrade path
- groundwork for FreeBSD package/port work

### Technical tasks

- `PREFIX`/`BINDIR` support in the Makefile
- rc.d service script
- sample configuration file
- installation documentation, covering the Host Setup step and safe deployment — including reaching a localhost-bound UI over an SSH `LocalForward` rather than opening the bind
- release build workflow

### Completion criteria

Jail Deck can be installed on FreeBSD in a repeatable way and run as a service.

---

## Deferred

Previously in scope, deliberately moved out. Not rejected — just not now.

- **In-jail service management** — list/start/stop/restart services inside a jail, view service logs. This was the original conception's core; the project has since moved toward provisioning
- **In-jail package inspection**
- **Jail configuration** — show source, edit limited safe settings, reload
- **Jail detail view, log tails, raw diagnostic output** — worth revisiting once the UI exists and their shape is clear
- **ZFS write operations** — snapshot creation, rollback, clone, replication status
- **Structured application logging**, distinct from the operation audit trail (JD-011)
- **Durable metrics history** — the one feature that would justify a database. Live values and a trailing in-memory window need none; the trigger is wanting a chart *over time* that survives a restart (JD-011)
- **Audit log rotation or richer querying** — filtering currently scans the whole file. Harmless at single-operator scale; rotation is the cheaper answer before anything larger (JD-011)

## Not planned

- multi-host management
- cluster orchestration
- bhyve/VM management
- user management, roles, or team permissions beyond a single operator
- full terminal emulator
- broad configuration management beyond what jail creation itself needs
- plugin system
- production Node.js dependency
- remote cloud control plane

## Cross-cutting work

Applies across every milestone: keep docs current as domains land, keep parser/fixture and service-level tests passing, and keep reviewing privilege boundaries and destructive-action confirmation as storage, template and creation features add real teeth.

## Open questions

1. Who orchestrates the multi-step jail-creation flow across the `jails`/`storage` boundary?
2. How should long-running template operations be represented — synchronous with a long timeout, polling, or SSE? (JD-008, now scoped to the Templates milestone alone)
3. How is per-jail resource accounting sourced when `kern.racct.enable` is off?
4. How does Host Setup establish the dataset layout — an `init` subcommand, or a guided first-run screen?
5. Is the default template prepared on demand only, or offered proactively once the host release is known?
6. ~~How is the first operator credential established?~~ **Resolved 2026-09-05: a `jaildeck passwd` subcommand.** An unauthenticated first-run setup screen is a race for the account; a token printed at startup lands in a log file nobody is watching, because Jail Deck is a service rather than an interactive program. A CLI subcommand is the convention for this (`htpasswd`, `initdb`) and keeps credential setup out of band from the daemon's lifecycle. See JD-003.
