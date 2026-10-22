# Jail Deck — Roadmap

Capability-based rather than date-based.

## Scope

Jail Deck is a single-host operations dashboard and host agent for FreeBSD jails and bhyve VMs, covering the full native lifecycle — template preparation, creation, lifecycle control, destruction, and observability — built on `jls`, `service`, `jail.conf`, `zfs`, and the tools named in JD-012.

Not multi-host. Not OCI. Never the author of `pf.conf`.

Milestones marked **★ Otter** are needed by Otter Labs Cloud, which drives Jail Deck through its API (JD-016); the note on each says for what. They are ordered first where that does not fight the dependency chain.

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
- **Access Control** (v0.5.0): mandatory single-operator authentication, `jaildeck passwd`, named API tokens (JD-003)
- **Web UI** (v0.6.0): embedded Svelte SPA with login, jails and operations views; API moved under `/api` (JD-015); audit log cursor paging
- **Host Setup** (v0.7.0): host readiness report per capability, through the API and a Host page; ZFS pool discovery with `JAILDECK_ZFS_POOL`; `jaildeck init` and `PUT /api/host/layout` ensure the jails dataset layout, never clobbering one (JD-021)

---

## Milestone: Access Control

**Status:** done in v0.5.0. Kept for the record of what it set out to do.

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

**Status:** done in v0.6.0. Kept for the record of what it set out to do.

**Depends on:** Access Control for the login view; the rest can proceed in parallel.

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

**Status:** done in v0.7.0. Kept for the record of what it set out to do, with two readiness bullets reworded: they named a *configured* bridge and devfs ruleset, and neither is a global setting — both are named per jail spec (JD-017, JD-018), so there is nothing to validate a configured value against. The report answers the host-level question instead.

**Depends on:** nothing. **Precondition for:** Templates, Jail Provisioning. **★ Otter:** node preparation — a node is not usable until these checks pass.

Goal: Jail Deck establishes the dataset layout its own jail and template work depends on, instead of assuming an operator followed a runbook first, and reports whether the host is ready for VNET jails, resource limits and VMs.

### Capabilities

- discover the ZFS pool rather than assuming a name
- ensure `<pool>/jails` exists, mounted at `/usr/local/jails`, with `media/`, `templates/`, `containers/`
- a host readiness report, **reported, never fixed**:
  - `kern.racct.enable` (rctl limits need it, and it takes a reboot to change)
  - `exec.timeout` set in the shared jail.conf defaults (JD-014)
  - at least one network bridge exists (JD-017 — a VNET jail names its own in its stanza)
  - which devfs rulesets the kernel has loaded (JD-018 — a jail spec names one of those numbers)
  - VT-x with EPT and unrestricted guest (`UG`) in `dmesg`, for VMs (JD-019)
  - whether PF is loaded and enabled (JD-020)

### Technical tasks

- enumerate candidate pools (`zpool list -H -o name`); require explicit configuration when there is more than one
- **ensure, not create** — existing hosts already have this layout; detect and adopt, never clobber, safe to re-run
- decide the mechanism: an explicit `init` step is preferred over a `pkg` post-install side effect, which would surprise operators and leave `pkg delete` unclean
- readiness checks are read-only; each failed check names the file or command that fixes it. Changing loader tunables, creating bridges and writing devfs rules stay with the operator

### Completion criteria

A host with ZFS but no jail layout can be brought to a ready state by Jail Deck itself, and a host that already has the layout is detected and left alone. The readiness report is available through the API and the UI.

---

## Milestone: Storage Visibility

**Depends on:** Web UI (to display it).

Goal: build the `storage` domain's read side — the primitives both template preparation and jail creation depend on.

### Capabilities

- list relevant ZFS datasets, with space used/available and mountpoints
- list snapshots
- show a jail's child datasets and ZFS user properties (`otter:instance`, etc.) alongside it (JD-004)

### Technical tasks

- define `Dataset` and `Snapshot` entities and the `StorageSystem` port in `internal/storage` (JD-010)
- implement the adapter in `internal/system/freebsd`; a single batched `zfs list -H -o name,used,avail,refer,mountpoint <paths...>` answers datasets, space and mountpoints together, in the same "one call, then match" shape as the `jls` adapter
- parser/fixture tests, same pattern as the existing `jls` parser tests
- `storage.Service` unit tested against a fake repository, without touching real ZFS

### Completion criteria

Dataset and snapshot state is visible through the API and the UI. Read-only; creation and rollback are not in this milestone.

---

## Milestone: Storage Writes

**Depends on:** Storage Visibility. **★ Otter:** per-instance data datasets with quotas, on-demand snapshots, and keeping data when a jail is destroyed or its root replaced.

Goal: the ZFS write operations jail creation, upgrades and safe deletion need — moved out of Deferred because a real consumer now needs them.

### Capabilities

- create a child dataset under a jail's dataset, with properties (`refquota`, `recordsize`, `compression`, `atime`)
- set and read ZFS user properties on a jail's datasets; Jail Deck passes them through and never interprets them (JD-011)
- snapshot a dataset; list, destroy and roll back snapshots
- **rename a child dataset out of a jail** (e.g. to `<pool>/jails/retained/<name>`) and back in. Destroying a jail destroys its children and their snapshots, so this is what makes "destroy the jail, keep the data" possible
- **re-clone a jail's root from a newer template snapshot while keeping its child datasets** (JD-007) — the upgrade path for a jail whose state lives in a child dataset

### Technical tasks

- add the write side of the `StorageSystem` port; every mutation goes through the audit log
- destructive calls take `confirm=<exact name>` at the API level (JD-016), not only a UI dialog
- refuse to destroy a dataset with clones, holds or dependent snapshots unless explicitly told; report which
- rename uses `zfs rename -u` so the dataset is not remounted somewhere surprising mid-move
- fixture tests for every command's argument array

### Completion criteria

A child dataset can be created with a quota, snapshotted, moved out of a jail and back, and a jail's root replaced from a newer template without losing the child dataset — all from the API, without a shell.

---

## Milestone: Templates

**Depends on:** Host Setup, Storage Visibility. **★ Otter:** the release template every service template derives from.

Goal: automate the template-preparation workflow described in `docs/SPEC.md`. **Template preparation is an explicit, operator-initiated step** — not part of setup, and not folded into jail creation.

### Capabilities

- prepare a release template end to end: create dataset, fetch `base.txz`, extract, patch in host `resolv.conf`/`localtime`, bring to patch level with `freebsd-update`, snapshot as `@base`
- list templates and their readiness
- remove a template
- a **derived template**: a release template plus packages installed with `pkg -c` (e.g. a PostgreSQL template), re-snapshotted. Whether this is a template-domain feature or just exec + snapshot from the caller's side is an open question below

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

**Depends on:** Templates, Storage Writes. **★ Otter:** the critical path — every managed instance is a jail created here.

Goal: create and destroy jails, including VNET jails with resource limits, from a full spec.

### Capabilities

- create a jail from a ready template, from a spec covering network, limits, jail parameters, child datasets, user properties and autostart (JD-017, JD-018)
- **idempotent create:** `PUT /api/jails/{name}` with the full spec creates the jail if absent, returns `200` unchanged if present and matching, and `409` with a diff if present and different (JD-016). `POST /api/jails` stays for the UI
- destroy a jail: `DELETE /api/jails/{name}?confirm=<name>`
- autostart via rc.conf `jail_list`

### Technical tasks

- confirm the template's `@base` snapshot exists (precondition), then clone it into the jail's own dataset
- generate `jail.conf.d/<name>.conf` with `text/template`, not string concatenation
- **VNET:** the stanza creates the epair in `exec.prestart` and destroys it in `exec.poststop`, adds the host end to the configured bridge as a `private` member, and sets the address and default route inside the jail. The bridge itself is the operator's (Host Setup reports it)
- **limits:** rctl rules and cpuset applied when the jail starts and removed when it stops — by `exec.*` hooks or by the adapter, see the open questions
- **parameters:** only from the allowlist in JD-018; anything else is rejected with the parameter named
- child datasets and user properties created through the Storage Writes primitives
- start the jail; surface clear step-by-step failure state — a failed config write after a successful clone must not leave an undiagnosable half-created jail
- destruction: stop, remove config, destroy the dataset. The sharpest edge in the tool — explicit confirmation, a clear audit entry, and a refusal or warning when the dataset has dependent snapshots or clones
- decide orchestration ownership across the `jails`/`storage` boundary
- verify how `jail_enable`/`jail_list` interact with `jail.conf.d` on 15.1, and how `service jail` behaves with VNET stanzas (JD-005)

### Completion criteria

A VNET jail with limits and a quota'd child dataset can be created end to end (clone → configure → start) from one `PUT`, a repeated `PUT` changes nothing, and the jail can be destroyed again — without manually touching `zfs`, `rctl` or `jail.conf.d`.

---

## Milestone: Exec and Automation Contract

**Depends on:** Jail Provisioning for exec to have something to run in; the listener and correlation work can land earlier. **★ Otter:** in-jail bootstrap (e.g. `initdb`) and the worker's connection to Jail Deck.

Goal: make the API safe and predictable for a program, not only for the SPA (JD-016).

### Capabilities

- `POST /api/jails/{name}/exec` with `{ "argv": [...], "timeout": "60s", "env": {...} }`, returning stdout, stderr and exit code
- an optional **unix socket listener** (`JAILDECK_SOCKET`) next to, or instead of, TCP (JD-002)
- `X-Request-Id` recorded in every audit entry the request produces
- an API section in the changelog for every breaking change

### Technical tasks

- exec runs `jexec <name> argv...` through the existing `CommandRunner` — an argument array, no shell, audited
- the caller's timeout is capped by a server-side maximum, and output is capped in size (JD-014); a truncated output says so
- socket mode `0660`, owned by a configurable group; the token is still required
- request ids are validated (length, charset) before they reach the audit log

### Completion criteria

A local program can create a jail, run a command in it and destroy it over the unix socket with a token, and every resulting audit entry carries the program's request id.

---

## Milestone: Metrics

**Depends on:** Web UI. **★ Otter:** monitoring before the beta.

Goal: the observability half of the stated scope — see load and memory, for the host and per jail.

### Capabilities

- host summary: CPU, memory, load average, disk usage
- per-jail: CPU, memory, disk, process count
- a `/metrics` endpoint in Prometheus text format with the same values. Prometheus holds the history, so this satisfies "a chart over time that survives a restart" without a database (JD-011)

### Technical tasks

- data source: `rctl -u jail:<name>` where `kern.racct.enable` is on; a fallback via `jls` + `ps --libxo` where it is off
- decide sampling: on request, or a background sampler with a short retention window
- decide whether `/metrics` shares the API's token auth or binds a separate listener (open question below)

### Completion criteria

Host and per-jail resource usage are visible in the dashboard and scrapeable by Prometheus.

---

## Milestone: Firewall Visibility

**Depends on:** Web UI. Needed by: operator insight; an isolation-check script can read it.

Goal: PF phase 1 (JD-020) — show what the firewall is doing, change nothing.

### Capabilities

- PF enabled or disabled, state count and counters (`pfctl -s info`)
- the loaded ruleset with per-rule counters (`pfctl -s rules -v`)
- anchors and tables, with table contents (`pfctl -s Anchors`, `-s Tables`, `-t <table> -T show`)
- the last lines of `pflog`, if logging is configured

### Technical tasks

- a `firewall` domain package with a read-only port (JD-010)
- parser/fixture tests for each `pfctl` output

### Completion criteria

The loaded PF policy and its counters are visible in the API and the UI, and nothing in this milestone can modify PF.

---

## Milestone: Packaging and Distribution

**Depends on:** nothing functionally; most useful once the above is worth installing. **★ Otter:** before the beta — each node runs Jail Deck as an rc.d service.

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

## Milestone: Virtual Machines

**Depends on:** Host Setup (UG check), Storage Writes (zvols), Jail Provisioning (the patterns VMs mirror). **★ Otter:** Linux VMs, after its first beta.

Goal: a `vms` domain mirroring jails (JD-019).

### Capabilities

- list and inspect VMs
- create from an image template, with CPU and memory sizing, cpuset, a zvol disk and a cloud-init seed
- start, stop, restart, destroy (`confirm=<name>`)
- a tap on the configured bridge, as VNET jails use
- serial console through the API, later

### Technical tasks

- **spike first:** `vm-bhyve` against direct `bhyve(8)` + `bhyvectl` + `daemon(8)`, then record the choice in JD-019. libvirt is rejected
- cloud-init seed ISO with `makefs -t cd9660 -o rockridge -o label=cidata`
- image templates as ZFS datasets or zvols, following the release-template pattern
- VM state derived from the system (process, `/dev/vmm`, the VM's datasets), never stored (JD-011)

### Completion criteria

A Linux VM can be created from an image with cloud-init, reached over the bridge, stopped and destroyed from the API and the UI.

---

## Later: Firewall Tables and Anchors

PF phases 2 and 3 (JD-020) — adding and removing addresses in operator-declared tables, then loading rules into `jaildeck/*` anchors with validation and rollback. Only when a real need appears, most likely per-VM rules.

---

## Deferred

Previously in scope, deliberately moved out. Not rejected — just not now.

- **In-jail service management** — list/start/stop/restart services inside a jail, view service logs. This was the original conception's core; the project has since moved toward provisioning. Running a command in a jail is in scope (Exec and Automation Contract); managing services through it is not
- **In-jail package inspection**
- **Jail configuration** — show source, edit limited safe settings, reload
- **Jail detail view, log tails, raw diagnostic output** — worth revisiting once the UI exists and their shape is clear
- **ZFS replication status** — the rest of the ZFS write operations moved into Storage Writes
- **Structured application logging**, distinct from the operation audit trail (JD-011)
- **Durable metrics history inside Jail Deck** — the one feature that would justify a database. Live values and a trailing in-memory window need none, and the `/metrics` endpoint hands history to Prometheus; the trigger is wanting a chart *over time* inside Jail Deck itself that survives a restart (JD-011)
- **Audit log rotation or richer querying** — filtering currently scans the whole file. Harmless at single-operator scale; rotation is the cheaper answer before anything larger (JD-011)

## Not planned

- multi-host management
- cluster orchestration
- writing `pf.conf` or toggling `pf_enable` (JD-020)
- user management, roles, or team permissions beyond a single operator
- full terminal emulator
- broad configuration management beyond what jail creation itself needs
- plugin system
- production Node.js dependency
- remote cloud control plane

## Cross-cutting work

Applies across every milestone: keep docs current as domains land, keep parser/fixture and service-level tests passing, and keep reviewing privilege boundaries and destructive-action confirmation as storage, template and creation features add real teeth.

## Open questions

Added 2026-10-21 with the host-agent scope:

- Should rctl rules be applied by jail.conf `exec.*` hooks or by the adapter? Leaning **hooks**: they survive a Jail Deck outage and behave the same when a jail is started with `service jail start` from a shell
- What are the exec timeout cap and output size cap?
- Does `/metrics` share auth with the API, or bind a separate loopback/socket listener?
- Are derived templates (release + packages) owned by the template domain, or are they exec + snapshot from the caller's point of view?
- `vm-bhyve` or direct bhyve — settled by the spike in Virtual Machines (JD-019)

Earlier:

1. Who orchestrates the multi-step jail-creation flow across the `jails`/`storage` boundary?
2. How should long-running template operations be represented — synchronous with a long timeout, polling, or SSE? (JD-008, now scoped to the Templates milestone alone)
3. How is per-jail resource accounting sourced when `kern.racct.enable` is off?
4. How does Host Setup establish the dataset layout — an `init` subcommand, or a guided first-run screen?
5. Is the default template prepared on demand only, or offered proactively once the host release is known?
6. ~~How is the first operator credential established?~~ **Resolved 2026-09-05: a `jaildeck passwd` subcommand.** An unauthenticated first-run setup screen is a race for the account; a token printed at startup lands in a log file nobody is watching, because Jail Deck is a service rather than an interactive program. A CLI subcommand is the convention for this (`htpasswd`, `initdb`) and keeps credential setup out of band from the daemon's lifecycle. See JD-003.
