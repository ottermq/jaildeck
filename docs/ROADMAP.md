---
title: "Roadmap"
description: "The milestones, what each one has to deliver, and what it depends on."
---

# Jail Deck — Roadmap

Capability-based rather than date-based.

## Scope

Jail Deck is a single-host operations dashboard and host agent for FreeBSD jails and bhyve VMs, covering the full native lifecycle — template preparation, creation, lifecycle control, destruction, and observability — built on `jls`, `service`, `jail.conf`, `zfs`, and the tools named in JD-012.

Not multi-host. Not OCI. Never the author of `pf.conf`.

Milestones marked **★ Otter Cloud** are needed by Otter Labs Cloud, which drives Jail Deck through its API (JD-016); the note on each says for what. They are ordered first where that does not fight the dependency chain.

*Called **★ Otter** until 2026-10-23.* "Otter" on its own is the umbrella the sibling projects share, and `otter:instance` is the ZFS tag namespace any consumer writes — so a marker that also meant "the cloud asked for this" was carrying three jobs. The tracker's label moved with it, from `otter` to `otter-cloud`.

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
- **Storage Visibility** and **Storage Writes** (v0.8.0): a jail's datasets, ZFS user properties and snapshots, read and written through the API and the interface — create a child dataset with a quota, tag it, snapshot it, roll back, destroy, move a dataset out of a jail and back, and re-clone a jail's root from a newer template without losing its children (JD-022 … JD-026, JD-028, JD-029). Templates derive, list and remove in the same release (JD-027); preparing a release template is still ahead
- **Templates** and **Jail Provisioning** (v0.9.0): prepare a release template, create a jail from a spec with VNET, rctl limits, allowlisted parameters and child datasets, ensure it idempotently with `PUT`, and destroy it — from the API and the interface (JD-017, JD-018, JD-030)
- **Exec and Automation Contract** (v0.10.0): run a command inside a jail through the API with a clean environment, a capped timeout and capped output (JD-014, JD-016); host and per-jail resource usage on a Dashboard (JD-031)
- **Metrics** (v0.11.0): disk health joined to the pools each disk holds up (JD-032), and a Prometheus endpoint behind the API's own token (JD-033). **Packaging** began in the same release: `make package` builds a verified native `.pkg`
- **Firewall Visibility** (v0.12.0): PF's state, rules with counters, NAT, anchors, tables and recent pflog lines, read-only — nothing reachable from the API can change PF (JD-020, JD-034). The interface gained a test suite in the same release (#77)
- **Virtual Machines, begun** (v0.13.0): JD-019 locked on vm-bhyve after a spike, and Host Setup reports vm-bhyve and its UEFI firmware. The same release fixed disk health, which had never worked under rc's `PATH` (#95)

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

**Depends on:** nothing. **Precondition for:** Templates, Jail Provisioning. **★ Otter Cloud:** node preparation — a node is not usable until these checks pass.

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

**Status:** done in v0.8.0. Kept for the record of what it set out to do. The API side landed first; the milestone finished when the interface showed it (JD-028), because "visible through the API and the UI" is what it asked for.

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

**Status:** done in v0.8.0. Kept for the record of what it set out to do. Every capability below is reachable from the API and from the interface, each destructive one behind the `confirm=<exact name>` the API already required (JD-029).

**Depends on:** Storage Visibility. **★ Otter Cloud:** per-instance data datasets with quotas, on-demand snapshots, and keeping data when a jail is destroyed or its root replaced.

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

**Status:** done — 2026-10-23. Kept for the record of what it set out to do.

Listing with lineage and readiness, deriving one template from another with packages, cloning a configured jail as a template, and removing one (refusing while anything is cloned from it) landed in v0.8.0 (JD-027, JD-028, JD-029).

**Preparing a release template end to end** followed once JD-008 was settled the same day. `PUT /api/templates/{name}` with a `release` answers `202` and runs four audited stages — fetch `base.txz` verified against the release's own MANIFEST, `tar -xpf` it into a new dataset with the host's `resolv.conf` and `localtime`, `freebsd-update` to the current patch level, then the `@base` snapshot. The template reports a `state` read off the filesystem throughout, with the running step beside it, so the completion criterion's "with progress visible" is satisfied by polling the template rather than by a job object.

What the milestone asked for and did **not** get: a release picker sourced from `download.freebsd.org`. The release is typed, validated against the shape the mirror publishes, and defaults to the host's own — which is also the ceiling. Whether to offer a list is the open "which releases are supported" question in `docs/SPEC.md`, not something this milestone settled.

**Depends on:** Host Setup, Storage Visibility. **★ Otter Cloud:** the release template every service template derives from.

Goal: automate the template-preparation workflow described in `docs/SPEC.md`. **Template preparation is an explicit, operator-initiated step** — not part of setup, and not folded into jail creation.

### Capabilities

- prepare a release template end to end: create dataset, fetch `base.txz`, extract, patch in host `resolv.conf`/`localtime`, bring to patch level with `freebsd-update`, snapshot as `@base`
- list templates and their readiness
- remove a template
- a **derived template**: a release template plus packages installed with `pkg -c` (e.g. a PostgreSQL template), re-snapshotted. **Settled (JD-027):** Jail Deck builds these, because `pkg -c` needs no jail and is the same shape as stage 1's `freebsd-update -b`. It is not the only path and not the general one — starting a jail on the clone and provisioning it through exec covers everything beyond packages, and is how a **custom jail** is filled. The two are the same mechanism at different times: provision at creation, or provision once into a template and clone it

### Technical tasks

- `Template` entity with an explicit lifecycle state (created → fetched → extracted → patched → updated → ready); a template is usable only once its `@base` snapshot exists
- **release identifier normalization:** use the release, never the patch level. `freebsd-version -u | sed -E 's/-p[0-9]+$//'`. The mirror publishes no `-pN` directories — `.../15.1-RELEASE-p3/base.txz` returns 404 while `.../15.1-RELEASE/base.txz` returns 200. `freebsd-update --currently-running` is the deliberate exception and wants the full `-pN` form
- **version ceiling:** a jail's userland must be the same as or older than the host kernel, so the host's own release is both the default and the upper bound. Any release picker may offer versions at or below the host only; cross-version support goes downward
- note that `freebsd-update -b` requires `--currently-running` alongside it
- ~~resolve JD-008 for this domain~~ **done 2026-10-23**: the prepare call returns `202`, the template's own resource carries a `state` read off the filesystem and a `running` step held in memory, and there is no job object
- ~~removal must refuse while any jail is still cloned from the template's snapshot~~ **done** (JD-027): `DELETE /api/templates/{name}` asks `zfs get clones` first and names what is in the way

### Completion criteria

A template can be prepared end to end from the UI, ending in a usable `@base` snapshot, with progress visible and without manual shell steps.

---

## Milestone: Jail Provisioning

**Status:** done in v0.9.0. Kept for the record of what it set out to do. Everything below is reachable from the API and from the interface, except the idempotent `PUT`, which deliberately has none: it answers a question automation asks after a timeout, not one an operator does.

**Depends on:** Templates, Storage Writes. **★ Otter Cloud:** the critical path — every managed instance is a jail created here.

Goal: create and destroy jails, including VNET jails with resource limits, from a full spec.

### Capabilities

- create a jail from a ready template, from a spec covering network, limits, jail parameters, child datasets, user properties and autostart (JD-017, JD-018)
- **idempotent create:** `PUT /api/jails/{name}` with the full spec creates the jail if absent, returns `200` unchanged if present and matching, and `409` with a diff if present and different (JD-016). `POST /api/jails` stays for the UI. *Built 2026-10-24: "matching" is decided by rendering the stanza the spec would write and holding it against the one on disk, setting by setting, so the comparison covers every field the generator writes rather than a list somebody has to keep up to date. `template` comes from the root's clone origin and `autostart` from `jail_list`; `start` is deliberately not compared (JD-016).*
- destroy a jail: `DELETE /api/jails/{name}?confirm=<name>`
- autostart via rc.conf `jail_list`

### Technical tasks

- confirm the template's `@base` snapshot exists (precondition), then clone it into the jail's own dataset
- generate `jail.conf.d/<name>.conf` with `text/template`, not string concatenation
- **VNET:** the stanza creates the epair in `exec.prestart` and destroys it in `exec.poststop`, adds the host end to the configured bridge as a `private` member, and sets the address and default route inside the jail. The bridge itself is the operator's (Host Setup reports it). *Built 2026-10-24: the unit is chosen from what no interface uses and no stanza claims, `lo0` and the address come up before `/etc/rc`, and a start that cannot be confirmed has its epair destroyed (JD-017, JD-030).*
- **limits:** rctl rules and cpuset applied when the jail starts and removed when it stops — by `exec.*` hooks or by the adapter, see the open questions. *Built 2026-10-24 with hooks: `exec.poststart` applies them and `exec.prestop` takes the rules back, so a jail started from a shell is limited the same way and a jail whose limits cannot be applied does not start at all. Rules are keyed to the name and outlive the jail, so a create refuses a name that still carries them and a destroy sweeps what is left (JD-018).*
- **parameters:** only from the allowlist in JD-018; anything else is rejected with the parameter named. *Built 2026-10-24: nine parameters, each with its own range or enumeration, and `devfs_ruleset` checked against the kernel's loaded sets — an unloaded number gives the jail the host's whole `/dev` (JD-018).*
- child datasets and user properties created through the Storage Writes primitives. *Built 2026-10-24: `datasets` and `properties` in the spec, applied after the clone and before the stanza, and compared by the idempotent `PUT`. A child mounted outside its jail is refused everywhere a child can be asked for — measured, `zfs destroy -r` on the jail takes it anyway (JD-007).*
- start the jail; surface clear step-by-step failure state — a failed config write after a successful clone must not leave an undiagnosable half-created jail
- destruction: stop, remove config, destroy the dataset. The sharpest edge in the tool — explicit confirmation, a clear audit entry, and a refusal or warning when the dataset has dependent snapshots or clones. *Built 2026-10-24: `DELETE /api/jails/{name}?confirm=<name>` — children go with it and are named, snapshots and clones refuse it, and `zfs destroy` refusing a busy dataset is the guard rather than a check of our own (JD-030).*
- **start is confirmed, not assumed:** `service jail start` exits 0 whether or not the jail came up, so every start is followed by `jls -j <name>` (JD-030)
- **a failed start leaks its epair:** `exec.poststop` does not run when the start fails, and nothing afterwards will run it, so Jail Deck destroys the orphan itself — found by the `descr jail:<name>` the stanza writes (JD-030)
- autostart is `sysrc jail_list+=<name>`; Jail Deck writes no `/etc/jail.conf` (JD-030)
- **a failed start leaves its rctl rules too:** `exec.prestop` does not run either, so whatever `exec.poststart` had already applied stays in the kernel and Jail Deck takes it back — the same cleanup as the epair, for a leftover that would otherwise be inherited by the next jail of that name (JD-018)
- **neither sweep is audited.** The audit log records the steps a create or a destroy is made of; the two cleanups of things that should not have been there are reported in the response instead. Worth revisiting — they are host mutations

*Settled 2026-10-24: orchestration ownership across the `jails`/`storage` boundary, and both of the behaviours JD-005 left to verify — all measured on 15.1-RELEASE-p5 and recorded in JD-030.*

### Completion criteria

A VNET jail with limits and a quota'd child dataset can be created end to end (clone → configure → start) from one `PUT`, a repeated `PUT` changes nothing, and the jail can be destroyed again — without manually touching `zfs`, `rctl` or `jail.conf.d`.

*Met 2026-10-24, verified on isengard: a jail with child datasets, quotas, tags, limits and parameters is created from one `PUT`, a repeated `PUT` changes nothing, and the destroy takes its children and names them. The web interface followed the same day — a create form at `/jails/new` that renders the server's own step-by-step report, and a destroy section on the jail's page (`docs/UI.md`). The idempotent `PUT` has no interface of its own and wants none: it answers a question automation asks, not one an operator does.*

---

## Milestone: Exec and Automation Contract

**Status:** done — 2026-10-24. The listener and correlation work landed in v0.8.0; exec finished it. Verified end to end on isengard: a program created a jail, ran commands in it and destroyed it over the unix socket with a token, and every audit entry carried its request id.

**Depends on:** Jail Provisioning for exec to have something to run in; the listener and correlation work can land earlier. **★ Otter Cloud:** in-jail bootstrap (e.g. `initdb`) and the worker's connection to Jail Deck.

Goal: make the API safe and predictable for a program, not only for the SPA (JD-016).

### Capabilities

- `POST /api/jails/{name}/exec` with `{ "argv": [...], "timeout": "60s", "env": {...} }`, returning stdout, stderr and exit code
- an optional **unix socket listener** (`JAILDECK_SOCKET`) next to, or instead of, TCP (JD-002)
- `X-Request-Id` recorded in every audit entry the request produces
- an API section in the changelog for every breaking change

### Technical tasks

- exec runs `jexec <name> argv...` through the existing `CommandRunner` — an argument array, no shell, audited. *Built 2026-10-24 as `jexec -l`: measured, plain `jexec` hands the jail the caller's entire environment, so everything the Jail Deck process was started with would be readable by anything run through this endpoint (JD-016).*
- the caller's timeout is capped by a server-side maximum, and output is capped in size (JD-014); a truncated output says so. *Built 2026-10-24: 60s by default, 5 minutes at most and 1 MiB of each stream. The cap is a writer rather than a truncation afterwards — a command that prints a gigabyte would otherwise be held in memory in full before anyone decided it was too much.*
- socket mode `0660`, owned by a configurable group; the token is still required
- request ids are validated (length, charset) before they reach the audit log

### Completion criteria

A local program can create a jail, run a command in it and destroy it over the unix socket with a token, and every resulting audit entry carries the program's request id.

---

## Milestone: Metrics

**Status:** done in v0.11.0. Host and per-jail usage through `GET /api/metrics` and a Dashboard page (JD-031), **disk health** in the same report with a Disks section beside Pools (#83, JD-032), and the **Prometheus endpoint** at a bare `/metrics` behind the same token auth (#15, JD-033).

**Depends on:** Web UI. **★ Otter Cloud:** monitoring before the beta.

Goal: the observability half of the stated scope — see load and memory, for the host and per jail.

### Capabilities

- host summary: CPU, memory, load average, disk usage
- per-jail: CPU, memory, disk, process count
- a `/metrics` endpoint in Prometheus text format with the same values. Prometheus holds the history, so this satisfies "a chart over time that survives a restart" without a database (JD-011)
- **disk health, not just pool health** (#83). *Built 2026-10-25: every device `smartctl` can read, with the drive's own verdict, the attributes smartctl flags, the self-test log, and the pool each disk belongs to. No health score — `isengard`'s `ada3` reports `PASSED` while attribute 184 has been below threshold for days, and no single number describes that (JD-032).*

### Technical tasks

- data source: `rctl -u jail:<name>` where `kern.racct.enable` is on; a fallback via `jls` + `ps --libxo` where it is off. *Built 2026-10-24, and the fallback is trustworthy: measured on three jails, the `ps` resident sums land within 1% of rctl's `memoryuse` and the process counts are identical, because racct's `memoryuse` is the sum of the same per-process resident sizes (JD-031).*
- decide sampling: on request, or a background sampler with a short retention window. *Decided 2026-10-24: neither. The previous CPU reading is kept in memory and the response carries the window it was measured over; the first call after a restart reports no percentage rather than a zero.*
- ~~decide whether `/metrics` shares the API's token auth or binds a separate listener~~ *Answered 2026-10-25 (JD-033): it shares the auth. Measured first — a jail with `ip4=inherit` reaches the host's loopback, so an unauthenticated port there would publish every jail's usage and every disk's serial to any jail on the box. The socket listener serves it too and costs nothing, but Prometheus can only scrape a socket from v3.15.0, which is a month old.*
- disk health reads `smartctl --json`, joined to pool members through `zpool list -v` and `glabel status`, and to the kernel's own device names through `geom disk list`. *Built 2026-10-25. The join is by name first and by serial second: `isengard`'s NVMe is `nda0` to ZFS and only ever `/dev/nvme0` to smartctl, while `rohan`'s USB-bridged `da0` has one name and two different serials (JD-032).*
- ~~a disk that is failing is not yet surfaced anywhere but the dashboard.~~ *Closed 2026-10-25: `jaildeck_disk_failing_attributes` and `jaildeck_disk_passed` are both published, so a rule can catch the case where they disagree. Jail Deck still raises nothing itself, which is the position JD-032 takes and not a gap.*

### Completion criteria

Host and per-jail resource usage are visible in the dashboard and scrapeable by Prometheus, and a disk that is about to fail is visible before the pool notices. *Met 2026-10-25.*

---

## Milestone: Firewall Visibility

**Status:** done in v0.12.0 (#16, JD-034). `GET /api/firewall`, `/api/firewall/tables/{name}` and `/api/firewall/log`, and a Firewall page. Measured on the VM with a test ruleset loaded and removed again; on isengard and rohan, PF is not loaded, and the view says so as an answer.

**Depends on:** Web UI. Needed by: operator insight; an isolation-check script can read it.

Goal: PF phase 1 (JD-020) — show what the firewall is doing, change nothing.

### Capabilities

- PF enabled or disabled, state count and counters (`pfctl -s info`)
- the loaded ruleset with per-rule counters (`pfctl -s rules -v`)
- anchors and tables, with table contents (`pfctl -s Anchors`, `-s Tables`, `-t <table> -T show`)
- the last lines of `pflog`, if logging is configured

### Technical tasks

- a `firewall` domain package with a read-only port (JD-010). *Built 2026-10-25. The port has no method that writes, and a test checks every command the adapter runs for a flag that would (JD-034).*
- parser/fixture tests for each `pfctl` output. *Built 2026-10-25 from `capture-pf-induced.sh`, which also found what JD-020's command list missed: `-s rules` hides NAT, an anchor's rules are not in the main listing, a missing anchor exits 0, `-s Tables -vv` miscounts, and pfctl's "Disabled for" is time since boot when PF has never been enabled.*

### Completion criteria

The loaded PF policy and its counters are visible in the API and the UI, and nothing in this milestone can modify PF.

---

## Milestone: Packaging and Distribution

**Status:** the first half is done in v0.11.0 (#84). Package repositories, a port and
automated publishing are what remain.

**Local native packaging:** `make package VERSION=X.Y.Z` builds and verifies a FreeBSD 15
amd64 package through `pkg create`, reusing `make install` staging. See
[the packaging workflow](PACKAGING.md) and [local validation](PACKAGING-VALIDATION.md).
Package repositories, Ports and automated publishing remain separate follow-up work.

**Depends on:** nothing functionally; most useful once the above is worth installing. **★ Otter Cloud:** before the beta — each node runs Jail Deck as an rc.d service.

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

**Status:** begun in v0.13.0. The mechanism is decided — vm-bhyve, by a spike that built the same Linux VM both ways (JD-019) — and Host Setup reports whether a host is ready for it. Guests can be listed and inspected, through the API and on a VMs page (JD-035). Creating, running and destroying a VM are ahead.

**Depends on:** Host Setup (UG check), Storage Writes (zvols), Jail Provisioning (the patterns VMs mirror). **★ Otter Cloud:** Linux VMs, after its first beta.

Goal: a `vms` domain mirroring jails (JD-019).

### Capabilities

- list and inspect VMs
- create from an image template, with CPU and memory sizing, cpuset, a zvol disk and a cloud-init seed
- start, stop, restart, destroy (`confirm=<name>`)
- a tap on the configured bridge, as VNET jails use
- serial console through the API, later

### Technical tasks

- ~~**spike first:** `vm-bhyve` against direct `bhyve(8)` + `bhyvectl` + `daemon(8)`, then record the choice in JD-019. libvirt is rejected~~ *Done 2026-10-25 (#17): **`vm-bhyve`**. Measured on isengard, both ways, with the same Debian cloud image: everything direct bhyve would make Jail Deck own — restarting on a guest reboot, destroying the instance, tap lifecycle, a stop that can force — `vm-bhyve` already does, and it is actively maintained under `freebsd/vm-bhyve`. The constraints it brings are in JD-019: `vm start` must be detached by Jail Deck, and `vm_enable` and `vm_dir` are the operator's.*
- Host Setup reports what the VMs capability needs beyond the CPU: vm-bhyve configured and answering, and edk2-bhyve's UEFI firmware. *Built 2026-10-26: `vm list` is the probe, because vm-bhyve names each misconfiguration in its own words, measured in every state from not installed to initialised. Its remedy for a missing `vm_dir` carries `-o mountpoint=`: on a pool whose root is `mountpoint=none`, as isengard's `tank` is, a plain `zfs create` makes a dataset vm-bhyve cannot use.*
- cloud-init seed ISO with `makefs -t cd9660 -o rockridge -o label=cidata`
- image templates as ZFS datasets or zvols, following the release-template pattern
- VM state derived from the system (process, `/dev/vmm`, the VM's datasets), never stored (JD-011). *Listing built 2026-10-26 (JD-035): `GET /api/vms` and `GET /api/vms/{name}`, from `vm list` and `vm info`, read on every request. vm-bhyve decides "running" by its bhyve process, so `/dev/vmm` is not read separately. The port has no method that changes a guest.*

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

- ~~Should rctl rules be applied by jail.conf `exec.*` hooks or by the adapter?~~ **Answered 2026-10-24 (JD-018): hooks.** Measuring added a second reason to the first: jail(8) destroys a jail whose `exec.poststart` fails, so a jail that could not be limited never runs — which an adapter could only have achieved by stopping a jail it had just started
- ~~What are the exec timeout cap and output size cap?~~ **Answered 2026-10-24 (JD-014):** 60s by default, 5 minutes at most, 1 MiB per stream. Sized from what commands actually produce in a jail — `ps auxww` 1.4 KB against `find / -type f` at 2.85 MB.
- ~~Does `/metrics` share auth with the API, or bind a separate loopback/socket listener?~~ **Answered 2026-10-25 (JD-033): it shares the API's auth.** The loopback was the obvious boundary and is not one here — measured on isengard, a jail with `ip4=inherit` reached the host's `127.0.0.1:8888` and was served, while the jail with its own address was refused. A Unix socket is the better-shaped answer and stays available, but Prometheus only learned to scrape one in v3.15.0 (2026-09-25), so it cannot be the only path.
- Are derived templates (release + packages) owned by the template domain, or are they exec + snapshot from the caller's point of view?
- ~~`vm-bhyve` or direct bhyve~~ **Answered 2026-10-25 (JD-019): `vm-bhyve`**, by a spike that built the same Linux VM both ways on isengard.

Earlier:

1. Who orchestrates the multi-step jail-creation flow across the `jails`/`storage` boundary?
2. ~~How should long-running template operations be represented — synchronous with a long timeout, polling, or SSE?~~ **Answered 2026-10-23 (JD-008):** detached, `202`, and the client polls the template. SSE is not excluded; it sits on top rather than replacing it.
3. ~~How is per-jail resource accounting sourced when `kern.racct.enable` is off?~~ **Answered 2026-10-24 (JD-031):** by summing `ps -J <name>`, which reads the same quantity another way rather than a different quantity under the same name — measured within 1% of rctl's. The response says which source it used, because `rctl` additionally answers `vmemoryuse`, `openfiles` and `cputime` and `ps` does not.
4. How does Host Setup establish the dataset layout — an `init` subcommand, or a guided first-run screen?
5. Is the default template prepared on demand only, or offered proactively once the host release is known?
6. ~~How is the first operator credential established?~~ **Resolved 2026-09-05: a `jaildeck passwd` subcommand.** An unauthenticated first-run setup screen is a race for the account; a token printed at startup lands in a log file nobody is watching, because Jail Deck is a service rather than an interactive program. A CLI subcommand is the convention for this (`htpasswd`, `initdb`) and keeps credential setup out of band from the daemon's lifecycle. See JD-003.
