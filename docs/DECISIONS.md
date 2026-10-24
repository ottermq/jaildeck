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

### Rationale

Templates are shared, immutable-once-snapshotted bases; jails are cheap clones of them. This keeps jail creation fast and avoids re-fetching/re-patching a userland per jail.

**Amended 2026-10-21 — child datasets and root re-cloning.** A jail may have **child datasets** under its own dataset (e.g. `containers/<name>/data` mounted at `/var/db/postgres`), created with properties at creation time. Two reasons:

- **Quotas belong on a child.** A clone's `referenced` space includes the template's blocks, so a quota on the jail's root dataset counts the userland against the jail. A `refquota` on a child dataset counts only what the jail wrote.
- **State in a child makes the root replaceable.** A jail whose persistent state lives in a child can have its root re-cloned from a newer template snapshot — move the child aside with `zfs rename -u`, destroy and re-clone the root, move the child back. That is a generally useful upgrade path, not one specific to any consumer.

The same rename is what lets a jail be destroyed while its data is kept: `zfs destroy -r` on the jail takes its children and their snapshots with it, so anything worth keeping is renamed out first.

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

**Amended 2026-10-21 — exec.** The exec endpoint (JD-016) is the one place a caller supplies a command's timeout. It is capped by a server-side maximum, so a caller can shorten the bound but never remove it, and the output kept is capped in size. The cap values are still open.

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
- **Change policy.** Breaking changes are allowed before 1.0 but each is recorded under an **API** heading in the changelog. A client pins a Jail Deck version and runs contract tests before upgrading.

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

### Rationale

**The allowlist is the API's security boundary.** A token holder can already do anything root can (JD-003), but the API should not make it *easy* to create a jail that can escape its own confinement. A denylist fails open the day jail(8) gains a parameter; an allowlist fails closed.

**Refusing beats running unlimited.** A jail that was asked for a 640M memory limit and silently got none is worse than an error: the operator believes something that is false.

### Still open

- Whether limits are applied by `exec.poststart`/`exec.prestop` hooks in the stanza or by the adapter. Leaning hooks, for the same reason as the epair in JD-017: the jail behaves the same however it is started.

---

## JD-019 — Virtual Machines (bhyve)

**Status:** Open — scope locked 2026-10-21; mechanism pending a spike

**Scope (locked).** Jail Deck gains a `vms` domain mirroring jails: list and inspect; create from an image template with CPU, memory and cpuset sizing, a zvol disk and a cloud-init seed; start, stop, restart, destroy with `confirm`; a tap on an operator-owned bridge, as JD-017 does for epairs; serial console through the API later. VM state is derived from the system, never stored (JD-011). The host prerequisite is `VT-x` with `EPT` and `UG` in `dmesg`, reported by Host Setup.

**Mechanism (open).** JD-005 chose `service jail` because it is the standard administrative interface. bhyve has no equivalent in base. The candidates:

| Option | Fit | For | Against |
| --- | --- | --- | --- |
| **A. `vm-bhyve`** (`sysutils/vm-bhyve`) | JD-005/JD-012: drive an established admin tool; its per-VM config files and datasets are derivable state | Mature; supervision, taps, UEFI, ISO and cloud images; cloud-init support (to verify) | A dependency outside base; its maintenance cadence needs checking |
| **B. Direct `bhyve(8)` + `bhyvectl` + `daemon(8)`** | JD-012: base tools only | Full control; no extra dependency; the most to learn | Jail Deck owns supervision across its own restarts, tap lifecycle and host reboots — more code |
| C. libvirt | Weak: a daemon and abstraction foreign to FreeBSD conventions | Feature-rich | Heavy runtime dependency; needs `libvirtd`; cgo bindings |

Spike A and B at the start of the Virtual Machines milestone, then lock this decision. Leaning **A** for consistency with JD-005, unless the spike shows gaps in cloud-init or maintenance. **C is rejected.**

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
- **Jail Deck adds the step only it can take.** After a start it could not confirm, it destroys the epair that stanza would have created.

The orphan is found by the description the stanza writes, `descr jail:<name>`, not by the interface name alone. A sweep that matched on name could take an interface belonging to something else on the host.

The cleanup is itself a command that can fail, and when it does the orphan is **named in the error** rather than swallowed. A leaked epair someone has been told about is a minute's work; a silent one is a jail that will never start again and no clue as to why.

### Destroy stops, unconfigures, and then lets ZFS have the last word

In order: stop the jail, remove it from `jail_list`, delete its configuration file, destroy the dataset.

Measured: `devfs` is unmounted when the jail stops, and `zfs destroy` then succeeds immediately with no busy error. Measured also: `zfs destroy` *while* the jail runs exits 1 with `pool or dataset is busy`.

That refusal is kept as the last line of defence rather than replaced by a check of Jail Deck's own. A check reads the state and then acts on what it read; the mount is held for as long as the jail is up. Only one of those can lose a race.

Destruction takes `DELETE /api/jails/{name}?confirm=<name>` — the same typed confirmation JD-024 asks for before a snapshot goes and JD-027 before a template does, for the same reason.

A jail whose dataset has snapshots or clones is **refused**, with the dependents named, as JD-024 already refuses. `zfs destroy -R` is never passed.

### Orchestration lives in `jails`; the primitives stay in `storage`

The `jails` context owns the order of the steps, because the order is a rule about jails: confirm the template's `@base`, clone it, render the configuration, set autostart, start, confirm. Cloning a snapshot, creating a child dataset and destroying a root belong to `storage`, reached through ports that `jails` defines — the direction JD-010 set, and the one `storage.JailSource` already implements for the storage writes.

The alternative, a single `storage` method that creates a jail end to end, would put the knowledge that a jail is "clone, then configure, then start" inside the package whose subject is datasets.

### A half-created jail is reported, not rolled back

If the configuration write fails after the clone succeeded, the clone stays. The response names the step that stopped and what exists, and nothing is undone — the same position JD-026 takes for a re-clone, and for the same reason: an automatic rollback is a second destructive operation running at the moment least is known about the state.

The epair is the one exception, and it is an exception precisely because it is not recoverable state. Everything else a half-created jail leaves behind can be looked at, kept or removed deliberately. A leftover epair can only block the retry.

---

## Decision Policy

Once a decision is marked as **Locked**, it should not be revisited without a clear technical or product justification. Architectural evolution is expected, but changes should be deliberate and documented rather than occurring through incremental drift.
