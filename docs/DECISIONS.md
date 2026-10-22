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

**Amended 2026-10-21.** A jail listing also returns the jail's child datasets and its ZFS user properties (`zfs get -H -o property,value -s local all`, filtered to names containing `:`). An automation client tags its jails with properties such as `otter:instance` and finds them again from the listing alone, with nothing stored in Jail Deck (JD-011).

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

**To verify (2026-10-21):** that `service jail start|stop` behaves as expected with VNET stanzas whose `exec.prestart`/`exec.poststop` create and destroy epairs (JD-017), and how `jail_enable`/`jail_list` autostart interacts with `jail.conf.d` on 15.1. Neither is expected to change this decision; both are checked in Jail Provisioning.

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

**Amended 2026-10-21 — child datasets and root re-cloning.** A jail may have **child datasets** under its own dataset (e.g. `containers/<name>/data` mounted at `/var/db/postgres`), created with properties at creation time. Two reasons:

- **Quotas belong on a child.** A clone's `referenced` space includes the template's blocks, so a quota on the jail's root dataset counts the userland against the jail. A `refquota` on a child dataset counts only what the jail wrote.
- **State in a child makes the root replaceable.** A jail whose persistent state lives in a child can have its root re-cloned from a newer template snapshot — move the child aside with `zfs rename -u`, destroy and re-clone the root, move the child back. That is a generally useful upgrade path, not one specific to any consumer.

The same rename is what lets a jail be destroyed while its data is kept: `zfs destroy -r` on the jail takes its children and their snapshots with it, so anything worth keeping is renamed out first.

---

## JD-008 — Long-running Operations

**Status:** Open — scope narrowed 2026-09-04

Fetching `base.txz`, extracting it, and running `freebsd-update` can take real time. Whether this needs task queues, polling, or Server-Sent Events, versus staying synchronous with a longer timeout, is still undecided.

**What is now settled is the scope of the question: it applies to the template domain only.** Template preparation is a distinct, operator-initiated step — not part of application setup, and not folded into jail creation (JD-007). Jail creation stays synchronous and fast, because cloning a snapshot is a copy-on-write operation that returns immediately. Jail lifecycle operations are already fast.

So the remaining decision is a progress mechanism for one domain and a handful of endpoints, not a general asynchronous-execution strategy for the application.

**Amended 2026-10-21.** VM image import joins template preparation as the other slow flow that needs the progress mechanism. Jail operations stay synchronous. An automation client that wants asynchronous behaviour provides it itself — it already has its own job state, and a synchronous, idempotent call (JD-016) is easier to retry safely than a job handle Jail Deck would have to remember.

### Rationale

Worth deciding deliberately once template preparation is actually being built, rather than guessing ahead of the real shape of the problem.

Separating template preparation from jail creation shrank this problem considerably. The earlier framing implied every long operation across the app needed an answer; in fact only one flow is slow, which makes polling or SSE on a single resource a proportionate solution and removes any argument for a task queue at this scale.

---

## JD-009 — Frontend: Svelte SPA

**Status:** Locked

Jail Deck's UI is a **Svelte** single-page application. The backend exposes a JSON API; handlers do not render HTML fragments.

Production still ships as a single Go binary: the Svelte build output is embedded via `embed.FS`, the same way templates and static assets were previously. Node.js is a build-time dependency only, never a production one.

### Rationale

The growing feature set (templates, ZFS operations, jail creation flows) benefits from richer client-side interaction than server-rendered HTML fragment-swapping comfortably provides. Svelte was chosen over Vue after direct comparison. The migration happens early — while `jails` and the audit log are the only existing domains, there is a small, well-understood surface to convert. Every domain added afterward (storage, templates) is built API-first from the start, avoiding a larger migration later — see `docs/ROADMAP.md`. The JSON API landed first (Phase 1a); the Svelte UI itself is Phase 1b and not yet built.

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

This decision is about bounding individual commands. How a legitimately slow flow is represented to the operator — progress, polling, or SSE during template preparation — remains open under JD-008.

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

- How a multi-minute template preparation is represented in the UI — blocked on JD-008.
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

**Synchronous is better for automation, not worse.** Jail creation is fast (JD-008). A client with its own job queue gains nothing from a Jail Deck job handle except a second piece of state that can disagree with the first.

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

## Decision Policy

Once a decision is marked as **Locked**, it should not be revisited without a clear technical or product justification. Architectural evolution is expected, but changes should be deliberate and documented rather than occurring through incremental drift.
