---
title: "Decisions"
description: "Every architectural decision that has been locked, with the reasoning and the costs accepted."
---

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

**Status:** Locked — revised 2026-09-04

The default bind address is:

```
127.0.0.1
```

Jail Deck manages jails on the host it runs on, not a fleet of machines — that is a scope statement, not an access restriction. The bind address is configurable via `JAILDECK_HOST`.

**The bind address is no longer a security control.** Authentication is unconditional (JD-003), so the default exists to reduce attack surface and to keep credentials off the wire by default — not to make anything safe. Binding elsewhere is an ordinary configuration choice.

A non-loopback bind sends the operator's password across the network, so such a deployment is expected to terminate TLS — a reverse proxy is the recommended path. Jail Deck warns at startup when bound non-loopback without TLS in front, but does not refuse: it cannot detect a proxy, and refusing would break the recommended deployment.

The previous wording — "operators may bind to another interface or `0.0.0.0` at their own risk" — is withdrawn. "At their own risk" is not a control, and the risk was mis-stated: Jail Deck runs as root (JD-001) and, from Phase 4 onward, can create and destroy jails and ZFS datasets. An unauthenticated reachable instance is not an information leak, it is unauthenticated root-equivalent control of the host.

### Rationale

An earlier revision of this decision made a non-loopback bind conditional on authentication being configured, enforced at startup. That interlock is withdrawn — not because it was wrong, but because it is unnecessary once authentication is unconditional. There is no longer a configuration in which the bind address determines whether the application is safe.

Defaulting to loopback still earns its place: it keeps the login form and session cookie off the network for the common single-host case, and it means the reverse-proxy deployment is the path of least resistance rather than an extra step.

What does not survive is the previous framing that local-only access "eliminates authentication concerns." It does not — see JD-003.

For reaching a localhost-bound instance from another machine without weakening the bind, see the operator guidance: an SSH `LocalForward` for ad-hoc access, or a reverse proxy terminating TLS for a permanent one.

**Amended 2026-10-21 — unix socket listener.** Jail Deck may also listen on a unix socket (`JAILDECK_SOCKET`), next to or instead of TCP. The socket is mode `0660` and owned by a configurable group, so a local automation client (JD-016) needs no TCP port and no TLS, and file permissions become a first access check. They are not the only one: the API token is still required on the socket, because JD-003's reasoning about who can reach loopback applies to anything in the socket's group.

**Amended 2026-10-22 — a browser cannot hold a session over plain HTTP.** "Expected to terminate TLS" above understates it for browser use. The session cookie is `Secure` (JD-003), so a browser discards it on any non-loopback plain-HTTP origin: `POST /api/auth/login` returns `200`, the cookie is dropped, and the next request returns `401` — which looks exactly like a rejected password, and is distinguishable from one only by the response body. TLS in front is therefore a **requirement** for the web interface on a non-loopback bind, not a recommendation. It is not a requirement for automation: `RequireAuth` checks the `Authorization: Bearer` token before any cookie, so an API client (JD-016) is unaffected. Browsers treat `localhost` as a secure context, which is why the default bind and the `LocalForward` above both work without TLS. The startup warning names this consequence as well as the plaintext password.

---

## JD-003 — Authentication

**Status:** Locked — amended 2026-10-01; revised 2026-09-04, supersedes the previous "no authentication" decision

The previous decision recorded its own trigger: *"revisit if remote/multi-user access becomes real."* Remote access became real — requiring an operator to set up an SSH tunnel before opening a web UI is poor ergonomics for a tool whose stated goal is to be the first thing opened after logging in. This decision is the revisit.

Jail Deck implements **mandatory single-operator authentication**. There is no unauthenticated mode, on any bind address, including loopback. There is no flag to disable it.

**Session model — opaque token in a cookie, not JWT.**

- On successful login the server issues a cryptographically random token and stores it server-side, keyed to an expiry.
- The token is delivered as a cookie with `HttpOnly`, `Secure`, `SameSite=Strict`, and an explicit `Path` and `MaxAge`.
- The frontend never reads, stores, or attaches the credential; the browser does. The SPA determines login state by calling an identity endpoint and branching on `200` vs `401`.
- Logout deletes the server-side entry, which revokes the session immediately.

**Credential storage:** a single operator password, stored as an **argon2id** hash in the application state file (JD-011). No user management, no roles, no registration — consistent with "complex user/team permissions" being out of scope.

**Credential bootstrapping — a CLI subcommand, not a first-run screen and not a printed token.**

```
jaildeck passwd                 # prompts on the tty, no echo
jaildeck passwd --stdin         # reads from stdin, for unattended installs
```

The subcommand is the only writer of the credential; it hashes with argon2id and writes the state file atomically with restrictive permissions. It requires root, consistent with JD-001. The credential is **not** also accepted from configuration or the environment: two sources for one credential produces a precedence question that will be answered wrongly at the worst moment.

**Starting without a credential is a fatal error**, naming the fix. Fail closed — consistent with there being no unauthenticated mode.

The resulting installation sequence:

```sh
pkg install jaildeck
jaildeck passwd
sysrc jaildeck_enable=YES
service jaildeck start
```

Changing the credential invalidates existing browser sessions, but **not** API tokens — see below.

**There is no password-recovery flow.** Root on the host runs `jaildeck passwd` again. This is not a gap: root can already do everything Jail Deck can do, so there is no privilege to protect behind a recovery mechanism and nothing to design.

**Optional API token:** a separate, long-lived token accepted in an `Authorization` header, for scripting the JSON API. This is a distinct credential from the browser session and is not a reason to change the session model.

Tokens are random, stored as a **fast** hash, and named. The hash is SHA-256, not argon2id: key stretching exists to compensate for the low entropy of a human password, and a 256-bit value from `crypto/rand` has nothing to buy back. Using argon2id here would cost 64 MiB and ~67 ms per request on an endpoint meant for scripting, and `Authorization` is unauthenticated until verified — so it would rebuild the amplification the login bound exists to contain. Each token carries an operator-chosen unique name, because "revocable" is meaningless if the operator cannot say *which* token to revoke six months later.

**Changing the operator password does not revoke API tokens.** This is deliberate, and it is the one place where tokens and sessions behave differently.

A session is bound to the credential it was issued against — it records a fingerprint of the stored hash, and `passwd` invalidates every live session by writing the file. A token is not: it is an independent credential, created and revoked on its own.

The reasoning is about who is affected. A session belongs to a human who is present and can log in again. A token belongs to an unattended script, and silently breaking every cron job because an operator rotated their own password is behaviour people work around badly — by never rotating, or by sharing the password instead of issuing tokens. The explicit answer for a compromise is `jaildeck token revoke`, which revokes exactly what was compromised and nothing else.

The cost is that a password rotation is not a blanket revocation. An operator who needs that must revoke the tokens as a separate, deliberate step.

**A token is root-equivalent** (amended 2026-10-21). Jail Deck runs as root (JD-001), and once jail creation, exec and storage writes exist, any token can create a jail with arbitrary contents, run a command in any jail, or destroy a dataset. A token should therefore be held only on the host it controls — by an automation client running there, reaching Jail Deck over the unix socket (JD-002) — and never copied to another machine. Named tokens are exactly what such a client needs, so no new credential type is introduced.

### Rationale

**Why authentication is unconditional, including on loopback.** An earlier revision made authentication optional for a loopback bind, on the reasoning that localhost is reachable only by the operator. That reasoning is wrong, and wrong in a way that matters specifically for this application:

- **A jail with `ip4=inherit` shares the host's network stack, including `127.0.0.1`.** A jail host is precisely the environment where loopback is not a private channel. A compromised jail could reach an unauthenticated Jail Deck and, through it, control every other jail and the host. The tool's own threat model contains the thing it manages.
- **Any unprivileged local user can connect to a loopback port.** With no authentication, that is a direct path from an ordinary shell account to root-equivalent control.
- **A web page the operator visits can issue cross-origin requests to `127.0.0.1`.** Same-site cookie policy and CORS mitigate much of this, but an endpoint requiring no credential at all has nothing to mitigate.

"Localhost only" was never an access control; it was an assumption about who is on the machine. For an application running as root with `zfs destroy` in reach, that assumption is not worth the convenience it buys.

Making authentication unconditional also removes a whole category of configuration state: there is no "is auth enabled" branch, no startup interlock, no combination of settings that is quietly unsafe.

**Why not JWT.** JWT solves problems Jail Deck does not have: stateless verification across independent services, cross-origin frontends, and third-party API consumers. Jail Deck is one binary serving both the SPA and the API from one origin (JD-009), for one operator, on one host.

What remains is JWT's costs. Revocation requires a server-side denylist, which discards the statelessness that was the only reason to choose it — and for a tool running as root with `zfs destroy` in reach, immediate revocation is the single most important security operation, not an afterthought. Browser storage is the other cost: `localStorage` is readable by any successful XSS, whereas an `HttpOnly` cookie is not; and storing a JWT in a cookie concedes the argument. Finally, JWT carries a class of implementation vulnerabilities — algorithm confusion, `alg: none`, expiry and refresh-token handling — that opaque tokens simply do not have.

A server-side session store for a single operator is one map behind a mutex. The "stateless scales better" argument has no force at n=1, and sessions not surviving a restart is acceptable — arguably desirable — for an administrative tool.

**Why not PAM.** Authenticating against the host's own users is the more FreeBSD-native option and was considered. It is rejected because Go PAM bindings require **cgo**, which forfeits the pure-Go property that lets a FreeBSD binary be cross-compiled from another platform in seconds. That build simplicity is worth more than avoiding one password hash, and a single-operator credential fits the scope better than host-user integration.

**Why a subcommand rather than a one-time token printed at startup.** The printed-token pattern (as used by Jupyter) depends on the program running in the foreground with an operator watching its output. Jail Deck is started by `rc.d` and daemonizes; its output goes to a log file or to `/dev/null`, and nobody is watching. The token would come to rest inside `/var/log`, where it is a live credential at rest in a file that may be world-readable, log-rotated, or shipped to a remote syslog — taking the credential off the machine entirely. The pattern is a poor fit for a service, as opposed to an interactive application.

A CLI subcommand is also the established convention for exactly this problem: `htpasswd` for Apache and nginx, `initdb` for PostgreSQL. Credentials are established out of band from the daemon's lifecycle, by an administrator who is present, rather than by the daemon at a moment when nobody is.

**CSRF.** `SameSite=Strict` addresses cross-site request forgery for the cookie path; the `Authorization` header path is immune by construction. This supersedes any earlier treatment of CSRF written against server-rendered HTML forms.

**TLS.** Authentication over plaintext on a LAN is weak. A non-loopback deployment is expected to terminate TLS — a reverse proxy is the recommended path, keeping certificate handling out of Jail Deck. `Secure` cookies remain valid over `http://localhost`, so the loopback development case needs no exception.

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

**Amended 2026-10-21.** A jail listing also returns the jail's child datasets and its ZFS user properties (`zfs get -H -r -o name,property,value,source -s local all <pool>`, filtered to names containing `:`). An automation client tags its jails with properties such as `otter:instance` and finds them again from the listing alone, with nothing stored in Jail Deck (JD-011).

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

**Verified 2026-10-24** on 15.1-RELEASE-p5, against both questions this decision left open:

- `service jail start|stop <name>` drives a VNET stanza whose `exec.prestart` and `exec.poststop` create and destroy the epair, exactly as JD-017 assumed. The host end joins the bridge with the `private` flag set, the jail end is moved into the vnet, the jail's own `/etc/rc` configures it, and the stop takes both ends away again.
- Autostart reads `jail_list`, and a jail whose only configuration is `/etc/jail.conf.d/<name>.conf` starts from it. `service jail start <name>` works just as well for a name that is *not* in the list, so membership means "start at boot" and nothing more.

Neither answer changes this decision. What they did change is how much `service` can be trusted to *report*, which is JD-030.

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

   *Built 2026-10-23, detached and polled per JD-008.* Four stages, each audited: `fetch-base`, `extract-base`, `update-template`, `create-snapshot`. **The snapshot is last, and that ordering is the point** — `base.txz` is the release as published, so a template sealed before `freebsd-update` would carry every advisory since into every jail cloned from it. A stage that fails leaves the template honestly not ready rather than sealed.

   `freebsd-update -b <root> --currently-running <release> --not-running-from-cron fetch install`, measured against the real tool on the deployment host: the **plain release** is what `--currently-running` wants, not a patch level, because a freshly unpacked `base.txz` is exactly the release as published — it answered "updating to 15.1-RELEASE-p6". `--not-running-from-cron` is what makes it non-interactive, and `fetch install` in one invocation exits 0 even when there is nothing new, which is the ordinary case for a recent release.
2. **Jail creation** (per jail, requires a ready template): clone the template snapshot into the jail's own dataset, generate the `jail.conf.d/<name>.conf` stanza, start the jail, and hand off to in-jail provisioning (`jexec` + `pkg`) for service-specific setup.

   *Built 2026-10-24 for `inherit`, `alias` and `disable` networking; the order and the failure path are JD-030.* The root is cloned into `<pool>/jails/containers/<name>` **in the template's own pool**, because a clone cannot cross one. The start is confirmed with `jls` rather than believed from an exit code. VNET (JD-017) and limits (JD-018) extend the same spec and are built too; child datasets and user properties are not.

### Rationale

Templates are shared, immutable-once-snapshotted bases; jails are cheap clones of them. This keeps jail creation fast and avoids re-fetching/re-patching a userland per jail.

**Amended 2026-10-21 — child datasets and root re-cloning.** A jail may have **child datasets** under its own dataset (e.g. `containers/<name>/data` mounted at `/var/db/postgres`), created with properties at creation time. Two reasons:

- **Quotas belong on a child.** A clone's `referenced` space includes the template's blocks, so a quota on the jail's root dataset counts the userland against the jail. A `refquota` on a child dataset counts only what the jail wrote.
- **State in a child makes the root replaceable.** A jail whose persistent state lives in a child can have its root re-cloned from a newer template snapshot — move the child aside with `zfs rename -u`, destroy and re-clone the root, move the child back. That is a generally useful upgrade path, not one specific to any consumer.

The same rename is what lets a jail be destroyed while its data is kept: `zfs destroy -r` on the jail takes its children and their snapshots with it, so anything worth keeping is renamed out first.

**Built 2026-10-24.** Child datasets and the jail's own user properties are part of the create: `datasets` and `properties` in the spec, made and set **after the root is cloned and before the stanza is written**, so a jail whose `/etc/rc` expects its data mounted finds it there and a quota is in force before the first write. Each child takes the native properties of JD-023's allowlist, and `mountpoint` is a path on the host — the one a child inherits is `<jail path>/<name>`, which is `/<name>` seen from inside the jail.

**A child mounted outside the jail is refused**, in the service rather than in the create, so the `PUT /api/jails/{name}/datasets/{child}` endpoint makes the same refusal. Measured on 15.1-RELEASE-p5: ZFS creates such a dataset without complaint, the jail cannot see it — and `zfs destroy -r` on the jail destroys it anyway, because it is the jail's child whatever it is mounted over. So destroying the jail would take a dataset living elsewhere on the host, and the operator would read its name in `taken` with no reason to look twice. Data meant to outlive the jail has its own door: `retain` moves it out of the jail's dataset tree, which is what makes the destroy leave it alone.

It is the one refusal in a create that cannot come before anything is created: the jail has no path until its root is cloned, and JD-021 fixes only where the layout is mounted, not that every pool mounts it alike. It is made at the datasets step, and a create stopped there has a cloned root and no stanza, which the response says (JD-030).

**What the idempotent `PUT` compares** (JD-016): a child the spec asks for and the jail does not have is a difference, and so is one the jail has that the spec does not name — a child takes space and goes with the jail, so it is part of the jail in a way a tag is not. Only direct children, because the spec describes one level and a deeper dataset is one it could neither ask for nor disown. Properties compare as a subset in both senses, which is what the write side already does: native properties are applied rather than replaced, and user properties are merged so that a tag another tool set survives (JD-023).

---

## JD-008 — Long-running Operations

**Status:** Locked — 2026-10-23 (scope narrowed 2026-09-04, amended 2026-10-21)

Fetching `base.txz`, extracting it and running `freebsd-update` take minutes. **The request that starts that work does not wait for it.** It returns `202` at once; the client watches the resource itself.

```
PUT /api/templates/15.1-RELEASE        -> 202, {"state": "fetching"}
GET /api/templates/15.1-RELEASE        -> {"state": "extracting",
                                           "running": {"step": "extract", "since": …}}
   …
GET /api/templates/15.1-RELEASE        -> {"state": "ready", "ready": true}
```

**There is no job, and no job id.** The thing being watched is the template, at the URL it will keep for the rest of its life. A handle that only exists while an operation is in flight would be a second identity for the same object, and a client that lost it would have no way back to the work it started.

### The state comes off the disk

`state` is read, not recorded. JD-011 already settled this when it listed "what state is a template in?" among the derived questions, and named the checks:

| State | What makes it true |
| --- | --- |
| `absent` | no dataset |
| `fetching` | nothing yet, and an operation is running |
| `fetched` | `media/<release>/base.txz` exists |
| `extracting` / `extracted` | the template's dataset exists and holds a userland |
| `updating` | as above, with an operation running |
| `ready` | the `@base` snapshot exists |

So a restart, a crash, or a client that never comes back cost nothing: the next `GET` reads the same filesystem and reports where the work actually got to. Nothing can disagree with the host, which is the whole of JD-011 in one sentence.

**It is also what makes the call retry-safe.** JD-016 wants an idempotent `PUT`; repeating this one resumes from whatever stage is already a fact on disk rather than starting again. That was the 2026-10-21 amendment's argument for staying synchronous, and it survives the move to `202` intact — because the idempotency never came from the response shape, it came from the stages being facts.

### `running` is in memory, and that is correct

One thing cannot be derived: whether a step is executing *right now*, as opposed to having stopped half way. A dataset that exists without `@base` looks identical whether `freebsd-update` is running in it or died an hour ago.

So the process keeps an in-memory record of what it is currently doing — step and start time — and reports it as `running` beside the derived state. It is deliberately **not persisted**:

- A restart kills the work, so a marker that survived the restart would be claiming something false. Losing it is the truthful outcome, not a limitation.
- Persisting it would make Jail Deck own durable state about operations, which is exactly the database JD-011 refuses. `/var/db/jaildeck/state.json` holds credentials because they cannot be re-derived; a step counter can be.

**What happened, as opposed to what is happening, is the audit log's job.** Every stage writes its own entry with the command, exit code and duration (JD-011), so a failed prepare is answered by the operations page rather than by a status field that would have to be invented and stored.

### Where the line is

| | |
| --- | --- |
| **Detached, `202`, polled** | preparing a release template; importing a VM image (JD-019) |
| **Synchronous** | everything else, including deriving a template with `pkg -c` |

The line is not "slow" in the abstract, it is **whether the operator needs to be told what is happening to keep using the tool**. Deriving a template with packages measured ~25 s on the deployment target — long enough to notice, short enough that a spinner is an honest answer. A 372 MB fetch followed by `freebsd-update` is not.

Deriving stays synchronous for a second reason: it shipped that way in v0.8.0, and a client that works should not be broken to make two endpoints in the same domain look alike.

### Why not the alternatives

**Synchronous with a long timeout** was the cheapest, and nearly won — JD-016's retry contract already covers the client that gives up. It loses on what the operator sees: minutes of spinner with no way to tell working from wedged, through an intermediary that may well cut the connection first. "Retry and find out" is an answer for a script, not for someone watching a release download.

**A task queue, or any job store**, is disproportionate at this scale and would reintroduce the database JD-011 removed. Two slow flows on one host do not need a scheduler.

**Server-Sent Events** streaming the command's own output is the nicest version of this and is not excluded — it sits on top of what is above rather than replacing it. It is not in the first implementation because the stream dies with a page reload while the polled state does not, an automation client has no use for it, and the command runner collects output rather than streaming it, so it is a change to the runner as much as to the API. Worth doing when watching `freebsd-update` scroll is worth that; the polled state is what makes the feature usable without it.

### Rationale

The decision that mattered was not the transport. It was refusing to invent a job object: once the resource is the thing you watch and its state is read off the disk, the asynchronous version of this API is barely different from the synchronous one, and nothing new has to be remembered, migrated or cleaned up.

---

## JD-009 — Frontend: Svelte SPA

**Status:** Locked

Jail Deck's UI is a **Svelte** single-page application. The backend exposes a JSON API; handlers do not render HTML fragments.

Production still ships as a single Go binary: the Svelte build output is embedded via `embed.FS`, the same way templates and static assets were previously. Node.js is a build-time dependency only, never a production one.

### Rationale

The growing feature set (templates, ZFS operations, jail creation flows) benefits from richer client-side interaction than server-rendered HTML fragment-swapping comfortably provides. Svelte was chosen over Vue after direct comparison. The migration happens early — while `jails` and the audit log are the only existing domains, there is a small, well-understood surface to convert. Every domain added afterward (storage, templates) is built API-first from the start, avoiding a larger migration later — see `docs/ROADMAP.md`. The JSON API landed first (Phase 1a); the Svelte UI was left to Phase 1b and shipped in v0.6.0, on the architecture JD-015 settles.

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

**Amended 2026-10-21.** Two domains are planned beyond `jails` and `storage`: `vms` (JD-019) and `firewall` (JD-020). `storage` gains write operations. Each follows the same shape when it is built, and not before.

### Rationale

Organizing by domain keeps each bounded context's rules, contract, and orchestration logic in one place instead of spread across several folders, and scales cleanly as new domains (storage) are added. `internal/system`'s execution primitives (`Command`, `CommandResult`, `CommandRunner`, `CommandError`) are the one deliberate exception — they are domain-agnostic and correctly live outside any domain package.

---

## JD-011 — State and Persistence

**Status:** Locked — amended 2026-09-21; expanded 2026-09-05 (was "Operation History")

Jail Deck has **no database**, and none of its currently scoped features require one.

State is classified four ways, and each kind has exactly one home:

**1. Operator configuration** — what the administrator sets. Bind address, port, pool name. Read from the environment (`godotenv`). Jail Deck reads this and never writes it.

**2. Application state** — what Jail Deck itself owns and must persist. Currently the operator password hash (argon2id) and any API tokens. Held in a state file that Jail Deck owns, **separate from operator configuration**, written atomically (temp file plus rename) with restrictive permissions.

Note this is a genuinely new capability: until authentication (JD-003), Jail Deck only ever *read* configuration. Writing a password change back into the operator's `.env` would destroy their formatting and comments, which is why the two are separate files rather than one.

**3. Derived state** — everything else. Not stored, because the system already holds it authoritatively:

| Question | Answer |
| --- | --- |
| Which jails exist, and are they running? | `jls` merged with `jail.conf`/`jail.conf.d` (JD-004) |
| How is a jail configured? | `jail.conf.d/<name>.conf` *is* the record |
| Which datasets and snapshots exist? | `zfs list` |
| What state is a template in? | Dataset exists? `base.txz` present in `media/`? `@base` snapshot exists? Those checks **are** the state machine |
| Which template was this jail cloned from? | `zfs get origin <dataset>` |
| When was it created? | `zfs get creation <dataset>` |
| Can this dataset be destroyed? | `zfs get clones` plus snapshot listing |
| Which consumer object does this jail belong to? | ZFS user properties on its dataset (e.g. `otter:instance`), set by the consumer, passed through and never interpreted by Jail Deck |
| What limits does this jail have? | Its `jail.conf.d` stanza and `rctl -l jail:<name>` |

Template metadata that is genuinely underivable — the patch level `freebsd-update` brought a template to, for example — is stored in a small file **inside the template's own dataset**, not in a central store. It then clones with the template, is destroyed with it, and cannot outlive what it describes.

**4. Recorded history** — what Jail Deck *did*. The append-only JSON-lines audit log (`internal/audit`). Jail Deck writes it and reads it back, which makes it look like application state, but it is **expendable**: deleting it costs the operator their history and costs Jail Deck nothing.

That difference is not a judgement call — it is already encoded in the code. A missing audit log is not an error: `Recent` returns an empty result and `Log` creates the file on its first write. A corrupt line is skipped and reading continues. A missing state file, by contrast, is a fatal startup error (JD-003), and a corrupt one is reported as corrupt rather than treated as absent, precisely so an operator is never told to set a new password when the real problem is a damaged file.

Nor is it derived state: `jls` and `zfs` can say what *is*, but nothing on the system can reconstruct who restarted a jail at 03:00 last Tuesday. It is recorded because it cannot be re-derived, and it is discardable because nothing depends on it.

Every mutating operation continues to be recorded in the append-only JSON-lines audit log (`internal/audit`): timestamp, operation, target, command, exit code, success/failure, whether the command timed out, and an error summary.

A timeout is recorded as its own field rather than inferred from the exit code, because a killed process and a command that could not start both report `-1`. Distinguishing them matters: one is Jail Deck giving up on the system, the other is the system rejecting the command.

New fields are added with `omitempty`, so lines written before a field existed decode as its zero value. That constraint is inherent to an append-only log: old records are never rewritten, so the reader must tolerate every shape the writer has ever produced.

**Where each kind lives:**

| Kind | Home | Configurable via |
| --- | --- | --- |
| Operator configuration | environment, `.env` | — |
| Application state | `/var/db/jaildeck/state.json` | `JAILDECK_STATE_DIR` |
| Recorded history | `/var/log/jaildeck/audit.log` | `JAILDECK_AUDIT_LOG` |
| Derived state | not stored | — |

`/var/db` is for data a program needs in order to function — lose the pkg database and pkg is broken. The audit log is not that, so it belongs in `/var/log` despite being machine-readable. Being read back by a program has never moved anything out of `/var/log`: `wtmp` is append-only, read by `last(1)`, rotated, and lives there.

**The consequence, accepted deliberately:** `/var/log` invites `newsyslog`, and after a rotation `GET /api/audit` sees only the current file. History appears to truncate, with no error and nothing in the response to say so. This is the cost of treating the log as a log, and it is preferred to filing it under `/var/db` to avoid an honest consequence. Should richer history matter later, the trigger is recorded below.

Rotation is otherwise safe by construction: `Log` opens with `O_CREATE|O_APPEND` on every write and closes immediately, so Jail Deck never holds a descriptor across a rotation and needs no `SIGHUP` reopen handling.

**Still open, with explicit triggers rather than vague intent:**

- **Metrics history.** Live values need no storage; a trailing window is served by an in-memory ring buffer that may die on restart. Durable time-series storage becomes necessary only when the requirement is a chart *over time* that survives restarts. That is the trigger — not "we have metrics." A Prometheus `/metrics` endpoint (2026-10-21) meets that requirement outside Jail Deck: Prometheus keeps the history, so the trigger only fires if the chart has to live inside Jail Deck itself.
- **Audit log at scale.** Filtering currently scans the whole file. For a single operator this is harmless for years. Log rotation is the cheaper answer and should be tried first; richer querying is the trigger for anything more.
- **Structured application logging**, distinct from the audit trail. Not designed.

**If a database is ever adopted, it must be a pure-Go driver.** SQLite is the obvious candidate, but `mattn/go-sqlite3` requires cgo, which forfeits the same property that ruled out PAM in JD-003: a FreeBSD binary cross-compiles from another platform in seconds with no toolchain. `modernc.org/sqlite` is pure Go and preserves it.

### Rationale

The flat-file audit log works and nothing forces a change today. But the stronger reason for having no database is not simplicity — it is correctness.

A database here would be a **second source of truth that can disagree with the first**. If a stored record says a jail exists and `jls` says it does not, the dashboard has to choose, and any choice is sometimes wrong. For an application whose entire purpose is showing the operator the true state of the system (JD-012, and the "thin abstraction" principle), a cache that can drift is a liability, not a feature.

Deriving state instead makes whole categories of bug impossible rather than merely unlikely. It also pays off in the awkward cases: a crash midway through template preparation leaves "dataset extracted, no `@base` snapshot" — which is a meaningful, recoverable, *automatically correct* state, obtained for free. A job table recording "preparation in progress" would, after the same crash, be confidently wrong.

ZFS in particular is already a metadata store. `origin` and `creation` answer provenance and timestamp questions that look like relational data, and they cannot drift because they are not records *of* the clone — they are properties *of* it.

This decision is therefore not "a database is premature." It is that most of what would go in one is better answered by asking the system, and the parts that are not — a password hash, an API token — do not need one.

---

## JD-012 — Native Integration

**Status:** Locked

Jail Deck integrates existing FreeBSD facilities rather than replacing them:

```
service · jls · jail · jexec · zfs · pkg · fetch · freebsd-update · sysrc
rctl · cpuset · ifconfig · pfctl · bhyve tooling (per JD-019)
```

The second line was added 2026-10-21 with VNET jails, resource limits, PF visibility and VMs (JD-017 to JD-020).

### Rationale

Jail Deck is an orchestration and visualization layer built on top of FreeBSD, not an alternative implementation of its administration tools.

---

## JD-013 — Readability Over Cleverness

**Status:** Locked

The project favors explicit, predictable, maintainable implementations over clever abstractions: standard library before external dependencies, small focused libraries, straightforward project organization.

### Rationale

Long-term maintainability is valued over short-term convenience. This is also why the domain-driven organization (JD-010) is scoped to the domains the code has actually needed — not speculative bounded-context ceremony ahead of real requirements.

---

## JD-014 — Command Execution and Timeouts

**Status:** Locked

**No command runs unbounded.** The runner applies `Command.Timeout`, or a package default when that field is zero. The deadline is created inside the runner rather than by callers, so a caller cannot forget to set one — and each command gets its own fresh deadline rather than sharing a budget with whatever ran before it.

**Jail Deck's timeout is a backstop, not the primary limit.** FreeBSD bounds jail operations itself, and it cleans up after its own timeouts; a process killed from outside leaves nobody to clean up. The chain, with the values in use:

```
rcshutdown_timeout    90s   rc.shutdown's own watchdog          FreeBSD default
exec.timeout         120s   jail(8) caps each exec.* command    host configuration
stop.timeout          10s   SIGTERM grace before removal        FreeBSD default
Jail Deck backstop   150s   jail lifecycle operations           code constant
Jail Deck default     10s   everything else, e.g. jls           code constant
```

The inequalities are load-bearing. `exec.timeout` must exceed `rcshutdown_timeout`, or a slow shutdown is cut short and — per jail(8) — the jail "will not be removed", leaving it running. The backstop must exceed `exec.timeout` plus `stop.timeout`, so FreeBSD's orderly path always completes first and the backstop only fires when FreeBSD's own limits have failed.

Two of these values live on the host rather than in Jail Deck, so establishing `exec.timeout` belongs to host setup, not to the binary.

**Mutating operations are detached from client cancellation.** An operator closing a browser tab must not abort a jail stop half way through. `internal/jails` drops the request's cancellation with `context.WithoutCancel` before calling the system; the runner still enforces the command's own timeout. Read operations keep the request context — cancelling a `jls` nobody will read costs nothing.

**A command is terminated as a process group.** The child is started with its own process group, cancellation sends `SIGTERM` to the group rather than to the single process, and a `WaitDelay` bounds how long the runner keeps waiting afterwards.

**A timeout is distinguishable from a failure**, at both boundaries: HTTP `504 Gateway Timeout` rather than `500`, and a dedicated `timed_out` field in the audit entry (JD-011). Cancellation is not a timeout and must not be reported as one.

### Rationale

Every part of this exists because the obvious version does not work.

**Killing from outside is worse than waiting.** `jail(8)` responds to its own timeouts by removing the jail and cleaning up; an external `SIGKILL` mid-shutdown leaves a jail half-stopped with no one responsible for the rest. Deferring to the native limits is JD-012 applied to failure handling, not only to the happy path.

**Signalling one process is not enough.** `service(8)` is a shell script that runs `/etc/rc.d/jail`, which runs `jail(8)`. Killing only the direct child orphans the rest, and those orphans inherit the output pipes — so the runner's `Wait` blocks until they finish anyway. Without the process group and the `WaitDelay`, a timeout bounds nothing at all; it merely changes which error is eventually returned.

**Cancellation and timeout mean opposite things.** A timeout is Jail Deck giving up on the system. A cancellation is the operator walking away. Collapsing them into one status would make the audit log lie about which happened, and the distinction is fragile enough that both the runner and the error mapping have tests pinning it.

**504 rather than 500.** Nothing broke; Jail Deck stopped waiting for something it does not control. `500` claims a fault that did not occur, and `503` claims the service itself is unavailable, which is also untrue.

**Timeouts belong to the runner, not to callers.** Callers wrapping contexts would mean a slow operation consumes the whole budget, leaving nothing for the verification step that follows it — precisely when knowing the jail's real state matters most. It would also make "unbounded" the default for any caller who forgets.

This decision is about bounding individual commands. How a legitimately slow flow is represented to the operator is JD-008's, and it is settled: the call returns `202` and the operator polls the template. The two meet at one point — a detached stage still runs under a timeout, just a much larger one than `jls` gets.

**Amended 2026-10-21 — exec.** The exec endpoint (JD-016) is the one place a caller supplies a command's timeout. It is capped by a server-side maximum, so a caller can shorten the bound but never remove it, and the output kept is capped in size.

**The caps, settled 2026-10-24: 60 seconds by default, 5 minutes at most, and 1 MiB of each stream.**

Five minutes because the thing exec is for is an in-jail bootstrap — `initdb`, a service enable, a config written — and those are seconds. The minutes-long work is building a *template*, which is detached and polled precisely so that nothing holds a connection open for it (JD-008). A cap exists to stop a caller removing the bound, not to second-guess a legitimate install; when five minutes is genuinely not enough, the answer is that the work belongs in a template or behind something the caller polls, not a longer HTTP request.

1 MiB because of what commands actually produce, measured in a real jail: `ps auxww` 1.4 KB, `pkg info` 1.3 KB, `pkg help install` 10 KB — and `find / -type f` **2.85 MB**, which is the shape of the mistake the cap is for. Anything above a mebibyte of text is not something a caller reads out of a JSON response; it is a file to write in the jail and fetch deliberately. Each stream is capped separately, and a truncated one says so rather than quietly ending early.

---

## JD-015 — Frontend Architecture

**Status:** Locked

Settles what JD-009 left to Phase 1b. The UI is a plain **Svelte 5 + Vite** single-page application in TypeScript, not SvelteKit. State is held in runes; routing is a small hand-written router over `history.pushState`; styling is plain CSS with custom properties on `:root`; the API client is a hand-written `fetch` wrapper that centralises the error shape and the 401 path.

The JSON API is served under **`/api`**. Everything else is the SPA: unknown non-`/api` paths return `index.html` so client-side routing works, while unknown `/api` paths return a JSON error. Static assets are served without authentication — the login view has to be reachable — while every `/api` route except login stays behind `RequireAuth` (JD-003).

The Vite dev server proxies `/api` to the Go process, so development and production are same-origin and the session cookie behaves identically in both.

`web/dist/.gitkeep` is committed so `//go:embed all:dist` succeeds on a fresh clone; the `all:` prefix is required because `embed` skips dotfiles, and a `dist/` holding only `.gitkeep` otherwise fails to compile. When `index.html` is absent the binary serves a page saying to run the frontend build, rather than refusing to start. The placeholder is deliberately not `index.html` itself: that is the path the build writes, so committing it would leave the tree dirty after every build and invite a build artifact into git. Build tags switching between embedded and on-disk assets were rejected: they double the build matrix and make `go build ./...` behave differently for a contributor than for CI.

### Rationale

SvelteKit's value is server-side rendering, adapters and file-based routing. An embedded single-binary SPA uses none of it — `adapter-static` with `ssr = false` is configuration to switch off features that were never wanted, and the conventions remain to be learned regardless. Plain Vite produces an obviously static bundle, which is what `embed.FS` needs, and keeps one layer between the author and the browser rather than two (JD-013).

The `/api` prefix resolves a collision rather than expressing a taste. Without it the page route `/jails` and the API route `/jails` are the same URL, separable only by the `Accept` header — which fails for `curl` with no `Accept`, and leaves `/jails/{name}` ambiguous between a detail page and a resource. `docs/ARCHITECTURE.md` already documented `/api/*`; this aligns the code with the document rather than the reverse. The cost is a breaking change for API consumers, acceptable pre-1.0 with one known consumer.

Plain CSS over a utility framework or component library: `docs/UI.md` asks for something that does not "feel like a marketing dashboard or generic cloud console", and a component library supplies precisely an opinion about appearance. Tokens on `:root` also make `prefers-color-scheme` nearly free.

TanStack Query, a generated client and polling are premature at two domains. Refetch-after-mutation suffices; a jail operation can run to the 150-second backstop (JD-014), so per-row pending state matters more than a cache.

### Still open

- How a multi-minute template preparation is represented in the UI. JD-008 settled the API half; the page is not designed.
- Table-first or card-first for the jails list; browser `confirm()` or a custom dialog; when dark mode arrives. All reversible, none blocking.

---

## JD-016 — Automation API Contract

**Status:** Locked — 2026-10-21

Jail Deck has a second kind of API consumer: a program on the same host, driving it as a host agent. The first is Otter Labs Cloud's per-node worker. The API is designed so such a client can retry safely and correlate what it did:

- **Idempotent create.** `PUT /api/jails/{name}` carries the full jail spec (JD-017, JD-018). Absent → create it, `201`. Present and matching → `200`, nothing changed. Present and different → `409` with a field-by-field diff; Jail Deck never reconciles a running jail towards a new spec on its own. `POST /api/jails` remains for the UI.
- **Confirmed destroy.** `DELETE /api/jails/{name}?confirm={name}`, and the same `confirm` parameter on every destructive storage call. The confirmation lives in the API, so it binds the UI and automation equally. It refuses when the datasets have clones or holds, and says which.
- **Exec.** `POST /api/jails/{name}/exec` with `{ "argv": [...], "timeout": "60s", "env": {...} }` returns stdout, stderr and exit code. It runs `jexec <name> argv...` through `CommandRunner`: an argument array, no shell, audited, bounded by JD-014. This is root inside the jail, which is no more than the API already implies (JD-001) — it is JD-007's "hand off to in-jail provisioning" made callable.
- **Correlation.** An `X-Request-Id` header is recorded in every audit entry the request produces, so the client's own job id links both audit trails.
- **Transport.** A unix socket listener (JD-002) with token auth (JD-003).

**Exec was built 2026-10-24**, measured on isengard, 15.1-RELEASE-p5. Four things the decision did not say:

- **`jexec` hands the jail the caller's entire environment.** Measured: a variable set on the Jail Deck process appears inside the jail, which means `/usr/local/etc/jaildeck/jaildeck.env` and whatever else the service was started with are readable by anything run through this endpoint. So every exec runs **`jexec -l`**, which replaces it with a clean login environment — `HOME`, `PATH`, `SHELL`, `USER`, `LANG` and nothing of the host's. The caller's own variables go on top.
- **A caller's variables are applied with `/usr/bin/env`, and `env` has no `--`.** Measured: `env FOO=bar -- cmd` fails with "No such file or directory", so there is no separator to end the assignments with. `env` instead treats *any* leading argument containing `=` as another assignment — so a command whose first argument held one would silently become `env` printing the environment rather than the program the caller asked for. That combination is refused, naming it, rather than run.
- **The timeout is real, not advisory.** `jexec` does not fork: the process *is* the command, inside the jail, so killing it kills the command. The runner already signals the whole process group (`Setpgid`), so a command that forked goes with it.
- **Exec is root in the jail and takes no user.** `jexec -l -U <user>` needs that user to have a home directory in the jail — measured: `-U nobody` fails at `chdir: /nonexistent` — and nothing in scope needs it. Root is what this API already implies (JD-001, JD-003).

**A non-zero exit is a `200`.** The exec succeeded; the thing it ran failed, and its exit code is the answer. That is the opposite of JD-023's rule for Jail Deck's *own* commands, where a non-zero exit is the API's failure to report — here the exit code is the payload, and a client that reads the HTTP status instead of `exit_code` is reading the wrong field.

**There is no stdin, and no shell.** Jail Deck runs an argument array; a caller who wants a shell passes `["sh", "-c", "…"]` and has chosen it deliberately, which is a different thing from Jail Deck interposing one. A command that needs input writes a file in the jail first.
- **Change policy.** Breaking changes are allowed before 1.0 but each is recorded under an **API** heading in the changelog. A client pins a Jail Deck version and runs contract tests before upgrading.

**The idempotent `PUT` was built 2026-10-24.** What was left to decide was the hard half: what "matching" means.

**It is the jail's own configuration file that answers.** Jail Deck renders the stanza the spec would produce — against the path the jail's dataset actually reports and the epair unit its existing stanza already claims — and compares that with the stanza on disk, setting by setting. Nothing about the comparison is a list of fields to check, so no field can be forgotten by a comparison that does not know about it: a parameter added to the allowlist next month is compared the day it is generated. It is also JD-011 read literally, because the file is what the host says the jail is (JD-004).

Settings rather than bytes. A stanza written by an older Jail Deck has its lines in a different order — the limits work moved `exec.poststop` below `exec.prestop` — and that is not a difference in the jail. Comments, blank lines and the quoting of a value are ignored for the same reason; everything else is compared, including a bare flag an operator added by hand, which appears as a setting with no value.

**Two things are not in the file, and are read where they live.** `template` is the root's clone origin, from `zfs get origin`; `autostart` is `sysrc -n jail_list`. A host that never set `jail_list` answers "unknown variable" and exits 1 — measured — which is an empty list rather than a failure.

**`start` is deliberately not compared.** It says whether to start the jail *once it has been configured*, which is an instruction about the create and not a property of the jail. Comparing it would make a jail an operator stopped for maintenance refuse every `PUT` until they started it again, and acting on it would be reconciliation — the one thing this decision rules out. The response reports whether the jail is running, and the client decides.

A jail with no file of its own in `jail.conf.d` is refused rather than compared, on the same grounds the destroy refuses one (JD-030): its block is in `/etc/jail.conf`, which belongs to the operator and which Jail Deck never writes.

### Rationale

**The desired state lives in the client, not here.** A platform like Otter has a desired-state model — that is its job. Jail Deck stays a desired-state-free executor and reporter, which is what JD-011 asks for. An idempotent `PUT` gives the client everything it needs from that position: after a crash it repeats the call, and the answer (`201`, `200` or `409`) tells it exactly where it stands, derived from the system rather than from a record of what Jail Deck thinks it did.

**Synchronous is better for automation, not worse.** Jail creation is fast (JD-008). A client with its own job queue gains nothing from a Jail Deck job handle except a second piece of state that can disagree with the first — which is why, when JD-008 finally did detach something, it still issued no handle: the template's own URL is what you watch.

**Generic primitives only.** Every capability here passes the scope test in `docs/SPEC.md`: an operator on their own box can use `PUT`, `exec` and `confirm` from a script just as well. Nothing in the API knows what a customer, a quota or a database is.

---

## JD-017 — VNET Networking Model

**Status:** Locked — 2026-10-21. Supersedes "VNET jails" in the SPEC's deferred list.

A jail spec may ask for VNET networking:

```json
"network": { "mode": "vnet", "bridge": "bridge-tenant", "private": true,
             "ip4": "10.64.0.23/22", "gateway": "10.64.0.1" }
```

- The epair is created in the generated stanza's `exec.prestart` and destroyed in `exec.poststop`, so a jail started with `service jail start` from a shell gets the same network as one started by Jail Deck.
- The host end joins the named bridge, as a `private` member when asked, so jails on the same bridge cannot reach each other at layer 2.
- The address and default route are set inside the jail.
- **The bridge is the operator's.** Jail Deck adds and removes only its own epairs; it never creates a bridge, adds a physical interface to one, or edits `rc.conf` network settings. Host Setup reports whether the named bridge exists.
- `ip4=inherit` jails remain supported, unchanged.

**Built 2026-10-24**, measured on isengard, 15.1-RELEASE-p5. Four things the decision did not say, settled by measuring:

- **The epair unit is fixed in the stanza, and Jail Deck picks it.** `ifconfig epair create` allocates a unit but prints only the `a` end, so the peer's name is not knowable in advance — and both are needed before the jail runs, by `vnet.interface` and by `exec.poststop`. The unit is chosen from what no interface uses *and* no jail configuration claims: the host answers for running jails, the files in `jail.conf.d` for stopped ones whose stanza will create a unit the moment they start. Two creates racing can still be handed the same unit; the loser's start fails at `already exists` and is cleaned up like any other failed start.
- **The cleanup is one command.** `ifconfig <host end> destroy` takes the jail end *and* the bridge membership with it, so the orphan sweep needs neither a `deletem` first nor the bridge's name — and has no second command that can fail after the first succeeded. `exec.poststop` is one line for the same reason.
- **A VNET jail's loopback starts down.** The stanza brings `lo0` up, sets the address and adds the route *before* `/bin/sh /etc/rc`, so the services rc starts find an interface that is already configured.
- **`private` isolates private members from each other, and only from each other.** Measured: private→private is blocked, private→non-private works, private→the bridge works. The sentence above is accurate only where every jail on the bridge asks for it; a jail sharing a bridge with a non-private member is not isolated from it.

A VNET jail sets no `ip4.addr`, so neither `jls` nor its configuration reports an address for it and the jail listing shows none. The address is in the jail's own stack. A stanza cannot carry it as metadata either: an assignment jail(8) does not recognise is a hard error (`unknown parameter`), measured.

### Rationale

VNET gives each jail its own network stack, which is what makes a jail safe to hand to an untrusted workload: no shared `127.0.0.1` (the very risk JD-003 describes), its own firewall view, and isolation enforceable at the bridge. The line between what Jail Deck writes and what the operator writes follows the SPEC: jail creation may write what jail creation needs — the stanza and its epair — and nothing broader. A bridge is host network policy.

---

## JD-018 — Resource Limits and the Jail Parameter Allowlist

**Status:** Locked — 2026-10-21

A jail spec may carry limits and jail(8) parameters:

```json
"limits": { "memoryuse": "640M", "vmemoryuse": "1G", "maxproc": 100,
            "openfiles": 2048, "pcpu": 100, "cpuset": "0-3" },
"params": { "sysvshm": "new", "sysvsem": "new", "sysvmsg": "new",
            "devfs_ruleset": 4, "securelevel": 3, "enforce_statfs": 2 }
```

- **Limits** are rctl rules plus a cpuset, applied when the jail starts and removed when it stops. They require `kern.racct.enable=1`, a loader tunable that Host Setup checks and reports; Jail Deck refuses a spec with rctl limits on a host without it rather than creating a jail that silently runs unlimited.
- **Parameters** come only from an **allowlist**. The API rejects anything else, naming the parameter. The allowlist never includes `allow.mount*`, `allow.raw_sockets`, `children.max` above 0, or `vnet` combined with `inherit`.
- **devfs** references a ruleset number the operator installed. Jail Deck never appends to `/etc/devfs.rules` at runtime.
- **Child datasets and user properties** (JD-007, JD-004) are part of the same spec, so one `PUT` describes the whole jail.

**Built 2026-10-24**, measured on isengard, 15.1-RELEASE-p5. **Limits are applied by hooks**, which settles the question this decision left open, and six things it did not say were settled by measuring:

- **A hook that fails takes the jail down with it.** An `exec.poststart` command exiting non-zero makes jail(8) destroy the jail it has just created — measured with a deliberately invalid rule — so a jail whose limits could not be applied never runs. That is "refusing beats running unlimited" enforced by jail(8) itself, on every start, including the ones Jail Deck knows nothing about. `service jail start` still exits 0 throughout; the `jls` confirmation from JD-030 is what notices.
- **Rules outlive the jail.** A jail removed without `rctl -r` leaves its rules in the kernel, keyed to the name, and the next jail of that name silently inherits them — a rule can even be added for a jail that does not exist. So a create refuses a name that already carries rules, naming them and the command that clears them, and a destroy sweeps whatever is left the way it sweeps a leftover epair.
- **A start that fails part way through `exec.poststart` keeps the rules it had already added**, and `exec.prestop` does not run. Jail Deck removes them itself, as it does the epair.
- **`rctl -l jail:<name>` does not work** — "Operation not supported". The listing form for a jail subject is the bare filter, `rctl jail:<name>`, which exits 0 with no output when there are none and matches the name exactly rather than as a prefix. `rctl -r` on a name with no rules exits 1, "No such process", so the removal is only asked for when the listing found something.
- **`pcpu` takes `deny`, not `throttle`**, which is the reverse of what rctl(8)'s description of `throttle` suggests: `throttle` on a jail subject is "Operation not supported".
- **A `devfs_ruleset` the kernel has not loaded gives the jail the host's whole `/dev`** — `ada0`, `mem`, `kmem`, `io`, `bpf` — silently, while `jls` reports the ruleset number that was asked for. An unloaded number names an empty ruleset, and an empty ruleset hides nothing; a stanza with no `devfs_ruleset` at all gets the restricted set. The number is therefore checked against `devfs rule showsets` before anything is created. It is the one check here that exists for confinement rather than for accuracy.

The allowlist as built is nine parameters: `devfs_ruleset`, `securelevel`, `enforce_statfs`, `children.max` (`0` only, which is what "never above 0" comes to in practice), `sysvmsg`, `sysvsem`, `sysvshm`, `allow.chflags` and `allow.set_hostname`. Each carries its own rule — a range, an enumeration or a boolean — so a value out of range is refused as precisely as an unknown name. `allow.sysvipc` is deliberately *not* on it: the three `sysv*` parameters set to `new` give a jail its own IPC namespaces, which is the same capability without the host's.

Parameters Jail Deck writes itself — `path`, `host.hostname`, `ip4`, `vnet`, the `exec.*` hooks — are refused with a different message, naming the spec field that sets them instead. An operator writing `"params": {"path": …}` has made a different mistake from one writing `"params": {"allow.mount": true}`.

Two values are validated here rather than left to the host (JD-012), because the host's own refusal says nothing useful: a size like `640X` and a cpu list like `abc`. `cpuset` answers the second with "Resource deadlock avoided".

### Rationale

**The allowlist is the API's security boundary.** A token holder can already do anything root can (JD-003), but the API should not make it *easy* to create a jail that can escape its own confinement. A denylist fails open the day jail(8) gains a parameter; an allowlist fails closed.

**Refusing beats running unlimited.** A jail that was asked for a 640M memory limit and silently got none is worse than an error: the operator believes something that is false.

### Settled

- **Limits are applied by `exec.poststart` and removed by `exec.prestop`**, as this decision leaned. Measuring made the case stronger than the original reason: a jail started from a shell gets the same limits, *and* a jail whose limits cannot be applied does not start at all. The adapter would have had to re-implement both, and could only have managed the second by stopping a jail it had just started.

### Still open

- `allow.raw_sockets` is refused for every jail. That is right for one sharing the host's network stack and arguably too strict for a VNET jail with its own. Revisit when something actually needs `ping` inside a jail.

---

## JD-019 — Virtual Machines (bhyve)

**Status:** Locked — 2026-10-25. Scope locked 2026-10-21; mechanism **A, `vm-bhyve`**, chosen by the spike below (#17)

**Scope (locked).** Jail Deck gains a `vms` domain mirroring jails: list and inspect; create from an image template with CPU, memory and cpuset sizing, a zvol disk and a cloud-init seed; start, stop, restart, destroy with `confirm`; a tap on an operator-owned bridge, as JD-017 does for epairs; serial console through the API later. VM state is derived from the system, never stored (JD-011). The host prerequisite is `VT-x` with `EPT` and `UG` in `dmesg`, reported by Host Setup.

**Mechanism (open).** JD-005 chose `service jail` because it is the standard administrative interface. bhyve has no equivalent in base. The candidates:

| Option | Fit | For | Against |
| --- | --- | --- | --- |
| **A. `vm-bhyve`** (`sysutils/vm-bhyve`) | JD-005/JD-012: drive an established admin tool; its per-VM config files and datasets are derivable state | Mature; supervision, taps, UEFI, ISO and cloud images; cloud-init support (to verify) | A dependency outside base; its maintenance cadence needs checking |
| **B. Direct `bhyve(8)` + `bhyvectl` + `daemon(8)`** | JD-012: base tools only | Full control; no extra dependency; the most to learn | Jail Deck owns supervision across its own restarts, tap lifecycle and host reboots — more code |
| C. libvirt | Weak: a daemon and abstraction foreign to FreeBSD conventions | Feature-rich | Heavy runtime dependency; needs `libvirtd`; cgo bindings |

Spike A and B at the start of the Virtual Machines milestone, then lock this decision. Leaning **A** for consistency with JD-005, unless the spike shows gaps in cloud-init or maintenance. **C is rejected.**

### The spike, 2026-10-25 (#17)

The same Debian 13 cloud image (`debian-13-genericcloud-amd64`, checked against Debian's `SHA512SUMS`) was built both ways on isengard, 15.1-RELEASE-p5. Each guest had a 4 GiB zvol, a cloud-init seed, a tap on an isolated bridge with no physical member, 1 vCPU and 1 GiB. isengard was returned to a recorded baseline afterwards — interfaces, datasets, modules, packages, sysctl and a byte-identical `rc.conf` — and its jails and the Jail Deck service ran untouched throughout.

**The lab VM could not host it.** bhyve nested under the laptop's KVM froze the whole VM the moment the guest ran, and QEMU refused to continue it ("Resetting the Virtual Machine is required"). bhyve work is tested on isengard.

**Cloud-init works, with the seed exactly as specified.** `makefs -t cd9660 -o rockridge -o label=cidata` writes the volume ID as **`CIDATA`** — ISO 9660 upper-cases it — and Debian's cloud-init found it anyway (`nocloud`). It applied the hostname, a static address and an SSH key in both approaches. `vm-bhyve` builds its seed with the same `makefs` invocation. A cold boot reached SSH in 15–20 seconds.

**What running bhyve needs, measured — the list direct bhyve would make Jail Deck own:**

| | measured | direct bhyve | `vm-bhyve` |
| --- | --- | --- | --- |
| guest reboot | bhyve **exits 0** and is gone | Jail Deck reruns it | `vm-run` reruns it: back in 17 s |
| guest poweroff | bhyve **exits 1** | Jail Deck stops | `vm-run` stops |
| bhyve's error, or its instance destroyed underneath | **exits 4** | Jail Deck decides | `on_fault`, default stop |
| after any exit | the instance **stays in `/dev/vmm`** until `bhyvectl --destroy` (rerunning without it reuses it) | Jail Deck destroys it | `vm-run` destroys it |
| the tap | bhyve accepts **only `tapN`** — a renamed tap is refused, so a VM cannot own its tap by name | Jail Deck allocates and labels | a fresh tap per run, described `vmnet/<vm>/<n>/<switch>`, destroyed on stop |
| the MAC | cloud-init matches the NIC by it, so it must not change | Jail Deck persists one | written into the guest's config |
| host-side stop | SIGTERM is the ACPI power button: a booted guest powered off in 4–5 s. **It is ignored before the guest OS is up** — measured in both | Jail Deck times out and forces | `vm stop`, then `vm poweroff` (`bhyvectl --force-poweroff`) |
| host boot | nothing starts a guest | Jail Deck's own startup | `rc.d/vm` starts `vm_list` (read from the code; isengard was not rebooted) |
| console | — | Jail Deck sets up `nmdm` | an `nmdm` pair per guest |

The exit statuses are bhyve's own, and `vm-run`'s handling of them was read in its source and then observed: restart on 0, stop on 1 or 2 unless `vm restart` asked otherwise, `on_fault` for anything else. Direct bhyve is not "fewer moving parts" — the parts are the same, and the supervisor has to be a process that outlives Jail Deck. That is `vm-run` written again, in Go or in shell.

**Maintenance looked abandoned and is not.** The original `churchers/vm-bhyve` has had no commit in a year; it moved to **`freebsd/vm-bhyve`**, which had a commit four days before the spike and at least a hundred in the year before it. FreeBSD's package is 1.7.5. A check of the first repository alone would have recorded the opposite conclusion.

**Size.** `vm-bhyve` is 6,767 lines of shell, 1,124 of them `vm-run`. It covers far more than Jail Deck needs — VNC, passthrough, grub, other guest types — and none of that is code Jail Deck carries.

### Decided: A, `vm-bhyve`

Every row of that table is needed whichever approach is taken. `vm-bhyve` does all of it and is actively maintained, and driving it is the JD-005 position: an established administrative tool, its per-guest files and datasets read as derivable state.

**The constraints the spike found, which the milestone's design has to keep:**

- **`vm start` does not detach.** It returns at once, but `vm-run` and its bhyve stay in the caller's process group and session — measured. Jail Deck's command runner puts each command in a process group of its own and kills that group on a timeout, so `vm start` runs under `daemon(8)` (or `setsid`), never as a plain child. Direct bhyve under `daemon(8)` was in its own session; `vm start` was not.
- **`vm-bhyve` must be an enabled service.** It refuses to run without `vm_enable="YES"` and a `vm_dir` in `rc.conf`, and it starts `vm_list` at boot. Both are the operator's, like `jail_list` — Host Setup reports them, as it reports racct.
- **It owns every bhyve on the host.** Its shutdown `stopall` "will also stop instances not started by vm-bhyve" (its own comment). Jail Deck never starts bhyve any other way on a host that uses it.
- **Images are zvols that Jail Deck writes or clones.** `vm-bhyve`'s cloud-image import needs `qemu-img` (`sysutils/qemu-tools`) even for a raw image. It is not used: `vm create -C` without `-i` builds the guest and its seed, and the disk was filled directly — measured. That keeps image templates on the release-template pattern (JD-027) and takes no `qemu-tools`. *Built in JD-037, which found that the disk has to be filled with GEOM kept out of it: `volmode=dev`.*
- **A stop has a deadline.** `vm stop`, a timeout, then `vm poweroff`, because the power button is ignored until the guest has booted. *Built in JD-036, which also found that a guest with no OS, sitting in UEFI, answers the button at once: what ignores it is an OS that is still booting.*
- **Its network config adds `dhcp6: true` and `accept-ra: true`** to a static IPv4 address. Harmless on an isolated bridge and worth knowing on one that is not.
- **State is read, not kept** (JD-011): `vm list`, `/dev/vmm`, the `vm-run` process, the guest's dataset and configuration, and a tap found by its description.

### Rationale

Jail Deck's owner needs Linux VMs on the same host as the jails, managed the same way. Excluding bhyve was a scope choice, not a technical one, and the reason for it — not chasing Sylve feature-for-feature — still holds: VMs come in because a real consumer needs them, with the smallest surface that serves it.

---

## JD-020 — Firewall (PF) Scope

**Status:** Locked — 2026-10-21

PF support grows in phases, and one thing is never done:

| Phase | Capability | Commands |
| --- | --- | --- |
| **1. Visibility** (read-only) | Loaded ruleset with counters, anchors, tables and their contents, state count, PF enabled or not, recent `pflog` lines | `pfctl -s rules -v`, `-s Anchors`, `-s Tables`, `-s info`, `-t <table> -T show` |
| **2. Tables** | Add or remove addresses in named tables the operator declared in `pf.conf` | `pfctl -t <table> -T add/delete` |
| **3. Owned anchors** | Load rules into `jaildeck/*` anchors that `pf.conf` includes once; validate with `pfctl -nf` before loading and roll back on failure | `pfctl -a jaildeck/<n> -nf … && pfctl -a jaildeck/<n> -f …` |
| **Never** | Write `/etc/pf.conf`, or toggle `pf_enable` | — |

Phase 1 is a planned milestone. Phases 2 and 3 wait for a real need.

### Rationale

The firewall policy is the most security-critical file on the host. The operator keeps authorship of it; Jail Deck may only work inside boxes the operator explicitly opened — a table they declared, an anchor they included. This is the deliberate opposite of overwriting `pf.conf`, as Sylve does. Sylve's validate-then-rollback idea is worth adopting for phase 3.

Read-only visibility comes first because it is useful immediately and costs nothing in risk: a consumer like Otter uses a static, address-based policy in `pf.conf` that never changes per tenant, and only needs to *see* that it is loaded and matching.

---

## JD-021 — ZFS Pool Choice and the Jails Dataset Layout

**Status:** Locked — 2026-10-22

The layout is fixed: `<pool>/jails` mounted at `/usr/local/jails`, with `media`, `templates` and `containers` beneath it inheriting that mountpoint. JD-006 names those paths; this decision is about *which pool* and *what may be done to get there*.

**Jail Deck never guesses the pool.**

| Pools imported | `JAILDECK_ZFS_POOL` | Result |
| --- | --- | --- |
| one | unset | adopted |
| several | unset | `zfs_pool` **fails**, listing them |
| any | names a listed pool | that pool |
| any | names an absent pool | `zfs_pool` **fails**, listing them |
| none | — | `zfs_pool` **fails** |
| no ZFS — `zpool` exits non-zero | — | `zfs_pool` **fails** (JD-006) |

A pool that cannot be chosen **fails the check but never refuses startup**: listing and running jails do not need the layout, and a readiness report that cannot be reached is useless.

**Ensure, never clobber.** `PUT /api/host/layout` and `jaildeck init` are one service method. It creates only what is missing, parent first, and when nothing is missing it runs no `zfs create` at all. Two states refuse the whole request before anything is created:

- a layout dataset that **exists but is mounted somewhere else**
- a **missing** layout dataset whose expected mountpoint **another dataset already holds** — on any pool, the selected one included

Both are a `409` naming the datasets. Jail Deck will not move a mounted dataset, and will not mount a new one over an existing one: either may hold live jails. Checking the second costs a read of every pool whenever anything is missing, so one unreadable pool makes the layout check `unknown` rather than `fail` — losing the answer is the right way to fail here, because the wrong answer is "run `jaildeck init`".

Afterwards the layout is read back and reported as the system shows it, not as the request believes it made it (JD-011).

Two things are **not** detected. A plain directory with content at a layout path: the `HostSystem` port has no file-system method, and creating the parent would mount over it. And ZFS's own `mounted` state, as opposed to the `mountpoint` property — that needs real captured output before a parser is written for it.

### Rationale

Both rules come from the deployment host's own topology, not from caution in the abstract.

`zpool list` prints `backup`, `tank`, `zroot` on isengard, and the first of those holds a `zrepl` replica of the jails layout with **every mountpoint `none`**. Adopting the first pool printed, or matching the layout on dataset names, reports a fully provisioned host whose datasets are mounted nowhere. Hence an explicit pool, and a layout check that compares mountpoints against **full dataset paths** — another host has a pool called `media`, which is also one of the layout's names.

The mount-over refusal is the same host read the other way round. That replica lives at `backup/isengard/tank/jails`, not `backup/jails`, so with `JAILDECK_ZFS_POOL=backup` the layout is simply *missing* — and creating `backup/jails` with `mountpoint=/usr/local/jails` would have mounted it over the live `tank/jails`, where the jails actually run. The readiness remedy would have been `jaildeck init`: the report would have told the operator to do it.

---

## JD-022 — Matching a Jail to Its Datasets

**Status:** Locked — 2026-10-23

A dataset belongs to a jail when it is **mounted at the jail's `path` or under it**. The jail's own dataset is the one mounted exactly there; everything deeper is a child. Where two jail paths both match, the longest wins, so a jail nested inside another's directory keeps its own datasets.

The alternative was the naming convention — `<pool>/jails/containers/<name>`, which JD-021 fixes and host readiness already enforces. Mountpoint matching was chosen because it is **truthful rather than conventional**: it works for a jail placed outside that layout, and a jail not on ZFS at all reports nothing instead of reporting the datasets of whatever happens to carry its name. A jail whose stanza sets no `path` matches nothing rather than everything.

**Sizes cross the API as exact bytes.** `zfs list` prints `1.41G` and `897G` unless given `-p`; those cannot be compared or totalled, and the API is read by automation first (JD-016). Formatting a byte count for a human is the web interface's job. `referenced` routinely exceeds `used` on a jail cloned from a template, because a clone still refers to the template's blocks — the reason JD-007 puts quotas on a child dataset.

**The read is batched per host, never per jail**: one `zfs list` and one `zfs get`, scoped to `JAILDECK_ZFS_POOL` when the operator set one and to every imported pool when they did not. Widening rather than adopting a pool by guesswork is deliberate: judging whether a pool is usable belongs to host readiness (JD-021), and a second opinion here is how the two would come to disagree.

**A failed storage read degrades the listing instead of failing it.** The jails come back without their datasets and the reason is logged at `WARN`. Seeing a stopped jail is how an operator starts it (JD-004), and an unreadable or suspended pool must not take that away.

**The consequence, accepted deliberately:** absent storage in a response means either "this jail has none" or "ZFS could not be read", and nothing in the response tells the two apart. The log does. Should a client ever need to distinguish them, the cheap fix is a field added with `omitempty`, as every other addition here has been.

### Rationale

Both halves of this are the same choice twice: prefer what the system actually reports over what it is supposed to look like. A convention-based match is a second source of truth about where a jail's data lives, and JD-011 rejects those for the same reason — the moment it disagrees with the mount table, the dashboard has to pick, and either answer is sometimes wrong.

**Known duplication, recorded rather than fixed:** `host.HostSystem` reads `zfs list -H -o name,mountpoint -r <pool>` for the layout check while `storage.StorageSystem` reads the same datasets with their sizes. Two commands where one would do. Readiness predates the storage domain and its fixtures are tested against the narrower command; folding one into the other is worth doing when something else makes that code move.

---

## JD-023 — ZFS Property Writes

**Status:** Locked — 2026-10-23

Jail Deck writes two kinds of ZFS property, and the difference decides everything else.

**Native properties are allowlisted, and the list is five long:** `mountpoint`, `refquota`, `recordsize`, `compression`, `atime`. This ZFS reports **108 properties, over sixty of them editable**, and the editable set includes `sharenfs` (which would publish a jail's filesystem over NFS), `canmount` (which would stop it mounting), `exec`, `setuid` and `devices` (mount-time security semantics), `jailed` (ZFS delegation) and `keylocation` (encryption). An allowlist exists so that widening it is a deliberate, reviewed act rather than whatever `zfs` happens to permit, which is why it starts at the five the consumer actually needs.

`quota` is deliberately **not** among them. JD-007 puts the limit on `refquota` because a clone's `referenced` space includes the template's blocks; offering both invites the mistake that decision exists to prevent.

**`mountpoint` is settable exactly once, at creation.** Changing it on a running jail's dataset remounts it elsewhere and leaves the jail looking at a directory no longer backed by what it was. So there are two lists: everything may be set by `zfs create`, and everything but `mountpoint` may be changed afterwards — growing a quota being the ordinary operational need with no other path through this API. Asking for a mountpoint an existing dataset does not have is **409 with both values**, not a silent move: the caller is describing a different dataset than the one that is there.

**User properties are passed through and never interpreted** (JD-011). The only rule is ZFS's own — the name contains a colon — plus one of this code's: no equals sign or whitespace in the name, because `zfs set` takes `property=value` as a single argument. The value is not inspected at all.

**Native property *values* are not validated either.** `zfs` answers `recordsize=7K` with "must be power of 2 from 512B to 16M", and nothing written here would say it better (JD-012). Re-implementing those rules would only add a second opinion that can be wrong on its own.

**A command that ran and exited non-zero is reported as 400, not 500.** A client told 500 will retry a value that can never work, which defeats JD-016's retry contract. The distinction available is only "ran and exited non-zero" against "never ran or timed out" — both of the latter report exit code `-1` — so a genuinely server-side refusal such as permission denied also reports 400. `zfs`'s own sentence is in the body either way, which is what the operator reads.

**Writes are idempotent and audited.** `PUT` creates or converges; a repeat is `200` with `created: false`. Updatable properties are applied rather than compared first: deciding "10G is already 10G" would mean parsing a size this package has no business interpreting, and setting a property to the value it already holds is a no-op in ZFS. Every command writes one audit line with the command itself, so `zfs inherit`'s one-property-per-call shape really does produce one entry per removal.

### Rationale

The two kinds of property are not two flavours of the same thing. A native property changes how the filesystem behaves and is Jail Deck's responsibility; a user property is a string the consumer owns and Jail Deck is merely a courier for. Treating them alike would mean either validating tags nobody has the right to validate, or letting `sharenfs` through because it arrived in the same map.

**Verified on isengard, not only in tests.** Creating `tank/jails/containers/ottermq/data` with `refquota=2G` through the API put the quota where JD-007 says it belongs: `df` **inside the running jail** reports 2.0G rather than the pool's 897G, with the mount point shown as `[restricted]` by `enforce_statfs`. That is the behaviour the whole arrangement exists for.

### What is reported back — added 2026-10-23 (#55)

The read side is the same allowlist pointed the other way. Each dataset in a jail's listing carries the four native properties **it sets locally**, as the strings `zfs` prints, so a `refquota` reads `2G` and a client can send that same value back.

**Not the value in effect — the value this dataset sets.** A property inherited from the dataset above is absent. ZFS reports it on a `zfs get` without `-s local`, and showing it would be friendlier right up to the moment someone edits it: the write would create a *local override* on the child rather than change the setting they were looking at, and the parent would go on applying its value to every other dataset beneath it. An absent property is an honest "this dataset does not decide that".

It costs nothing to read. `Inventory` already runs one `zfs get -H -r -o name,property,value,source -s local all` for the tags; the native properties were in that output all along and were being discarded.

**Not every native property, the same four.** `canmount` is set locally on real datasets and is outside the list for the reason it is outside the write list: it would stop a jail's dataset mounting. Reporting the set Jail Deck is willing to write keeps one answer to "what can I change here".

`mountpoint` is the exception on both sides — reported as its own column, and never written to an existing dataset.

---

## JD-024 — Snapshots: Recursion, Ordering and Refusal

**Status:** Locked — 2026-10-23

**A jail-level snapshot is recursive; a rollback cannot be.** `PUT /api/jails/{name}/snapshots/{s}` runs `zfs snapshot -r`, taking the jail's dataset and every child **at one point in time** — verified on the deployment target, where one call produced `ottermq@before-upgrade` and `ottermq/data@before-upgrade` sharing `createtxg 186346`. That atomicity is what JD-007's re-clone path needs: a jail's root replaced from a newer template while its data dataset stays consistent with the moment the root was captured.

Rollback gets no such treatment, because ZFS offers none. `zfs rollback` has no flag that descends into children, and its own `-r` means something else entirely — "destroy the snapshots newer than this one". So a jail-level rollback returns the jail's own dataset and **leaves its children where they are**. This is visible in practice: a recursive snapshot followed by a jail-level rollback leaves the child's snapshot of the destroyed name still present. Rolling a child back is its own call, which is the honest shape — for a jail whose state lives in a child, rolling the data back without touching the userland is usually what was wanted anyway.

**Ordering is by transaction group, never by timestamp.** `creation` has second granularity, so two snapshots taken in the same second tie on it, and "which of these is newer" is exactly the question that decides what a rollback destroys. `createtxg` is ZFS's own total order and never ties. It is in the API as `create_txg` for the same reason: a client deciding what is newer needs it too.

**Refusals are decided before anything destructive runs.** What blocks a destroy is asked structurally — `zfs get clones` and `zfs holds` — rather than read out of the error `zfs destroy` would raise. Two reasons: parsing a refusal means depending on its wording, and more importantly a check that happens first is a refusal, while one that happens after is a report of something already attempted. The commands' own failures are still mapped, as defence in depth.

- **Newer snapshots** block a rollback, which names every one it would destroy. `destroy_newer=true` accepts losing exactly those. Two parameters rather than one, because `confirm=` and "yes, lose those" are different questions.
- **Clones and holds** block both a destroy and a rollback, and **cannot be overridden through this API at any level of insistence**. Getting past a clone needs `zfs destroy -R`, which takes the clone with it; a hold exists precisely to say "not this one". Releasing a hold or destroying a clone is a separate, deliberate act with its own blast radius.

**`confirm=` is the snapshot's full `dataset@snapshot`**, not the word already in the path. A script looping over jails with one snapshot name in a variable would otherwise confirm every one of them by accident, which is the failure the confirmation exists to prevent (JD-016). The expected value is in the error message: this guards against a mistake, not against a client that can already list the snapshots.

### Rationale

Every part of this is the same preference stated four times: let the system answer, and ask it before acting rather than after. `createtxg` over a timestamp, a structured dependency check over a parsed error message, a refusal that names what is in the way over one that just says no, and a confirmation specific enough that it cannot be satisfied by accident.

The one place that preference yields is recursion, and it yields to the tool: `zfs snapshot` can take a jail atomically and `zfs rollback` cannot put one back the same way. Pretending otherwise — looping over the children and calling it a recursive rollback — would offer an atomicity that does not exist.

---

## JD-025 — Moving a Dataset Out of a Jail

**Status:** Locked — 2026-10-23

Destroying a jail takes its children and their snapshots with it (JD-007), so a dataset worth keeping has to **stop being a child** before the jail goes. Moving it is three commands, in this order, each audited on its own:

```
zfs rename -u  <jail>/data  <pool>/jails/retained/<name>
zfs unmount                 <pool>/jails/retained/<name>
zfs mount                   <pool>/jails/retained/<name>
```

**`-u` is the whole design, and it is not optional.** A plain `zfs rename` unmounts the dataset and remounts it at its new mountpoint inside the same command. On a running jail that is the filesystem disappearing from under a live process, decided by `zfs` rather than by anyone. Measured on the deployment target: after a plain rename of a running jail's data dataset, `ls /data` inside that jail answered `No such file or directory`. With `-u` the rename is invisible — the mount stays exactly where it was and the jail keeps reading and writing — and moving it becomes a separate step that happens when this code says so.

**But the move is still completed, in the same call.** `-u` alone leaves the dataset named in one place and mounted in another, and a jail with a foreign filesystem mounted inside it **cannot be destroyed**: `zfs destroy` answers `pool or dataset is busy`. A half-moved dataset therefore blocks the one thing this feature exists to enable. Both shapes were measured; the difference is the reason the unmount and the mount are not left to the caller.

The cost, accepted: a running jail loses that path at the unmount. The workflow this serves starts by stopping the jail, and the response says `remounted: true` so a caller knows the filesystem moved rather than only the name.

**The destination is `<pool>/jails/retained/<name>`, created on demand.** It sits beside `media`, `templates` and `containers` so that one destroy of `<pool>/jails` is still the whole story, and the pool is taken from the dataset being moved rather than from configuration — a dataset is already on exactly one pool, and that is the one its retained sibling belongs on. It is deliberately **not** added to the layout host readiness checks (JD-021): a host that has never retained anything is not missing something.

The name is given by the caller, not derived from the jail. Retained names share one namespace per pool, so two jails whose data datasets are both called `data` would collide under any derivation; being asked for a name makes that collision the caller's to resolve, and a `409` when it is already taken.

**A dataset that was not mounted is renamed and left alone.** Mounting it would be a change nobody asked for, and `zfs mount` on something already mounted is an error rather than a no-op — as is `zfs unmount` on something that is not. The mount state is therefore read before the rename, as its own question.

### Rationale

The whole decision is about who chooses the moment a filesystem disappears. `zfs rename` will choose it for you, inside a command whose name says nothing about mounting. `-u` takes that choice back, and completing the move in the same call means the caller still gets one operation with one outcome rather than a state they can forget to finish.

**Verified end to end on isengard**, with a jail configured for the purpose and never started: retain the data, `zfs destroy -r` the jail (which succeeded, where the half-moved form had failed `busy`), recreate the jail's dataset, restore the data — marker file and `refquota` both intact, mounted back where they belonged.

---

## JD-026 — Re-cloning a Jail's Root

**Status:** Locked — 2026-10-23

A jail's root is a clone of a template snapshot (JD-007), so upgrading one means replacing that root with a clone of a newer snapshot. Everything the jail is supposed to keep lives in a child dataset, and the child has to survive the replacement.

The sequence, in this order, and the order is the design:

```
stop the jail                     only if it was running
move every child out              retained/<jail>-<child>
park the old root                 retained/<jail>-root-<its origin snapshot>
clone the new root                zfs clone <template>@<snapshot> <jail dataset>
move every child back
start the jail                    only if it was running
```

**Nothing is destroyed.** The old root is renamed beside the children rather than destroyed, so a re-clone onto a wrong template costs one call to undo instead of a restore from backup. It is a clone, so it occupies only what the jail wrote since it was made — 352 KB on the host this was verified on, against a 372 MB template.

**Every child leaves before the root is touched.** Not an ordering preference: `zfs rename` carries a dataset's descendants with it, so parking the root first would move the children inside the parked copy, where nothing would look for them. Reversing the two steps was tried, and the next operation failed with `no dataset named .../probe6/data` — the children were simply gone from where everything expected them.

**Refusals happen before the jail is stopped.** A mistyped template name should cost an error message, not an outage. The template snapshot is checked, the jail's own dataset resolved, and the name validated, all before `service jail stop` runs.

**A failure stops, and reports.** No step is retried and nothing is undone. The response names the step that failed and lists every dataset that is no longer under the jail with where it now sits, so it can be restored with the call JD-025 already provides. Automatic recovery was rejected: an undo that fails leaves two interleaved failures in the audit log and a host in a state neither of them describes. The acceptance criterion — children in a known, reported place and never destroyed — is met by reporting, not by repair.

**The template snapshot is named in full, with no default.** Every jail on a real host is already a clone of `templates/<release>@base`, so defaulting to `@base` would re-clone a jail onto the snapshot it is already on: a no-op dressed as an upgrade.

**`confirm=` is the jail's name**, because this stops a running jail and replaces the filesystem its processes run on (JD-016).

### Rationale

The whole arrangement only pays off if a jail's state is in a child dataset, which is what JD-007 asks for and what the quota in JD-023 enforces. The measurement that makes it concrete, from the verification run:

```
jexec probe6 cat /data/marker          state-that-must-survive-the-upgrade
cat .../retained/probe6-root-base/root/marker   i-was-written-into-the-root
cat .../containers/probe6/root/marker   No such file or directory
```

The child survived. What had been written into the jail's **root** did not — it is a fresh template now, which is the point. That file is still readable in the parked copy, which is why the parked copy exists.

---

## JD-027 — Two Ways to Get a Jail, and What Builds the Templates Behind Them

**Status:** Locked — 2026-10-23

A jail is always a clone of a template (JD-007). There is one mechanism, and **two paths an operator takes through it**:

| | clone from | after it starts |
| --- | --- | --- |
| **a prepared jail** | a template with the packages already in it | it works |
| **a custom jail** | a release template — a bare FreeBSD userland | you fill it |

The second is the ordinary case today: `ottermq` and `goodiesdb` on the deployment target have no packages at all. They are empty jails that were configured after the fact, which is JD-007 stage 2 — "start the jail, and hand off to in-jail provisioning (`jexec` + `pkg`) for service-specific setup" — done by hand, and made callable by JD-016's exec endpoint.

The first is an optimisation of the second: do the provisioning **once**, snapshot it, and every jail cloned from that snapshot skips it.

### Two ways to build a prepared template

**The general one: clone a jail you configured** — the Handbook's own "Cloning a Jail as a Template" (17.4.3), made callable. Configure a jail however you like, by hand or through exec, stop it, and promote it:

```
POST /api/jails/ottermq/template  {"name": "ottermq-base"}

  zfs snapshot <jail>@base
  zfs clone    <jail>@base  templates/ottermq-base
  zfs promote  templates/ottermq-base
```

This captures whatever is in the jail, not only packages — `sysrc`, an edited configuration, an `initdb`, anything. It is how the deployment target's jails were actually built, and it is why the other path below is a shortcut rather than the answer.

**The `zfs promote` is the step that is easy to omit and expensive to omit.** Without it the template is a clone *of* the jail, so that jail can never be destroyed. Measured, before and after:

```
before promote:  containers/ottermq       <- templates/15.1-RELEASE@base
                 templates/ottermq-base   <- containers/ottermq@base
                 zfs destroy containers/ottermq
                   cannot destroy: filesystem has dependent clones -> templates/ottermq-base

after promote:   containers/ottermq       <- templates/ottermq-base@base
                 templates/ottermq-base   <- templates/15.1-RELEASE@base
                 zfs destroy containers/ottermq                     [exit 0]
```

After it the jail is a clone of the template instead, which is also the right way round: a template should outlive the jail it came from. The lineage then reads release template → this one → the jail, which is exactly JD-007's model, so the new template lists like any other with no special case anywhere.

**The jail must be stopped**, and Jail Deck refuses rather than stopping it. "Stop the jail before snapshotting so the image is consistent" is the Handbook's instruction; unlike a re-clone, nothing here *requires* the jail to be down — a snapshot of a running jail succeeds, it is just an image of a system caught mid-stride. Taking a service down is the operator's call, and it is one call they already have.

**Nothing is ever built on top of a template that already has a dataset and no `@base`.** Added 2026-10-23, when preparing a release template landed its extract stage.

A dataset holding half a userland looks exactly like one holding a whole userland — the filesystem cannot tell them apart, so JD-008's derived state machine cannot either, and both report `extracted`. Unpacking a second `base.txz` over the first would merge two trees into something nothing can describe afterwards.

So the rule that `Derive` already followed is now the rule for every path: a template with a dataset and no `@base` is a stage that stopped, it is a `409` naming itself, and the operator destroys it. That is also why a failed extract is left exactly where it stopped rather than cleaned up — the dataset is the evidence, and removing it is a decision with the operator's name on it (JD-026's stance, one domain over).

**A jail already carrying `@base` is refused.** `CreateSnapshot` is idempotent by design (JD-024), so an existing one would be silently reused — and the template would then capture whenever *that* was taken rather than the jail as it is now. The name is a template's convention; a jail wearing it is worth stopping for.

### The shortcut: packages without a jail

Jail Deck also derives one template from another, with packages, without booting anything:

```
PUT /api/templates/postgres-16  {"from": "15.1-RELEASE", "packages": ["postgresql16-server"]}

  zfs clone    templates/15.1-RELEASE@base templates/postgres-16
  pkg -c       /usr/local/jails/templates/postgres-16 install -y postgresql16-server
  zfs snapshot templates/postgres-16@base
```

**The precedent is JD-007's own stage 1**, which already has Jail Deck running `freebsd-update -b <root>` against a template that has never been started. `pkg -c` is the same shape of operation on the same kind of directory. This is not a new category of thing for Jail Deck to do.

**What it buys:** no jail, no `jail.conf.d` stanza, no networking for a temporary jail, nothing to clean up afterwards. For "a template with postgres in it", that is the whole job.

**What it cannot do, and this is the limit:** `pkg -c` installs packages. It cannot run `sysrc`, edit a configuration file, or run an `initdb`. Anything beyond installing packages belongs to the jail-based path — clone a template, create a jail on it, `exec` into it, stop it, snapshot the dataset. That path is strictly more capable, needs no new primitives beyond Jail Provisioning and exec, and is the general answer.

**An earlier draft of this record claimed the jail-based path could not work**, on the grounds that `jexec` cannot reach a template. It cannot — `jexec 15.1-RELEASE` answers `jail not found` — but that is irrelevant: the path starts a jail first, or clones one that already exists. The same draft argued the alternative would require exposing arbitrary command execution against a template root; JD-016 had already accepted exec into a running jail and called it "no more than the API already implies". Both arguments were wrong. `pkg -c` is kept as a shortcut on its own merits, not because anything else was impossible.

### What is stored: nothing

A template's whole state comes from ZFS (JD-011). `ready` is whether `@base` exists, which is what makes a template usable (JD-007). `parent` is read back from `zfs get origin`, so a derived template cannot disagree about where it came from.

**A failed install therefore needs no cleanup to be truthful.** The clone stays without its `@base` — a template that exists and is not ready, which is exactly what it is and exactly what the listing reports. A second attempt on it is refused rather than resumed: finishing it would install on top of whatever the failed attempt left, and from outside, a clean clone and a clone with half of postgres in it are indistinguishable.

### Package names

One rule: an argument beginning with a dash is refused, because `pkg` would read `-f` as an option and `install -y -f` is not the command that was asked for. No shell is involved either way — `CommandRunner` takes an argument array. Versions, origins and flavours pass through untouched. `pkg` running and refusing is a `400` carrying its own sentence, for the reason JD-023 gives.

### Consequence for the code's shape

Templates get their own bounded context, `internal/templates`, which resolves the "storage domain granularity" question `docs/SPEC.md` had open. A template is a dataset, but it is not *a dataset*: it has a readiness state and a lineage, and those are rules about templates. `internal/storage` implements the port `internal/templates` defines, as it does for `internal/jails` (JD-010).

### Rationale

The two paths are not competing mechanisms to choose between — they are the same mechanism at different times. Provision at jail creation, or provision once into a template and clone it. Jail Deck should make both reachable and should not pretend either is the only one.

`pkg -c` earns its place by being the part of that which needs nothing else to exist. Everything beyond packages waits for Jail Provisioning and exec, and when they land, the jail-based path covers what this cannot — including the case the deployment target actually has, which is jails with no packages and a configuration done by hand.

### Removing one

`DELETE /api/templates/{name}?confirm={name}` destroys the template and its snapshots, and **refuses while anything is cloned from it**, naming every one.

ZFS would refuse too — but only after being asked, and its message says "dependent clones" rather than which jails are running on them. Asking `zfs get clones` first turns a refusal into an answer. Never `zfs destroy -R`: that takes the dependent clones with it, which would make the check decorative.

A jail that should outlive its template is not stuck, and the refusal says so: `zfs promote` on the jail moves the dependency the other way, which is cloning a jail as a template run in reverse.

`confirm` is the template's name. Unlike a snapshot's, it is unambiguous on its own — a template name is unique within the pool, where `nightly` names a snapshot on every dataset that has one (JD-024).

This is also what cleans up the half-built template a failed derive leaves behind. Without it, refusing to resume one and offering no way to remove it would have left the operator reaching for `zfs destroy` by hand.

**Still to settle, in the Jail Provisioning milestone:** how a custom jail is filled through the API rather than by hand — exec (JD-016) is the mechanism, the orchestration is not designed — and how the paths are presented in the interface.


---

## JD-028 — Showing the Storage Model in the Web Interface

**Status:** Locked — 2026-10-23

Six releases' worth of storage endpoints — datasets, tags, snapshots, moves, re-clones, templates — had nothing in the interface. This is the reading half: every one of those things becomes visible before any of them becomes clickable.

### A page per jail, not an expanded row

`/jails/{name}` is a page of its own, reached by clicking the jail's name on the list. The alternatives were an expanding row and a side panel; both keep you on a list whose job is comparison, and a jail's datasets, tags and snapshots are three tables. They need the width.

A page also has an address. "Look at `/jails/ottermq`" is a thing one person can say to another, which an expanded row is not.

### One Templates page, templates and retained datasets together

`/templates` carries both. A retained dataset is not a template — it is a dataset that outlived its jail (JD-025) — but the two are the same kind of question: *what is on this host that is not a running jail?* Two pages, each with one small table, would be two clicks to answer it.

### The lineage is an indented table, not a tree widget

A template's parentage is a tree, and the obvious move is a tree control. It was not taken.

A host has a handful of templates, two or three deep. Indentation carries that. A tree widget brings expand state, collapse state, selection state and keyboard semantics, all of which would have to be right, and none of which answer a question the indentation does not. Sylve, which solves the same problems for the same kind of host, has tree views — for navigating *between nodes* and for exploring a whole pool. Jail Deck is single-host by decision (JD-003) and shows the jails' datasets only. Neither case applies here.

The row order is the lineage: parent before child, siblings by name. A template whose parent is not itself a template — cloned from somewhere outside `<pool>/jails/templates` — is appended at depth 0 rather than dropped, because a template that exists and does not appear in the list is worse than one that appears in the wrong place.

### What a clone is, is read from the host

A template's dependents arrive as dataset names. Sorting them into jails, derived templates and everything else by *reading those names* — looking for `/containers/` or `/templates/` — is the obvious implementation, and it was wrong the first time it ran: this project's own fake lays its jails out as `<pool>/jails/<name>`, with no `containers` level, and every jail landed under "other clones".

`<pool>/jails/containers` is the layout Jail Deck *creates* (JD-006, JD-021). It is not where the jails it *lists* have to be: those come from the host's own configuration, and a host that adopts Jail Deck arrives with jails already somewhere. Jail Deck stores no layout of its own (JD-011) and matches a jail to its datasets by mountpoint rather than by name precisely because of that (JD-022).

So a clone is a jail when a jail reports that dataset as its own, a template when a template does, and otherwise it is shown as the bare dataset it is. JD-021 already refused to identify the layout by dataset name — on the deployment host that would have reported a replica mounted nowhere as a provisioned system. This is the same mistake one layer up, and it reached the screen before it was caught: of everything on these pages, a name read out of a path was the only part that could be confidently wrong rather than merely unhelpful.

### Each panel fails on its own

A jail's page reads the jail and then its snapshots; a template's page reads the template and then the jails and templates it is matched against. The secondary reads are allowed to fail without taking the page down: a snapshot listing that errors costs the snapshot table, and a jail listing that errors costs the names and the links, not the page. The pages report what they could read, which is the same stance the API takes when a pool is unreadable (JD-022).

### Read before write

No destructive action is on these pages yet — no tag, no snapshot, no rollback, no retain, no destroy. Those arrive next, each with the confirmation its refusal already defines. Shipping the reading first means the confirmations can be built against pages that already show what is about to be changed.

---

## JD-029 — Acting on the Storage Model from the Interface

**Status:** Locked — 2026-10-23

JD-028 made the storage model visible. This is how it is changed: what gets a form, what gets a confirmation, and what the interface is allowed to decide on its own.

### A form where the thing is, a dialog only for what cannot be undone

Setting a tag, creating a child dataset and retaining one all open a form **in place** — under the section header, or in a row beneath the dataset. None of them destroys anything: a tag is a property, a new dataset is new, and a retain is `zfs rename -u` plus a remount with nothing removed (JD-025). A modal for each would be ceremony, and ceremony everywhere is ceremony nowhere.

The modal is kept for what a second call cannot take back. It replaces the browser's `confirm()`, which `docs/UI.md` had left open as a question and which could not have carried the rest of this section.

### A typed confirmation is the API's requirement, shown

`DELETE` and `rollback` will not act without `confirm=<dataset>@<snapshot>` spelled out in full (JD-024), and a template will not be removed without `confirm=<name>` (JD-027). The dialog asks for exactly that string and its button stays disabled until it matches.

This is deliberately **not** an invention of the interface. The same ceremony exists for anyone using `curl`, for the same reason — `nightly` names a snapshot on every dataset that has one, so a short name is not an answer to "which one". A client that made the typing optional would be offering a shortcut the API does not have.

### Every write is followed by a read

Each action refetches the jail when it finishes, whichever way it finished. The server reads ZFS rather than remembering what it was told (JD-011), so a refetch is the only answer that cannot disagree with the host — and a command that failed can still have changed something, which is exactly when a stale screen is dangerous.

That costs a request per action. The alternative is patching local state from the response and being wrong the first time two things change at once.

### The interface validates nothing the server validates

A property name without a colon, a dataset name with a slash, a `recordsize` of 7K: all of them go to the server and the server's own sentence comes back. `zfs` answers `recordsize=7K` with "must be power of 2 from 512B to 16M", which is better than anything this side would have written, and JD-012 already decided that sentence reaches the client.

The forms carry the rules as *hints* — "the name has to contain a colon", "one level only" — because an operator should know before typing. A hint is not a check, and when the two could disagree only one of them is talking to ZFS.

### What a refusal looks like

A 409 from these endpoints is not a sentence: the body carries the structure that explains it, which is the whole reason the API answers that way. The client keeps the decoded body on its error type and renders the explanation. Showing "Conflict" would throw away the part the API went out of its way to provide.

### A refusal is an answer, and it is shown where the row is

The snapshot endpoints refuse with `409` and a body that names what stood in the way — the newer snapshots, the clones, the holds (JD-024). Nothing ran, so it is not a failure; the client returns it as a result rather than throwing, and renders it in the warning colour under the row it is about, not the danger colour. Red is reserved for what did go wrong.

### A refused rollback is offered back, once

`destroy_newer=true` is the one refusal this API lets a client accept. The ladder is:

1. **Roll back** — the dialog explains what a rollback discards and asks for the typed name.
2. The server refuses, **naming the newer snapshots**. That list is the reason the first attempt is worth making: a client that computed the set itself would be second-guessing the transaction-group ordering that decides it.
3. **Destroy them and roll back** — a second dialog, carrying the list, with no typing. The name was already typed for this operation and nothing about the target changed; what is new is the list, and that is what the dialog shows.

Clones and holds get no such button, because nothing in the API accepts them. A hold exists to say "not this one", and taking a clone with its origin needs `zfs destroy -R`, which Jail Deck never passes.

### A recursive destroy names what it takes, first

Destroying a snapshot of the jail's **own** dataset is recursive: every snapshot of that name under the jail goes with it. The confirmation lists them before anything runs, computed from the snapshot list already on screen — the same set the server computes with `namesWithShort`.

A snapshot on a **child** dataset is destroyed through the child's own route and takes only itself. Which route a row uses is therefore decided by which dataset the row names, and the confirmation says what that means.

### A template snapshot is chosen, not typed

Re-cloning a jail's root takes the full `dataset@snapshot`, with no default — every jail is already a clone of some template's `@base`, so defaulting to one would be a no-op dressed as an upgrade (JD-026). But a string nobody can remember is not a choice either, so the form offers the **ready** templates and sends the snapshot each one names.

Only the ready ones: without its `@base` snapshot there is nothing to clone (JD-007). When none is ready the button is disabled and says why, which is the one place a hint becomes a check — there is no request that could succeed.

### A re-clone reports where it came from, not what it replaced

`Reclone` carries both: `to` is the template snapshot the new root was cloned from, `from` is what the root was a clone of before. An early version of this page printed `from` as "re-cloned from", which described the upgrade as the thing it replaced — on a jail whose template had just changed, it read as though nothing had happened.

A failure is rendered differently from every other error on these pages, because it is the only record of where the jail's datasets went: it names the step that stopped, lists each dataset and where it now sits, and says that a restore brings it back. Nothing is retried and nothing is undone (JD-026), so this message is the recovery procedure.

### Removing a template from its own page leaves no page

A successful removal on `/templates/{name}` navigates back to the list, because the thing the page was about is gone. A refusal stays, rendered against the dependents the page already lists — and if the two ever disagree, the server is the one that just read ZFS.

### What is not here

A jail's **own** dataset can be read but not written. Its native properties show beside it like any other dataset's, and there is no Properties button, because `PUT /api/jails/{name}/datasets/{child}` writes to a child and there is no route to the dataset the jail runs from. Offering the form anyway would be offering a request that cannot be made.

*(Until [#55](https://github.com/ottermq/jaildeck/issues/55) the properties were not reported at all, so no dataset could be edited from the page. That gap is closed; this narrower one is what is left of it.)*

---

## JD-030 — Creating and Destroying a Jail

**Status:** Locked — 2026-10-24

JD-005 decided that lifecycle goes through `service jail`. JD-007 decided that a jail is a clone of a template's `@base`. This is the order those steps run in, which package owns each one, and what happens when the middle of it fails.

Everything measured here was measured on isengard, 15.1-RELEASE-p5, on 2026-10-24.

### The jail is configured by a file of its own

`/etc/jail.conf.d/<name>.conf`, rendered with `text/template`. Jail Deck does not create or write `/etc/jail.conf`.

`rc.d/jail` reads a per-jail file directly as long as `/etc/jail.conf` carries no `.include` of the directory — and if it does carry one, it prefers the shared file instead, so that a jail is never defined twice. A tool that wrote the shared file would be rewriting something that belongs to the operator, on a host that may already have jails in it. One file per jail is also one file to delete.

### Autostart is membership of `jail_list`

`sysrc jail_list+=<name>` to enable it, `sysrc jail_list-=<name>` to disable it. Both are idempotent: adding a name twice and removing one that was never there each change nothing and exit 0.

The alternative is an `/etc/jail.conf` holding `.include "/etc/jail.conf.d/*.conf";` with `jail_list` left empty, which makes `service jail start` start everything in the directory. It is rejected because it removes the distinction: every jail that exists would boot, and "this jail exists but should not start" becomes inexpressible. Autostart is a property of a jail, and `jail_list` is where FreeBSD keeps it.

The cost accepted is that Jail Deck writes to `/etc/rc.conf`. It does so through `sysrc` rather than by editing the file, so the operator's comments and ordering survive.

### `service jail start` exits 0 whether or not the jail started

`rc.d/jail` prints `jail(8)`'s error and returns success anyway. Measured for a jail that failed to start, and for one that was already running — both print a message and both exit 0. `jail(8)` run directly exits 1; the exit status is lost in between.

So a jail is started and then **confirmed with `jls -j <name>`**. The exit code is not evidence, and neither is the absence of output.

This is the second tool in this project whose exit status does not answer the question being asked of it; `freebsd-update fetch install` was the first. Both were found by measuring rather than by reading, and the lesson is recorded here rather than in a comment: when a step's success matters, Jail Deck reads the state back.

### A failed start leaves the epair behind, and only Jail Deck will clean it up

When a start fails after `exec.prestart` has already run, `jail(8)` does **not** run `exec.poststop`. The epair survives, still a member of the bridge. This holds whether `exec.start` failed or `prestart` itself failed partway through.

Nothing else will take it away afterwards. `service jail stop <name>` does nothing and `jail -r <name>` answers `not found`, both for the same reason: the jail does not exist, so there is nothing to remove and the hook never runs. With a per-jail epair name the orphan then blocks every retry at the stanza's first line, `ifconfig epair<N> create` → `already exists`.

The resolution keeps both halves where they belong:

- **The stanza keeps `exec.prestart` and `exec.poststop`.** An operator who never runs Jail Deck gets a jail that starts and stops correctly, including its networking. That is the "integrate, do not replace" principle, and a generated file that only works when the generator is present would quietly break it.
- **Jail Deck adds the step only it can take.** After a start it could not confirm, it destroys the epair that stanza would have created. *Built 2026-10-24 and exercised end to end: a create whose `exec.start` failed on an unreachable gateway reported `failed_at: start` with the failing command in the message, and left no interface behind.*

The orphan is found by the description the stanza writes, `descr jail:<name>`, not by the interface name alone. A sweep that matched on name could take an interface belonging to something else on the host.

The cleanup is itself a command that can fail, and when it does the orphan is **named in the error** rather than swallowed. A leaked epair someone has been told about is a minute's work; a silent one is a jail that will never start again and no clue as to why.

### Destroy stops, unconfigures, and then lets ZFS have the last word

In order: stop the jail, remove it from `jail_list`, delete its configuration file, destroy the dataset.

Measured: `devfs` is unmounted when the jail stops, and `zfs destroy` then succeeds immediately with no busy error. Measured also: `zfs destroy` *while* the jail runs exits 1 with `pool or dataset is busy`.

That refusal is kept as the last line of defence rather than replaced by a check of Jail Deck's own. A check reads the state and then acts on what it read; the mount is held for as long as the jail is up. Only one of those can lose a race.

Destruction takes `DELETE /api/jails/{name}?confirm=<name>` — the same typed confirmation JD-024 asks for before a snapshot goes and JD-027 before a template does, for the same reason.

A jail whose dataset has snapshots or clones is **refused**, with the dependents named, as JD-024 already refuses. `zfs destroy -R` is never passed.

Two details settled while building it, 2026-10-24:

- **`-r` only when the jail has children.** A jail's child datasets go with it (JD-007), and that needs `-r`. A jail with none is destroyed without it, so that a snapshot taken between the check and the destroy makes ZFS refuse rather than being swept up silently. The check is not the guard; it is the explanation given before the guard runs.
- **A jail with no file of its own in `jail.conf.d` is refused.** Its block is in `/etc/jail.conf`, which belongs to the operator and which Jail Deck never writes — so Jail Deck also cannot take the jail out of it. Destroying the dataset anyway would leave the jail still declared, still listed, and pointing at a path that no longer exists. Refusing names the file instead.

### Orchestration lives in `jails`; the primitives stay in `storage`

The `jails` context owns the order of the steps, because the order is a rule about jails: confirm the template's `@base`, clone it, render the configuration, set autostart, start, confirm. Cloning a snapshot, creating a child dataset and destroying a root belong to `storage`, reached through ports that `jails` defines — the direction JD-010 set, and the one `storage.JailSource` already implements for the storage writes.

The alternative, a single `storage` method that creates a jail end to end, would put the knowledge that a jail is "clone, then configure, then start" inside the package whose subject is datasets.

### A half-created jail is reported, not rolled back

If the configuration write fails after the clone succeeded, the clone stays. The response names the step that stopped and what exists, and nothing is undone — the same position JD-026 takes for a re-clone, and for the same reason: an automatic rollback is a second destructive operation running at the moment least is known about the state.

The epair is the one exception, and it is an exception precisely because it is not recoverable state. Everything else a half-created jail leaves behind can be looked at, kept or removed deliberately. A leftover epair can only block the retry.

---

## Decision Policy

Once a decision is marked as **Locked**, it should not be revisited without a clear technical or product justification. Architectural evolution is expected, but changes should be deliberate and documented rather than occurring through incremental drift.

---

## JD-031 — Metrics: Where the Numbers Come From, and What Is Not Kept

**Status:** Locked — 2026-10-24. Measured on isengard, 15.1-RELEASE-p5.

`GET /api/metrics` answers the host and every jail in one request: load average, CPU, memory and pools for the host; CPU, memory and process count per jail.

**One request, because the dashboard is one page.** Per-jail usage costs **one `rctl -u` per jail** — measured: `rctl -u jail:` is refused with "Invalid argument", and there is no form that reports every subject at once. That cost is the reason usage is *not* folded into `GET /api/jails`: a listing is the page an operator opens to start a jail, and making it run a command per jail would make the cheap page pay for the expensive one.

### Per-jail: rctl where it is on, ps where it is not

The roadmap asked for a `ps` fallback where `kern.racct.enable` is off and left open whether one could be trusted. It can, and the measurement is the reason:

| jail | `rctl` memoryuse | sum of `ps -o rss` | processes |
| --- | --- | --- | --- |
| postgres | 290 263 040 | 290 476 032 | 13 / 13 |
| ottermq | 20 684 800 | 20 979 712 | 6 / 6 |
| goodiesdb | 12 197 888 | 12 263 424 | 4 / 4 |

Within 1%, and the process counts identical. They are not two opinions about memory that happen to be close: racct's `memoryuse` **is** the sum of the same per-process resident sizes, so the two differ only by the moment each was sampled. A fallback that reads the same quantity another way is worth having; one that reads a different quantity and calls it the same name would not be.

**The response says which source it used**, because the two are not equally complete. `rctl` additionally gives `vmemoryuse`, `openfiles`, `cputime`, swap and the I/O counters; `ps` gives CPU, memory and a process count and nothing else. A consumer that needs the rest can read `source` and know why it is absent rather than guess.

**`rctl -u` answers for a name no jail has, with every field zero and exit 0.** So the jail listing is what says which jails exist, and `rctl` is only ever asked about a name that came from it. A metrics endpoint that trusted `rctl` alone would report a typo as a jail with no usage.

### Host CPU is a rate, and a rate needs two readings

`kern.cp_time` is five cumulative tick counters. There is no kernel-maintained host CPU percentage — `vm.loadavg` is load, not utilisation — so a percentage exists only between two readings.

Three ways to get one, and the costs:

| | |
| --- | --- |
| Sleep inside the request | A second of latency on every poll, to measure a second that has already passed |
| Background sampler with a retention window | The in-memory database JD-011 refuses, and a restart silently changes what the numbers mean |
| **Keep the previous reading and report the delta** | One sample's worth of memory, and the first call after a restart has no percentage to report |

The third. The response carries the raw counters **and** `cpu.over_seconds` — the window the percentage was actually measured over — so a reading taken five seconds after the last one and one taken five minutes after are told apart by the consumer rather than averaged into the same word. On the first call after a restart the percentage is absent, not zero.

This is the same position JD-008 took for a template's `running`: one thing cannot be derived from a single look at the system, it is held in memory, and losing it on a restart is the truthful outcome rather than a limitation.

**Per-jail CPU needs none of this.** racct maintains `pcpu` as a decaying average per subject, so it is a number the kernel already has — which is why the host reports a percentage only after two polls while a jail reports one immediately. The asymmetry is the kernel's, not an inconsistency to be smoothed over.

### What is reported, and what is left out

- **Memory** is the page counters — free, active, inactive, wired — times the page size, against `hw.physmem`. Reported as a breakdown rather than one "used" figure, because **wired is mostly ZFS ARC on a host like this** and a single number would read as pressure that is not there. `v_cache_count` exists and is always 0 on this release; it is not reported.
- **Pools** come from `zpool list -Hp` in exact bytes, as everything else in this API does (JD-022).
- **No disk I/O for the host**, and no network counters. Neither is in the stated scope, and `/metrics` for Prometheus is where a consumer who wants the long tail should be served.
- **Nothing is stored.** There is no history, no retention and no sampler goroutine: a chart over time is Prometheus's job, which is what the `/metrics` endpoint is for (JD-011).

### Rationale

The whole of this is derived state read on request, which is what JD-011 asks for, with exactly one exception argued above. The alternative — a sampler keeping a window of readings — would make Jail Deck own durable-ish state about something that can be re-read at any moment, and would answer "what was the CPU five minutes ago?" badly where Prometheus answers it well.

---

## JD-032 — Disk Health: What Is Reported, and What Is Not Judged

**Status:** Locked — 2026-10-25. Measured on isengard and rohan, 15.1-RELEASE-p5 and -p2.

`GET /api/metrics` reports every disk `smartctl --scan` finds, with the drive's own verdict, the attributes smartctl flags, and which pool the disk belongs to.

### Why this exists at all

ZFS reports **pool** health. An operator needs **disk** health, and on the night this was decided the two disagreed for five days: `zpool status` said `backup ONLINE, no known data errors` while one of that mirror's two members had been reporting a failed SMART attribute since Oct 20, to a console nobody reads. The dashboard shipped hours earlier said `ONLINE` too, and was right to. A pool is healthy until a disk actually fails; the question "is a disk about to fail?" is a different question and nothing in Jail Deck could answer it.

### No health score. None.

`isengard`'s `ada3` is, simultaneously and truthfully:

- `"passed": true` on the drive's own overall assessment
- `"when_failed": "now"` on attribute 184, End-to-End_Error, value 91 against a threshold of 99
- zero reallocated sectors, zero pending, zero offline uncorrectable
- passing an extended self-test that completed without error at 19,149 hours
- carrying 1641 UDMA CRC errors, which is a cable fault rather than a platter one
- having once reached 71°C

No single number describes that drive. A grade, a percentage or a traffic light would be wrong in one direction or the other, and an operator who trusted it would either ignore a real warning or replace a working disk. So Jail Deck reports what smartctl says and does not add an opinion — the same position JD-012 takes about validating what the host already validates, and JD-023 about re-implementing `zfs`'s rules.

**`when_failed` is smartctl's own judgment** — `"now"`, `"past"` or empty — and it is reported as given rather than re-derived from value against threshold. Deriving it would be a second opinion that can disagree with the first.

**Raw values are reported as the strings smartctl prints, never as numbers to reason about.** Seagate's `Raw_Read_Error_Rate` raw value on `ada3` is `200021688`, which is not a count of anything: vendors encode several fields into that integer. A tool that showed it as "200 million read errors" would be lying, and one that did arithmetic on it would be lying twice.

### The scan list is not a list of disks

Measured on isengard, where `smartctl --scan` returns eight entries for five devices:

| entry | exit | what it is |
| --- | --- | --- |
| `ada0`–`ada3` `-d atacam` | `0`, and `32` for `ada3` | the four disks |
| `nvme0` `-d nvme` | `0` | the NVMe |
| `nda0` `-d scsi` | **`2`** | the **same NVMe** under another driver, and it cannot be opened that way |
| `cd0` `-d atacam` | **`4`** | a DVD drive |
| `ses0` `-d atacam` | **`2`** | an enclosure |

So the duplicate resolves itself, and the rule is: **the device that answers is the device to report.** An entry whose output carries no `smart_status` is not a disk Jail Deck has anything to say about, and is left out.

### Amended 2026-10-25, while writing the test

The rule above decides *which* devices to report. It does not decide what to **call** them, and that turned out to be the harder half.

`zroot` stands on `nda0`. `smartctl` refuses `/dev/nda0` outright — *"NVMe controller controller/namespace ids must begin with `/dev/nvme`"*, including under `-d nvme`, which was measured rather than assumed — and answers only as `/dev/nvme0`. So the single disk holding the root pool is the single disk that nothing joins to its pool by name. Reporting it as `nvme0` would also be reporting it under a name that `zpool status`, `gpart` and the operator never use.

Guessing that `nvmeN` is `ndaN` would be a convention, not a fact, and JD-032 does not trade in conventions. `geom disk list` supplies the fact: base system, no privilege, and it prints the kernel's name beside the drive's serial — the same serial `smartctl` prints.

**The join is by name first, by serial second**, and each host proves one half:

- **by serial** for `isengard`'s NVMe, where nothing matches by name;
- **by name** for `rohan`'s `da0`, a bare drive in a USB enclosure where the two sources report *different* serials: `geom` gives the enclosure's and `smartctl` the drive's own. A serial-first rule would fail to place a disk whose name already matched.

Neither host has a disk whose smartctl serial is another disk's `geom` ident, so nothing captured says which lookup should win when they point at different disks. The order is a judgment — a name is identity, a matching string is evidence — and the test that fixes it says it is constructed.

Where the two names differ, `smart_path` carries the one smartctl needs, because an operator told "`nda0` is failing" who reaches for `smartctl -a /dev/nda0` gets a message about namespace ids instead of an answer.

This also corrects a claim made above before the code existed: that deduplicating by serial "would wrongly collapse two identical drives from the same batch". It would not — two drives from one batch share a model and never a serial. The serial is exactly the right key, and it is now what collapses the duplicate when both drivers answer. The exit status alone was enough only because, on this host, one of the two does not.

**Exit status is information, not failure.** `ada3` exits `32` — bit 5, "an attribute has been below threshold in the past" — on a completely successful read. An adapter that treated non-zero as an error would report nothing about the one disk that matters. The same dialect translation the adapter already makes for `rctl -r`, `sysctl`'s unknown oid, `ps`'s invalid jail id and `zpool list` on a host without ZFS.

### Disks with no pool are reported

A spare, a scratch disk, a disk being wiped: each can fail, and an operator watching disks wants all of them. `pool` is simply empty for those. The alternative — reporting only what the pools stand on — would answer a narrower question than the one being asked.

### The part that is Jail Deck's

Neither tool answers the operator's real question alone. `smartctl` cannot say which pool a failing disk holds up; `zpool status` cannot say the disk is failing. Joining them is the whole feature:

```
gpt/backup1  ->  ada3p1  ->  ada3  ->  184 End-to-End_Error, failing now
```

That took three commands and a `glabel status` to work out by hand. It is one field in the response.

A pool names its members however it was created — `isengard`'s `backup` says `gpt/backup1` and its `tank` says `ada0p4` — so resolving a GEOM label and stripping a partition are both ordinary paths rather than one being a special case. Anything that resolves to no device at all (`mirror-0`, `indirect-1`, a file-backed vdev) is ignored without the join having to enumerate what a vdev type can be called.

**Model and serial are reported**, because the operator's next question after "which disk is failing" is "which one do I unplug", and on a four-disk box that otherwise means powering down to read labels.

### Read-only, always

Jail Deck never starts a self-test. `smartctl -t long` takes hours and costs real I/O on a live host, and choosing when to pay that is an operator's decision, not an API's. The **self-test log is read**, because "the drive scanned itself three days ago and found nothing" was the single most reassuring fact on the night this was written, and it comes free in the same call.

### smartmontools is a package, not base

`sysutils/smartmontools`, at `/usr/local/sbin/smartctl`. A host without it reports no disks and says why, rather than failing the request — the position JD-021 takes for an unreadable pool and JD-011 takes generally. The rest of the metrics report is unaffected.

### NVMe is a different drive, and is reported as one

There is no attribute table. `nvme_smart_health_information_log` carries `critical_warning`, `percentage_used`, `available_spare` and `media_errors`, and a non-zero `critical_warning` is the equivalent of a failed attribute. Reporting an NVMe as though it had ATA attributes would be inventing data; reporting it with its own fields and none of theirs is the honest shape.

### Rationale

The whole of this is read on request and stored nowhere, like every other metric (JD-011, JD-031). Alerting is deliberately absent: Jail Deck reports, and something that already knows how to wake people decides to. The Prometheus endpoint is the path for that.

---

## JD-033 — The Prometheus Endpoint: Auth, Units and Names

**Status:** Locked — 2026-10-25. Measured on isengard, 15.1-RELEASE-p5, against Prometheus 3.15.1.

`GET /metrics` serves the same report as `GET /api/metrics` in Prometheus exposition format, behind the same authentication as every other endpoint. It answers the open question carried since 2026-10-21: **it shares the API's auth and does not bind a listener of its own.**

### Why not an unauthenticated loopback port

This was the obvious answer — it is what `node_exporter` does, and binding `127.0.0.1` feels like a boundary. On FreeBSD with jails, it is not one. Measured on isengard, where Jail Deck listens on `127.0.0.1:8888`:

| jail | `ip4` | reaching the host's `127.0.0.1:8888` |
| --- | --- | --- |
| `postgres` | `disable`, with its own address | connection refused |
| `ottermq` | `inherit`, no address of its own | **served Jail Deck's page** |

A jail configured with `ip4=inherit` — which is what a jail without an address gets, and two of the three jails on that host — shares the host's network stack *including its loopback*. An unauthenticated metrics port would publish the host's memory, every jail's resource usage and every disk's serial number to any jail on the box, including ones Jail Deck itself created.

So the loopback is not a boundary here, and "it is only on localhost" is not an argument on this platform. The endpoint is authenticated.

### Why not a Unix socket only

Jail Deck already has a socket listener (JD-002), mode 0660 with an optional group, and a jail cannot reach a host socket outside its root. File permissions are the more FreeBSD-shaped answer and were seriously considered.

The blocker is the scraper. Prometheus only learned to scrape a Unix socket in **v3.15.0, released 2026-09-25** — one month before this was written ([#12024](https://github.com/prometheus/prometheus/issues/12024), [#18091](https://github.com/prometheus/prometheus/pull/18091)). A socket-only endpoint is unscrapeable by any older Prometheus without `socat` or nginx in front of it, which is a bridge the operator has to run and keep running.

So the socket is not the *only* path — but it costs nothing to keep. The socket serves the same mux, so `/metrics` is already reachable over it, and an operator on 3.15.0 or newer can scrape it. Both work; neither is special-cased.

**Corrected 2026-10-25, by scraping it.** This record first said the socket could be scraped *without* a token. It cannot, and the mistake was reading JD-002 carelessly: the socket's `0660` group is a first access check and explicitly **not the only one**, because `RequireAuth` runs on that listener exactly as on TCP. Measured: `curl --unix-socket … /metrics` with no token is `401`, and `200` with one. The scrape config needs `credentials_file` either way, which makes the two paths more alike rather than less.

Two more things only a real scrape could say:

- The socket path goes in the `__unix_socket__` **label**, not in `targets` — `promtool check config` rejects a path there with *“is not a valid hostname”*. The address stays an ordinary `host:port` that nothing dials.
- **FreeBSD packages two Prometheus versions under different names.** `pkg install prometheus` gets **2.55.1**, which predates socket scraping entirely; **3.15.0 is the `prometheus3` package.** An operator who installs the obvious one cannot scrape a socket at all, which is the strongest form of the argument above for not making it the only path.

### The token is the same token

`jaildeck token add prometheus` produces a bearer token, and Prometheus scrapes with `authorization.credentials_file`. No second credential system, no second store, no anonymous mode behind a setting whose wrong value is the leak measured above. A scraper is an automation client, and JD-003 already decided what automation clients present.

```yaml
scrape_configs:
  - job_name: jaildeck
    static_configs:
      - targets: ['isengard:8888']
    authorization:
      credentials_file: /usr/local/etc/jaildeck.token
```

### `kern.cp_time` is in `stathz`, not `hz`

Measured, because the obvious reading is wrong. `kern.clockrate` on isengard:

```
{ hz = 1000, tick = 1000, profhz = 8128, stathz = 127 }
```

Over two seconds the five `kern.cp_time` counters advanced 3995 ticks across 16 CPUs — 125 per second per CPU, which is `stathz`, not `hz`. An implementation that divided by `hz` would report CPU seconds **eight times too small**, and nothing about the number would look wrong.

So `/metrics` exposes `jaildeck_host_cpu_seconds_total` converted with `stathz`, and `kern.clockrate` is read for it. Where `stathz` is zero — permitted, and it means the statistics clock is the main one — `hz` is used instead.

This exposed a real gap in JD-031's JSON as well: it hands out `cpu.ticks` and never says what a tick is, leaving every consumer to make the same wrong guess. `cpu.ticks_per_second` is now part of the response.

### Units and names

Prometheus conventions win over matching the JSON field names, because the audience is different: a scraper's user writes `rate()` and expects base units.

- **bytes**, not the API's already-exact byte counts renamed — those match already
- **seconds** for anything the drive or kernel reports in hours or ticks. Both conversions are exact (`×3600`, `÷stathz`), so nothing is invented
- **ratios** 0–1 rather than percentages, for pool capacity and NVMe spare
- `_total` on counters, and only on counters
- `_info` metrics carrying identity as labels with a value of `1` — the disk's model and serial, the build version. Labels, not metric names, because they change and metric names should not

**A jail's CPU is `jaildeck_jail_cpu_cores`**, not `pcpu`. The API reports percent of *one* core, so a jail on two whole cores reads 200, which JD-031 already found to be the confusing unit next to anything else. `cores` is the natural unit and the dashboard already says it. Where `rctl` is the source there is also `jaildeck_jail_cpu_seconds_total`, a genuine cumulative counter — a different quantity, not the same one renamed, and absent where `ps` is the source rather than zero.

### What is deliberately not here

**No alerting, no thresholds, no `up`-style verdict on a disk.** Jail Deck reports; Prometheus and Alertmanager already know how to wake someone, and this endpoint exists so that they can. A `jaildeck_disk_failing_attributes` of `1` is the fact; deciding it is worth a phone call at 3am is the operator's, expressed in a rule file that Jail Deck never sees. The same position JD-032 takes against a health score, for the same reason.

**No history, no sampler.** Unchanged from JD-011 and JD-031: the scraper holds the history, which is the entire argument for having this endpoint rather than a database.

### Settled against a real Prometheus

Verified on isengard, 2026-10-25, against `prometheus3-3.15.0` scraping a build of this branch running beside the live service:

- `promtool check metrics` on the live body: **exit 0**, no complaints. That is Prometheus's own parser *and* its naming linter, not a parser of ours.
- Both scrape jobs — TCP with a bearer token, and the Unix socket — report `health: "up"` with an empty `lastError`. 218 `jaildeck_*` series landed.
- `jaildeck_disk_failing_attributes > 0` selects exactly `ada3`, the drive this host has been complaining about since 20 October, while `jaildeck_disk_passed{device="ada3"}` is `1`. The disagreement JD-032 exists to preserve is queryable, which was the point.
- The `stathz` conversion checks out against physical reality rather than against itself: idle divided by `stathz` is 6,192,862 CPU-seconds against 6,203,520 of capacity for a 16-CPU host up 4 days 11:42 — 99.8% idle, which is what this host is. Dividing by `hz` would have claimed it was 87% busy for four days.

---

## JD-034 — Firewall Visibility: What Is Read, and What Is Not Trusted

**Status:** Locked — 2026-10-25. Measured on the VM, 15.1-RELEASE-p3, with a test ruleset loaded and removed again (`capture-pf-induced.sh`); checked against isengard and rohan, 15.1-RELEASE-p5 and -p2.

Phase 1 of JD-020, built: a `firewall` domain whose port has **no method that writes**. `GET /api/firewall` answers PF's state, its counters, the loaded rules with their counters, the NAT rules, every anchor with its own rules, and the tables. `GET /api/firewall/tables/{name}` answers one table's addresses. `GET /api/firewall/log` answers the most recent pflog lines.

### Three states, and none of them is an error

| state | what `pfctl` says | what the API says |
| --- | --- | --- |
| **not loaded** | `/dev/pf: No such file or directory`, exit 1, on every command | `"state": "not_loaded"`, `200`, nothing else to show |
| **disabled** | `Status: Disabled for …`, rules still listed, counters zero | `"state": "disabled"`, rules shown, and the interface says plainly that they are not being applied |
| **enabled** | `Status: Enabled for …` | `"state": "enabled"` |

**Not loaded is the common case.** Neither isengard nor rohan has PF loaded. On the hosts this project actually runs on, the view's first job is saying so without looking broken.

**pfctl's duration is not what it looks like.** `Status: Disabled for 0 days 00:21:19` was measured on a PF loaded 41 seconds earlier and never enabled — 21 minutes is the VM's uptime. The clock restarts at every `pfctl -e` and `pfctl -d` (measured: 3 seconds after an enable, 2 after the following disable), so it means "since PF was last switched on or off, **or since boot if it never has been**". It is reported as `for_seconds` with that meaning stated, and the interface says it in those words rather than "disabled for 21 minutes".

**Disabled with a ruleset is the case most worth showing.** A policy is loaded and doing nothing — the state of an operator who loaded `pf.conf` and forgot `pf_enable`. The rules look exactly as they would if they were enforced, so the state is reported first and the interface does not let it be missed.

### What JD-020's command list missed

- **`-s rules` does not show NAT.** Measured: a loaded `nat` rule appears under `-s nat` and nowhere in `-s rules`. A view built on JD-020's list would hide every translation rule, so `-s nat` is read too.
- **An anchor's rules are not in the main listing.** `-s rules` shows `anchor "jaildeck/*" all` and stops. `-s Anchors` lists every anchor flat — wildcard parents and inline anchors included — and each is read with `-a <anchor> -s rules` and `-a <anchor> -s nat`. One call per anchor per kind; anchors are few.

### What is not trusted

- **`-s Tables -vv`'s statistics.** On 15.1 it reports `Addresses: 1` for a table holding three entries and `References: Rules: 0` for a table a rule uses, while `-s rules -vv` reads `<table:3>` for the same table. Its flags (`p` persist, `a` active, `r` referenced and the rest, from `-s Tables -v`) agree with everything else and are reported. **Its counts are not**, and nothing is shown in their place: a table's addresses are read with `-T show` when that table is opened, which is also where a count is honest. The summary does not read every table's contents to count them, because a blocklist table can hold a hundred thousand entries.
- **The exit status, alone.** A nonexistent anchor is **exit 0** with `pfctl: Anchor does not exist.` on stderr; a nonexistent table is **exit 255**. Anchors are only ever read by names `-s Anchors` just returned, and the stderr message is checked anyway.
- **stderr, alone.** Every `-v` call prints `No ALTQ support in kernel` to stderr with exit 0. A reader that treats stderr as failure would fail on every host without ALTQ. That one notice is expected and ignored; anything else on stderr with exit 0 is reported, not swallowed.

### A rule is reported as `pfctl` prints it

The rule text is pfctl's own rendering — normalised by pfctl (`port = ssh`, `flags S/SA keep state` added where it applies), and **not re-parsed**. Re-implementing PF's grammar to restate a rule would be a second opinion that can disagree with the first: JD-032's position on SMART, JD-023's on `zfs`. Only what has to be a number is parsed — the counters in the brackets, the rule number `@N` from `-vv`, and the `Last Active Time` where there is one. The action (`pass`, `block`, `match`, `anchor`, `nat` …) is the first word and nothing more clever, for the interface to colour by.

`-vv` rather than `-v`, because pflog refers to rules by that number and nothing else prints it.

### pflog: the honest version of "recent"

Read with `tcpdump -n -e -tttt -r <file>`, where the file is `pflog_logfile` from `rc.conf` (default `/var/log/pflog`). Four things the response says rather than leaves to be discovered:

- **It lags.** `pflogd` buffers and flushes every 60 seconds by default, or on `SIGALRM`. Measured: the file was empty straight after the logged packets and had them after a flush. **Jail Deck does not send that signal.** It would make the view fresher, and it is still a signal to a daemon Jail Deck does not own; phase 1 changes nothing on the host. The response says the lines may be up to the flush delay behind.
- **`log` records the packet that creates a state**, not the traffic. Six packets matched a logged rule and one line was logged. A rule whose counters say thousands with one log line is not a contradiction.
- **An anchor is named by its last component.** A rule inside `jaildeck/web` logs as `rule 5.web.1/0`. Two anchors called `a/web` and `b/web` cannot be told apart from the log, so the line is reported with what it says, and the main rule number (`5`, which *is* unambiguous) is how the interface finds the anchor.
- **`-tttt`, not `-ttt`.** `-ttt` is the time since the previous packet, so the first line of every read is `00:00:00`. Found in the first capture.

**The tail is read honestly or not at all.** `/var/log/pflog` is rotated at 1000 KB by `/etc/newsyslog.conf.d/pf.conf` on all three hosts, so tcpdump's text is about the same size — larger than the 1 MiB the runner keeps by default, and **the runner keeps the beginning**: the oldest lines. So the log is read with a larger cap sized to the rotation, the last lines are kept in Go, and if the output is still cut short the response says the log could not be read to its end **and shows no lines**. Showing the oldest lines under the word "recent" would be worse than showing none.

### Read-only, by construction

The port has no method that could write, and the adapter is tested for it: every command any firewall test causes the adapter to run is checked for `-f`, `-F`, `-e`, `-d`, `-k`, `-K`, and `-T` with anything but `show`. A write path is phase 2 (JD-020), and it will need a new port method, a decision record and a reason — not a flag added to an existing call.

---

## JD-035 — Listing Virtual Machines: `vm list`, Read From Both Ends

**Status:** Locked — 2026-10-26. Measured on isengard, 15.1-RELEASE-p5, vm-bhyve 1.7.5, with two throwaway guests made and destroyed again (`capture-vm-guests-induced.sh`); every state string read in vm-bhyve's source.

The first unit of JD-019's `vms` domain, built read-only: `GET /api/vms` lists every guest vm-bhyve knows with its state, and `GET /api/vms/{name}` adds what `vm info` says about one. The port has **no method that starts, stops or changes a guest**. Lifecycle is the next unit, with its own methods and the detaching JD-019 requires.

### Where it comes from

**`vm list`, for the listing.** It is the command Host Setup's readiness check already runs: vm-bhyve checks `vm_enable`, `vm_dir` and the datastore before listing anything, and names what is wrong. So the listing has three answers and **none of them is an error** — the firewall's position on PF not loaded (JD-034):

| `vm list` | `vm_bhyve` | |
| --- | --- | --- |
| `vm` not found | `not_installed` | `200`, an empty list |
| exit 1 with vm-bhyve's `ERROR:` line | `not_configured` | `200`, an empty list, and vm-bhyve's words in `problem` |
| the table | `ready` | `200`, the guests |

**`vm info <name>`, for one guest**: the UUID, resident memory, consoles, network interfaces and disks. It is not used for the listing. It runs `zfs`, `netstat` and `bhyvectl` per guest, it **does not say whether a guest starts at boot**, and with no guests it exits 1 with no output at all — measured.

The detail is the listing's row joined with `vm info`'s additions. The name must be one the listing has, which makes a 404 Jail Deck's decision rather than a parse of vm-bhyve's `unable to locate virtual machine` (still recognised, for a guest destroyed between the two reads). A name vm-bhyve could never have created — its own `util::check_name` rule — is a `400` before anything runs.

### The table is read from both ends

`vm list` pipes its rows through `column -t`, which pads to the widest value. Two of its columns hold spaces — `AUTO` (`Yes [1]`) and `STATE` (`Running (16783)`) — so splitting on whitespace is wrong, and measured column offsets move: marking one guest to start at boot widened `AUTO` and moved `STATE` three characters right.

Both of those are the **last two** columns, and the name, first, never holds a space. So a row is matched from both ends: the name, then `AUTO` and `STATE` in every form vm-bhyve can print, and the five columns between, none of which holds a space.

**FreeBSD's `column -t` merges an empty field into the next.** Measured: an empty `MEMORY` puts `AUTO`'s value under the `MEMORY` heading. Of the five middle columns only `CPU` and `MEMORY` can be empty — `LOADER` defaults to `none`, `VNC` to `-` — so a row with four or three of them has its datastore, loader and VNC read and **`cpu` and `memory` left unknown** rather than guessed between. A heading other than the eight measured (`-v` adds three columns) is refused, not read by position.

### Every state, from the source

vm-bhyve's states were read in `lib/vm-core` and `lib/vm-run`, not inferred from two captures:

| `vm list` | `state` | |
| --- | --- | --- |
| `Running (<pid>)` | `running` | a bhyve process for the guest; `pid` is bhyve's — measured: not `vm-run`'s, its parent |
| `Bootloader (<pid>)` | `bootloader` | `bhyveload` or `grub-bhyve` loading a kernel, before bhyve runs |
| `Stopped` | `stopped` | |
| `Suspended` | `suspended` | stopped with its state saved to disk |
| `Locked (<host>)` | `locked` | marked running by another host, in a datastore shared between hosts; `locked_by` names it |

Anything else is refused as an unrecognised row. vm-bhyve decides "running" by **`pgrep` for a `bhyve: <name>` process**, so `/dev/vmm` is not read separately here: a bhyve instance vm-bhyve does not know about is not a guest this listing can name, and JD-019 already settled that vm-bhyve owns every bhyve on a host that uses it.

### What is not re-parsed

`cpu` and `memory` are the guest configuration's own values as vm-bhyve prints them (`"2"`, `"512M"`). They are what the operator wrote, and a unit Jail Deck derived could disagree with it — JD-034's position on PF's rule text. Where `vm info` gives bytes it gives them twice, `41967616 (40.023M)`: the integer is kept and the rounding is not.

`autostart` is whether the guest is in `rc.conf`'s `vm_list`, from the listing's `AUTO`, and `autostart_order` its place there. Sections of `vm info` not read — snapshots, clone origin — and any key vm-bhyve adds are passed over.

### Read on every request

Nothing is stored (JD-011). Each `GET` runs `vm list`, and the detail runs `vm info` once more. `vm list` took 0.06 s on isengard with no guests.

### Read-only, by construction

The port has two methods, both reads, and the adapter is tested for it: every command the listing and the detail cause it to run is checked to be `vm list` or `vm info`. Starting a guest needs a new method, and JD-019's constraint with it: `vm start` does not detach.

---

## JD-036 — Starting and Stopping a Guest: Detached, Confirmed, and a Stop With a Deadline

**Status:** Locked — 2026-10-26. Measured on isengard, 15.1-RELEASE-p5, vm-bhyve 1.7.5, with a throwaway guest started, stopped and powered off (`capture-vm-lifecycle-induced.sh`), then through the API with the branch binary; vm-bhyve's `start`, `stop` and `poweroff` read in its source.

`POST /api/vms/{name}/start`, `/stop` and `/restart`, audited as `vm-start`, `vm-stop` and `vm-poweroff`. They are the first VM endpoints that change the host. Creating, changing and destroying a guest are still to come.

### `vm start` runs in a session of its own

JD-019 required `vm start` to be detached from Jail Deck. The source says why, and the capture confirms it. `vm start` launches vm-run, the supervisor that keeps bhyve running, as `$0 _run <name> >/dev/null 2>&1 &`, without job control. So vm-run and bhyve stay in **the caller's process group and session**: measured, the capture script's group was theirs. Jail Deck's runner kills a command's whole process group on a timeout (that's how a hung `service` script and its children are cleaned up), so a slow `vm start` started the usual way would power its own guest off.

The runner gained `Detach`. The command gets a new session (`setsid`), and a timeout signals that command only, never its group. That is JD-019's "or `setsid`". It was chosen over `daemon -f vm start` because `daemon` would throw away `vm start`'s output and exit status, and those are where its refusals are ("guest appears to be running already", "incomplete virtual machine configuration"). vm-run's output goes to `/dev/null`, so it never holds the runner's pipes open: measured, the pipe closed **1 second** after `vm start` began.

Measured through the API: vm-run and bhyve were in a session other than Jail Deck's, and **stopping Jail Deck left the guest running**, with the same bhyve pid.

### Nothing is believed from an exit status

`vm start` returns once vm-run is in the background, before bhyve is up. Measured: `Stopped` on the first look, then `Running (pid)` a second later. `vm stop` returns once it has sent its signals, and **exits 0 even for a guest that isn't running** (with a WARNING). So every step is confirmed in `vm list`, once a second, the way a jail's start is confirmed in `jls` (JD-030):

| step | waits for | up to |
| --- | --- | --- |
| start | `running` or `bootloader` | 15 s — measured: 1 s |
| stop | `stopped` or `suspended` | **60 s** — the guest OS's shutdown, not vm-bhyve's |
| poweroff | `stopped` or `suspended` | 15 s — measured: under 1 s |

A start that `vm start` accepted but that never shows as running is a failure. The response points at `vm-bhyve.log` in the guest's directory, where vm-run writes what bhyve said.

### A stop has a deadline, and says when it was used

`vm stop` is the ACPI power button: SIGTERM to bhyve, then again a second later. A guest that doesn't answer within 60 seconds gets **`vm poweroff -f`**, the equivalent of pulling the plug, and the answer says **`forced: true`**. The audit log keeps both steps: the `vm-stop` entry is recorded as failed ("did not answer the ACPI shutdown within 1m0s"), followed by the `vm-poweroff` entry. A guest that stops between the deadline and the poweroff is still a stop: vm-bhyve then refuses the poweroff ("doesn't appear to be a running virtual machine"), and that refusal is logged too.

**Forced poweroff is automatic**, not something the caller has to ask for. JD-019 decided a stop has a deadline. Without one, `POST /stop` on a guest that ignores the button would answer "stopped" about a guest that isn't, or hang. Reporting `forced` is what keeps the shortcut honest.

**The power button was not ignored here.** JD-019 measured a Debian guest ignoring it before its OS was up. A guest with no OS at all, sitting in UEFI, powered off within the second `vm stop` itself spends, every time it was asked: three stops, one of them in the guest's first second. So the poweroff fallback hasn't happened on a real guest yet. It is tested against a scripted vm-bhyve, including the second either side of the deadline. Seeing it for real needs a guest with an OS, which arrives with image templates.

*Measured 2026-10-26 with one (JD-037's capture): a Debian 13 guest stopped 3 seconds into its boot, still in the firmware or GRUB, went off at once. Stopped at 10 seconds, and again at 60, it **ignored the button for the whole 70 seconds** that were watched, and `vm poweroff -f` then stopped it. That guest had no network interface, so systemd waited for one and the boot took 141 seconds; a guest whose boot is that slow is the one the deadline exists for. One more state turned up: for about a second while the guest powered itself off, `vm list` showed **`Locked (isengard)`**, the host's own name, with bhyve gone and vm-run still exiting. A start in that second is refused as `locked`.*

### Idempotent, and refused only where vm-bhyve can't be trusted

- **Start** of a `running` or `bootloader` guest, and **stop** of a `stopped` or `suspended` one, run nothing and answer the guest as it is, as a jail's start does. Nothing ran, so nothing is audited. **Starting a `suspended` guest resumes it**: vm start finds the saved state.
- **`locked`** is refused (`409`) for every operation: another host holds the guest in a shared datastore.
- **`bootloader`** is refused for stop and restart. vm-bhyve's stop asks on its terminal before killing a boot loader (`util::confirm`), and with Jail Deck's `/dev/null` for a terminal it declines and exits 0 **without saying so**, read in its source. Reporting that as a stop would be false.
- **Restart** is Jail Deck's stop (deadline and fallback included) followed by start, not vm-bhyve's `restart`, which asks the guest to reboot through the power button and has no deadline. A forced stop shows as `forced: true` even though the guest is running again by the time the answer comes.

Like a jail's, these are detached from the request: a client that disconnects does not stop a lifecycle step halfway. Each step is bounded by its own timeout, and the HTTP server has no write timeout for that reason.

---

## JD-037 — Image Templates for VMs: a Verified Archive, a zvol, and a Clone the Host Never Tastes

**Status:** Locked — 2026-10-26. Measured on isengard, 15.1-RELEASE-p5, vm-bhyve 1.7.5, by fetching Debian 13's genericcloud image, writing it into a zvol and booting a guest from a clone of it (`capture-vm-images-induced.sh`, run twice); vm-bhyve's `create` and `destroy` read in its source.

An **image template** is to a VM what a release template is to a jail (JD-027): something prepared once and cloned for every guest. `PUT /api/vm-images/{name}` prepares one, `GET` lists and shows them, `DELETE` removes one that no guest uses. Creating a guest from one is the next step, and the part of it this measured is recorded here so that step starts from facts.

### Where an image comes from: a source the adapter knows

The request names a **source**, not a URL: `{"source": "debian-13"}`. The adapter knows where that source lives, which file is the disk, and which checksum file vouches for it, the way the release adapter knows the FreeBSD mirror (JD-010). One source to start with:

| source | archive | checksum | disk in the archive |
| --- | --- | --- | --- |
| `debian-13` | `cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.tar.xz`, 217 MiB | `SHA512SUMS` beside it | `disk.raw`, 3 GiB |

**Why the `.tar.xz` and not the `.qcow2`** (342 MB) or the `.raw` (3 GiB): it is the smallest, and `tar` in base reads it. A qcow2 needs `qemu-img`, which JD-019 already declined to depend on.

**Why a catalog and not any URL with a checksum:** cloud images do not share a format. Debian ships a raw disk in a tar, Ubuntu a qcow2, others an `.xz` of a raw disk. A URL field would promise all of them and deliver one. A named source is one format the adapter actually handles, and another source is an entry plus, at most, a new way to unpack. An operator-supplied URL can come later as a source of its own, with the format stated.

**Verification is the publisher's checksum, over HTTPS.** Debian publishes `SHA512SUMS` for these images, and no signature beside it, so a checksum fetched from the same place guards against corruption, not against the server. That is the same strength as the release template's MANIFEST (JD-008's stage 1). The archive is downloaded to `<file>.part` and **renamed only once its SHA-512 matches**, so an archive under its own name is always a verified one, and "fetched" can be read off the disk. `fetch -a` resumes a `.part`.

Measured: SHA512SUMS 2 s, the archive **69–79 s**, the checksum 1 s.

**"latest" moved during the measuring.** Debian published a new build on 2026-10-26: the archive is dated 19:10 UTC and `SHA512SUMS` **19:24 UTC**, so for fourteen minutes the directory held a new archive beside the old checksums. A fetch in that window fails verification and its `.part` is removed, and asking again later succeeds — the failure is the honest outcome, not one to retry around. The two captures fetched different builds (`2a813040…` and `893deb85…`), which is what recording the checksum on the image is for.

### The image is a zvol, written straight from the archive

```
zfs create -s -V <size of disk.raw> -o volmode=dev <datastore>/.images/<name>
tar -xOf <archive> disk.raw  >  /dev/zvol/<datastore>/.images/<name>
zfs snapshot <datastore>/.images/<name>@base
```

- **Under vm-bhyve's own datastore**, `tank/vm/.images` on isengard, so images and guests share a pool and a guest's disk can be a clone. A dot-name is not a guest: `vm list` was unchanged with it there. The archives live in the same directory, `/vm/.images`, and are kept, as `base.txz` is, so an image can be rewritten without downloading again.
- **Its size is `disk.raw`'s**, read from `tar -tvf` (3 GiB, 13 s, since xz has to be read through to list it). Sparse: measured, the written image used **541 MiB** of the pool for 923 MiB of data, with compression on.
- **No `dd`, no shell, no temporary file.** tar writes the disk to its standard output, and the runner gained `StdoutFile` to point that at the zvol. Measured with a synthetic archive: tar's writes are 16 KiB and 64 KiB, the result is byte-identical, and a dev-mode zvol accepts even an unaligned write. Writing the real image took **18 s**.
- **Provenance is on the zvol**, as user properties set when it is created: `jaildeck:source` and `jaildeck:sha512`, the checksum of the archive it was written from. Debian's "latest" moves, and the checksum names the exact build. Recorded on the dataset, not in Jail Deck (JD-011).
- **`@base` makes it ready**, as for a release template (JD-007), and the states are read the same way (JD-008): `ready`, and `written` for a zvol with no `@base` (a stage that stopped). `fetching` and `writing` come from what this process is doing; an image being fetched has no zvol yet and is listed all the same, because the resource a client watches has to exist from the start. **"Fetched" belongs to the source, not the image**: one archive can be written into any number of images, so the listing's `sources` say whether each one's verified archive is on the host, and a prepare from a fetched source starts at the write. Preparing an image that is `written` is refused, as an extracted template is: a half-written disk looks like a whole one.

### `volmode=dev`, on the image and on every clone

A guest's disk is a clone of `@base`, grown to the size the guest asks for. **With GEOM's default `volmode`, growing it breaks the guest.** FreeBSD tastes the zvol, finds the image's GPT, and with `kern.geom.part.auto_resize=1` (the default) moves the backup header to the new end by itself, then holds the disk **open for exclusive write** until someone runs `gpart commit` or `gpart undo`. Measured, both runs: the kernel says so (`GEOM_PART: … was automatically resized`), the consumer is `r1w1e1`, `zfs destroy` answers "dataset is busy", and a guest started on it found **nothing bootable** on its disk and sat in the firmware's menu. `vm destroy` then failed too, and only said "failed to destroy ZFS dataset", because it sends `zfs destroy`'s own message to `/dev/null`.

`volmode=dev` exposes a zvol as a plain device and keeps GEOM out of it entirely. It goes on `.images`, so every image inherits it, and **explicitly on each clone**, which inherits from the guest's dataset rather than the image. Measured with it: no partition devices on the host, the guest booted Debian, **cloud-init grew the root filesystem to the new size by itself** (`EXT4-fs (vda1): resized filesystem` 8 s into the boot; the GPT's backup header moved from sector 6291455 to 16777215), and `vm destroy` exited 0. It is also the right default regardless: the host has no business parsing a guest's partition table.

### A guest's disk, measured for the next step

`vm create` makes `disk0` itself and has no way to be handed one. vm-bhyve can import an image (`-i`), but only through `qemu-img`. So a guest is created as usual, with its cloud-init seed (`-C`), and its `disk0` is replaced:

```
zfs destroy <datastore>/<guest>/disk0
zfs clone -o volmode=dev -o volsize=<size> <image>@base <datastore>/<guest>/disk0
```

- The size can be given in the clone itself; measured, `-o volsize=8G` was accepted. A clone costs **56 KiB** when made, and 5.6 MiB after its first boot.
- vm-bhyve finds the clone where it expects `disk0`, and **`vm destroy` takes it away** with the guest, because it destroys the guest's dataset recursively. The image's `@base` is then free again.
- `vm create -C` writes the seed ISO at the guest's **first start**, not when it is created (`vm info` showed it missing until then). Its user-data is read: a `power_state` in it powered the guest off after its first boot, 141 s in, with no network to wait for.

### Removing an image

`DELETE /api/vm-images/{name}?confirm=<name>` destroys the zvol and its snapshot, with the same `confirm` a template's removal asks for: an image is a download and a write to make again. **It is refused while any clone of `@base` exists**, naming the guests (`zfs get clones`): ZFS refuses anyway ("snapshot has dependent clones"), and the refusal is better said in terms of guests than of datasets. The archive is kept; it belongs to the source, and another image may be written from it.

### Operations

Each stage writes its own audit entry: `fetch-image`, `write-image`, `seal-image`, and `destroy-image` for a removal. Like the template's, preparing is detached from the request and answers `202` (JD-008, which already listed "importing a VM image" among them).

