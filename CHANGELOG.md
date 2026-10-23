# Changelog

Every released version is recorded here. A version additionally gets a
[release](https://github.com/ottermq/jaildeck/releases) with full notes when
it carries something an upgrader has to act on — a changed output format, a new
mandatory step, a migration. Versions that only fix internal behaviour live here
alone.

Dates are the tag date. Issue numbers link to
[the tracker](https://github.com/ottermq/jaildeck/issues).

Anything that changes the JSON API's surface is recorded under an **API** heading,
whether or not it breaks a client, so that a client pinning a version can see in
one place what a contract test would notice (JD-016). Breaking changes are called
out as such; before 1.0 they are allowed, but never silent.

The repository moved from Codeberg to GitHub on 2026-10-21. Issue and pull
request numbers in v0.6.0 and earlier, including the audit cursor entry in
v0.7.0, refer to the archived
[Codeberg tracker](https://codeberg.org/Otter-Labs/jaildeck/issues), and those
versions' release notes stay there. GitHub numbering started again at #1.

## Unreleased

### API

- **Derived templates.** `GET /api/templates` lists templates with their readiness and
  lineage; `PUT /api/templates/{name}` builds one from another plus packages — clone the
  parent's `@base`, `pkg -c <root> install -y`, snapshot as `@base`. (#7, JD-027)

  #7 asked whether this belonged to Jail Deck or to the caller. It belongs here, and the
  deciding fact is that the alternative does not work: the exec endpoint is `jexec`-based
  and a template is not a running jail, so "exec plus snapshot from the caller's side" would
  have meant exposing **arbitrary command execution against a template root** — strictly
  more powerful than installing a named package list.

  Nothing is stored. `ready` is whether `@base` exists; `parent` is read back from
  `zfs get origin`. A failed install leaves the clone without its `@base`, which the listing
  reports as not ready — no cleanup is needed for that state to be truthful — and a second
  attempt on it is refused rather than resumed.

  This also settles the **storage domain granularity** question `docs/SPEC.md` had open:
  templates get their own bounded context, `internal/templates`.

- **A jail's root can be re-cloned from a newer template, keeping its children.**
  `POST /api/jails/{name}/reclone?confirm={name}` stops the jail, moves every child dataset
  aside, parks the old root, clones the new one, moves the children back and starts it
  again — the upgrade path for a jail whose state lives in a child (#6, JD-007, JD-026).
  The jail ends in the state it was found in.

  **Nothing is destroyed.** The old root is renamed to
  `<pool>/jails/retained/<jail>-root-<snapshot>`, so a re-clone onto the wrong template
  costs one call to undo. It is a clone and occupies only what the jail wrote since it was
  made.

  **If a step fails, nothing is retried and nothing is undone.** The response names the step
  and lists every dataset that is no longer under the jail, with where it now sits, so it
  can be restored with the call JD-025 provides. An automatic undo that itself fails leaves
  two interleaved failures and a host in a state neither describes.

  Refusals happen **before** the jail is stopped: a mistyped template name costs an error
  rather than an outage. The template is the full `dataset@snapshot` with no default,
  because every jail is already a clone of `templates/<release>@base` and defaulting to it
  would be a no-op dressed as an upgrade.

### Changed

- **The documentation is published as a site.** `docs/` is now a Jekyll site built by
  GitHub Pages at [otter-labs.com/jaildeck](https://otter-labs.com/jaildeck/): the
  specification, architecture, decisions, roadmap, interface notes and the API guide, with
  the generated spec downloadable at `/jaildeck/api/swagger.json`.

  Nothing moved, so every existing link into `docs/` still works. Each page gained front
  matter and a link back to the index, because the theme has no navigation of its own.

  The site builds from `main`, and pull requests land on `dev` first, so the published
  version tracks releases rather than development.

- **The API is described by a generated OpenAPI spec, not a hand-maintained collection.**
  `collections/insomnium.json` is gone. `make doc` generates
  [`docs/api/swagger.json`](docs/api/swagger.json) from annotations on the handlers, using
  `swag` pinned in `go.mod` as a tool dependency — no global install, same version
  everywhere. Import it into any client that reads Swagger 2.0.

  The collection was 1,648 lines of JSON edited by hand for one tool. The guarantee it
  carried is kept and strengthened: `make check` now fails if the committed spec is not what
  the annotations produce, and the suite fails in both directions besides. That matters
  because `swag` silently skips an un-annotated handler, so an undocumented endpoint would
  otherwise produce a spec that is merely shorter.

  Jail Deck serves no Swagger UI; the spec is a file. The long-form API guide moved to
  [`docs/api/index.md`](docs/api/index.md).

  No endpoint changed. `common.ErrorResponse`, `auth.LoginRequest` and
  `storage.RetainedDataset` are named types now rather than inline literals, so the spec can
  point at them — the JSON each produces is identical.

### API

- **A dataset can be moved out of a jail and back in.**
  `POST /api/jails/{name}/datasets/{child}/retain` moves it to
  `<pool>/jails/retained/<name>`, `GET /api/retained` lists what is being kept, and
  `.../restore` brings one back under any jail. Destroying a jail takes its children and
  their snapshots with it (JD-007), so this is what makes "destroy the jail, keep the data"
  possible. (#5, JD-025)

  Three audited commands: `zfs rename -u`, `zfs unmount`, `zfs mount`. **The `-u` is not
  optional** — a plain `zfs rename` unmounts and remounts inside the same command, which on a
  running jail is the filesystem vanishing from under a live process. And the move is still
  completed in the same call, because a dataset left named in one place and mounted in another
  makes the jail undestroyable: `zfs destroy` answers `pool or dataset is busy`.

  A running jail loses that path at the unmount; the response says `remounted: true` so a
  caller knows the filesystem moved and not just the name.

  `<pool>/jails/retained` is created on demand and is **not** added to the layout readiness
  check: a host that has never retained anything is not missing something (JD-021).

- **Snapshots.** `GET /api/jails/{name}/snapshots` lists every snapshot of a jail's datasets;
  `PUT`, `DELETE` and `POST .../rollback` under `/snapshots/{snapshot}` take, destroy and roll
  back, with the same routes under `/datasets/{child}/` for one dataset alone. Every call is
  audited. (#4, JD-024)

  **A jail-level snapshot is recursive and atomic** — `zfs snapshot -r` takes the jail and
  every child at one point in time, which is what lets a root be re-cloned while its data
  dataset stays consistent with it (JD-007). **A rollback is never recursive**, because ZFS
  offers no way to make it so: `zfs rollback` cannot descend into children, and its own `-r`
  means "destroy the snapshots newer than this one". A jail-level rollback therefore leaves
  the children where they are.

  **Destroy and rollback take `confirm=`, and it is the snapshot's full `dataset@snapshot`** —
  not the word already in the path, so a script looping over jails with one snapshot name in a
  variable cannot confirm every one of them by accident (JD-016).

  **Refusals come before anything destructive runs.** A rollback that would destroy newer
  snapshots is `409` naming every one of them, until `destroy_newer=true` accepts losing
  exactly those. A clone or a hold blocks both a destroy and a rollback and cannot be
  overridden here at all. What blocks is asked for with `zfs get clones` and `zfs holds`,
  rather than read out of the error the destroy would have raised.

  Snapshots carry `create_txg`, ZFS's transaction group, and are ordered by it. `created` has
  second granularity and ties; the transaction group never does, and it is the ordering a
  rollback actually asks about.

- **A jail's datasets and tags can be written.** `PUT /api/jails/{name}/datasets/{child}`
  creates a child dataset or converges an existing one — `201` created, `200` already
  there, `409` it exists somewhere else. `PATCH /api/jails/{name}/properties` and
  `PATCH /api/jails/{name}/datasets/{child}/properties` set ZFS user properties, with a
  `null` value removing one via `zfs inherit`. Every write is audited with the command it
  ran, and carries the request's `X-Request-Id`. (#3, JD-023)

  **Five native properties are allowlisted** — `mountpoint`, `refquota`, `recordsize`,
  `compression`, `atime` — out of the sixty-odd this ZFS reports as editable. Anything else
  is `400` naming the list: `sharenfs` would publish a jail's filesystem over NFS and
  `canmount` would stop it mounting. `quota` is deliberately absent, because JD-007 puts the
  limit on `refquota`.

  `mountpoint` may be set only when creating. Changing it under a running jail would leave
  the jail looking at a directory no longer backed by that dataset, so asking for one an
  existing dataset does not have is `409` with both values rather than a move.

  **A value `zfs` refuses is `400`, not `500`**, carrying zfs's own sentence — a client told
  `500` would retry a value that can never work. The distinction available is only "ran and
  exited non-zero" against "never ran", so a server-side refusal such as permission denied
  also reports `400`; the message says which.

- **A jail listing carries its ZFS storage.** `GET /api/jails` and the new
  `GET /api/jails/{name}` include `datasets` — the dataset mounted at the jail's
  `path` and everything mounted under it, each with `name`, `mountpoint`, `used`,
  `referenced` and `available` — and `properties`, the ZFS *user* properties of the
  jail's own dataset. An automation client tags its jails with `otter:instance` and
  finds them again from the listing alone, with nothing stored in Jail Deck
  (JD-004, JD-011). Additive: both keys are absent for a jail that has neither.

  **The sizes are bytes**, not the `1.41G` that `zfs list` prints by default, so a
  client can compare and total them; the web interface formats them for display.
  `referenced` can exceed `used` on a jail cloned from a template, because a clone
  still refers to the template's blocks — the reason a quota belongs on a child
  dataset (JD-007).

  A jail is matched to its datasets **by mountpoint**, not by the dataset naming
  convention: a dataset is the jail's when it is mounted at the jail's path or
  under it. That holds for a jail placed outside `<pool>/jails/containers/<name>`,
  and reports nothing for a jail not on ZFS. A jail whose stanza sets no path
  matches nothing rather than everything.

  One batched `zfs list` and one batched `zfs get` answer every jail, never one
  call per jail, scoped to `JAILDECK_ZFS_POOL` when it is set and to every imported
  pool when it is not.

  **An unreadable pool does not fail the request.** The jails come back without
  their datasets and the reason is logged at `WARN`, because seeing a stopped jail
  is how an operator starts it (JD-004). The cost, accepted deliberately: absent
  storage can mean either "this jail has none" or "ZFS could not be read". (#2)

- **`X-Request-Id` is recorded in every audit entry a request produces**, and
  `GET /api/audit?request_id=<id>` filters on it, so an automation client can line
  its own job up against Jail Deck's audit trail (JD-016). Additive: an entry omits
  `request_id` entirely when the request sent no header, so a client that does not
  use it sees the shape it saw before.

  Up to 64 characters of ASCII letters, digits and `-._:` — a UUID, a ULID or a
  `trace:span` pair. Anything else is **dropped rather than rejected**, with the
  reason logged: the operation still succeeds and its entries carry no id. A
  correlation id is a convenience, and failing an operator's jail restart because
  their id held a space would be the worse outcome. The filter matches exactly and
  case-sensitively, since the id is the client's own opaque string and not a name
  Jail Deck normalises. (#14)

### Added

- **A unix socket listener.** `JAILDECK_SOCKET` adds a second listener serving the
  same router, and `JAILDECK_SOCKET_ONLY=true` drops the TCP one, so a local
  automation client needs neither a port nor TLS (JD-002). One `http.Server` over
  both listeners, so they cannot drift apart in handler or timeouts.

  The socket is created mode `0660` by umask rather than chmodded afterwards: a
  listening socket queues connections from the moment it exists, so a chmod would
  leave a window in which the default `0755` was world-connectable. Its group is a
  first access check and not the only one — `RequireAuth` runs here exactly as on
  TCP, so the token is still required. `JAILDECK_SOCKET_GROUP` names the group;
  empty leaves it in the process's own, which for a root service on FreeBSD is
  `wheel`.

  Jail Deck installs no signal handler, so a stopped service leaves the socket file
  behind. The next start probes it: something answering means another instance, and
  it refuses rather than stealing its clients; nothing answering means a leftover,
  which it removes. A path holding a regular file is a configuration error and is
  never deleted. `JAILDECK_SOCKET_ONLY` needs `JAILDECK_SOCKET` set, and says so
  rather than starting with nothing to listen on. (#13)

- **An `rc.d` service script, `make install`, and one config file.** `make install`
  puts the binary in `$PREFIX/bin`, the script in `$PREFIX/etc/rc.d` and a sample
  config in `$PREFIX/etc/jaildeck` (`PREFIX` defaults to `/usr/local`, `DESTDIR`
  stages the layout elsewhere). After that,
  `sysrc jaildeck_enable=YES && service jaildeck start` runs Jail Deck as a
  service that survives a reboot.

  **Configuration is a file, not rc.conf knobs.** `/usr/local/etc/jaildeck/jaildeck.env`
  takes the same `KEY=value` lines as `.env.sample` and the same variables as the
  README's table, so one format configures Jail Deck whether it is started by hand
  or by `rc.d`. The script sources it with `set -a` and keeps only four knobs of its
  own, none of which are Jail Deck's configuration.

  Jail Deck runs in the foreground, so the script runs it under `daemon(8)` with
  `-f -S`: output goes to syslog under the tag `jaildeck`, which is what the log
  handler's missing timestamps were always for, and `-f` keeps `service jaildeck
  start` from hanging a remote shell. `-r` is deliberately not passed, so
  `service jaildeck stop` stops it and nothing puts it back.

## v0.7.0 — 2026-10-22

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.7.0)

### Breaking

- **Module path is now `github.com/ottermq/jaildeck`**, following the move back
  to GitHub. Builds that stamp the version with `-ldflags -X` need the new
  package path.

### Added

- **`GET /api/audit` pages backwards with a cursor.** `?before=<RFC 3339
  timestamp>` returns entries strictly older than the one given, newest first,
  and composes with the existing `operation`, `targets`, `success` and
  `timed_out` filters. Pass the timestamp of the oldest entry already received.

  The response stays a plain array, so a client knows there may be more the
  same way the interface does: a page that fills `limit` might not be the last
  one. An unreadable `before` is a `400` rather than being ignored, because a
  silently dropped cursor returns the first page again and a client paging
  backwards would loop on it.

  A cursor rather than an offset because the audit log is an append-only file
  that `newsyslog` rotates and new entries extend — both shift what an offset
  means between one request and the next, so `?offset=` can skip a page or
  repeat a row. (#67)

- **A "Load older" control on the operations page**, which appends the next
  page rather than replacing the table. (#67)

- **`GET /api/host/readiness` reports what the host can do, per capability.**
  Six checks — `kern.racct.enable`, `exec.timeout` in the jail.conf defaults,
  bridges, devfs rulesets, VT-x with EPT and UG, and PF — each answer `pass`,
  `fail` or `unknown`. Each one states what it observed and which capability
  it gates (`jails`, `limits`, `vnet`, `provisioning`, `vms`, `firewall`).
  Every failed check carries a remedy, written as a command to paste into a
  root shell. Nothing in the path writes to the host.

  Readiness is per capability because "is this host ready" has no single
  answer: VT-x matters only to someone who wants VMs. A capability is
  `unavailable` if any check gating it fails, `unknown` if any is unknown, and
  `ready` otherwise. The report carries both levels so no client reimplements
  that rule.

  `unknown` is a third answer, not a softer `fail`, and carries no remedy. A
  kernel without RACCT has no `kern.racct.enable` to set, so the loader.conf
  advice that fixes a `0` would be wrong there. A host that is not x86 has no
  VT-x line to find, and an absent line is not proof the CPU lacks it. A wrong
  `fail` sends the operator to fix something that may be fine.

  The rules were written against real output from four hosts, committed under
  `internal/system/freebsd/testdata/hostfacts`, and three of them changed
  because of it. The VT-x line is read from `/var/run/dmesg.boot`, because the
  live `dmesg` buffer wraps on a long-running host and loses it. devfs
  rulesets come from the kernel: no captured host has `/etc/devfs.rules`, yet
  every one has rulesets 1–5 loaded. And a host need not have `/etc/jail.conf`
  at all. (#1)

- **`jaildeck init` and `PUT /api/host/layout` ensure the jails dataset
  layout:** `<pool>/jails` mounted at `/usr/local/jails`, with `media`,
  `templates` and `containers` beneath it. Both call one service method, and
  `GET /api/host/layout` reports each dataset, its mountpoint and what is
  missing. The readiness report gains `zfs_pool` and `zfs_layout`, which gate
  `provisioning`.

  Ensure, never clobber. When everything is in place no `zfs create` runs and
  the answer is `200`. When something is missing, only that is created, parent
  first, and the answer is `201`. A dataset that exists but is mounted
  somewhere else is a `409` that names it, and nothing is created: fixing it
  would move a mounted dataset that may hold live jails. So is a missing
  dataset whose path another dataset already holds, on any pool, since
  creating it would mount it over that one. `PUT` rather than
  `POST` because the layout is a singleton whose desired state is fixed
  (JD-016), and there is no `confirm` parameter because nothing here destroys.
  Each `zfs create` is its own `create-dataset` audit entry, findable with
  `?targets=<dataset>`.

  A host with one pool has it adopted. With several, `JAILDECK_ZFS_POOL` must
  name one, and until it does the `zfs_pool` check fails, listing the pools.
  Jail Deck does not pick: on the deployment host the first pool printed holds
  a replica of the layout, mounted nowhere. A missing or ambiguous pool fails
  the check but never stops Jail Deck from starting, since listing and
  running jails do not need the layout. A host with no ZFS fails it too,
  detected by `zpool` exiting non-zero, because the binary is in base even
  where ZFS is not. (#21)

- **A Host page shows the readiness report**, capability by capability, each
  with the checks that gate it. It leads with what is ready. An unavailable
  capability is drawn neutral, not red, because a host without VT-x is a
  perfectly good jail host. `unknown` is amber, like a timed-out operation,
  and never reads as a failure: a wrapped `dmesg` buffer is not a CPU without
  VT-x. Every remedy is shown exactly as the server sends it, as code to
  paste into a root shell, with a copy button where the browser allows one.
  The report is recomputed per request (JD-011), so the page keeps nothing
  between loads and has a Refresh control instead. It has no button for
  `PUT /api/host/layout`: a control that changes the host needs its own
  decision. (#22)

### Fixed

- **The non-loopback startup warning now says that login will not work.** It
  named the real risk — the operator password crossing the network in
  plaintext — but not the consequence an operator meets first. The session
  cookie is `Secure` (JD-003), so a browser on a plain-HTTP origin discards
  it: the login returns `200` and every request after it returns `401`, which
  looks exactly like a rejected password. The two `401`s are told apart only
  by their bodies. The README now says the same where it explains the port
  forward. (#27)

## v0.6.0 — 2026-10-21

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.6.0)

### Breaking

- **The JSON API moved under `/api`.** Every path changed: `/jails` is now
  `/api/jails`, `/audit` is `/api/audit`, `/auth/login` is `/api/auth/login`.
  Clients need their base URL updated — appending `/api` is enough, which is
  all the bundled Insomnium collection required. The root path is being handed
  to the web interface (#58), so the API needed a namespace of its own. (#57)

- **`GET /api/jails` now uses JSON tags.** The `Jail` type had no JSON tags, so
  it emitted Go field names: `JID`, `Name`, `Status`, `Hostname`, `IP`, `Path`.
  It now emits `jid`, `name`, `status`, `hostname`, `ip`, `path`, matching every
  other type the API returns. The jail returned by `POST /api/jails/{name}/start`,
  `/stop` and `/restart` changes the same way. (#60)

### Added

- **A web interface.** The binary now serves a Svelte single-page application
  at `/`; the JSON API stays under `/api`. The frontend is embedded via
  `embed.FS`, so there is still one binary and no Node.js at runtime — building
  it is `make web`, and a clone without that step compiles and runs, serving a
  page that says so. (#58)

- **The jails list, with `start`, `stop` and `restart`.** `/jails` lists every
  jail on the host with its JID, hostname, IP and path, and starts, stops or
  restarts them from the browser; stop and restart confirm first. An operation
  can run to its 150-second backstop (JD-014), so the row shows progress and
  refuses a second click until it returns.

  Two behaviours worth knowing. A failure shows the command's own diagnostic
  rather than a generic message. And a **timed-out** operation is reported as
  *may still be running* rather than as a failure — Jail Deck stopped waiting,
  which is not the same as the jail failing to start — with the list refetched
  so it shows the jail's actual state. (#60)

- **The operations view.** `/operations` lists what Jail Deck has run, newest
  first, with the command that actually executed and filters for target,
  operation and outcome. A **timed-out** operation is shown as its own outcome
  rather than as a failure: the API distinguishes the two deliberately, and
  before v0.4.0 both reported exit code `-1`. (#61)

### Fixed

- **`GET /api/audit` returns `[]` rather than `null` when nothing matches.** A
  fresh install with no audit file, and any filter matching no entries, both
  returned JSON `null`, so every client had to handle two shapes for "no
  results". `GET /api/jails` already returned `[]`. (#61)

- **`jail.conf` shared defaults are honoured.** Settings outside any jail block
  apply to every jail below them, which is the layout the FreeBSD handbook
  recommends: shared defaults in `/etc/jail.conf`, one small file per jail in
  `/etc/jail.conf.d/`. Jail Deck read only what sat inside a `name { … }` block,
  so a jail configured that way reported an empty `Path` — and its hostname was
  correct only by coincidence, since the fallback happens to be the jail's own
  name. A `${name}` in a shared default is expanded per jail rather than once.
  Running jails were unaffected because `jls` supplies the real values, so this
  was visible only for stopped ones. (#51)

- **`ip4` is no longer reported as a jail's address.** It names a mode —
  `inherit`, `new` or `disable` — while addresses live in `ip4.addr`. A jail
  configured with `ip4 = inherit` showed the literal string `inherit` where its
  IP should be; it now shows none, which is also what `jls` reports for it. (#51)

## v0.5.2 — 2026-10-07

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.5.2)

### Changed

- **The request log is now structured, and on stderr.** It previously came from
  chi's own middleware as free-form lines on stdout; it now goes through
  `log/slog` alongside every other record — one record per request, with the level
  taken from the status: 5xx `ERROR`, 4xx `WARN`, otherwise `INFO`. Anything
  reading stdout for request lines will find nothing there. (#44)
- **The startup line is structured**: `msg=listening version=… addr=…`, in place
  of a prose sentence.

### Added

- `principal_kind` on every authenticated request record, and `principal` when the
  credential has a name — a token's name, the one `jaildeck token revoke` takes. A
  session has no name; there is one operator. (#44)
- Records emitted while serving a request now carry that request's `method`,
  `path` and principal. A failed jail command's DEBUG record therefore says which
  request and which credential produced it. (#44)

### Fixed

- The startup record was malformed. `slog.Info` was called with a format string
  and positional arguments, so slog paired them as an attribute and the version
  became the *key*: `msg="Jail Deck %s listening on %s" v0.5.1=127.0.0.1:8888`.

### Known issues

- **`jail.conf` shared defaults are ignored.** A stopped jail configured the way
  the FreeBSD handbook recommends — shared defaults in `/etc/jail.conf`, one file
  per jail in `/etc/jail.conf.d/` — reports an empty `Path`, and `ip4 = inherit`
  surfaces as the literal string `inherit`. Running jails are unaffected, because
  `jls` supplies the real values. (#51)

## v0.5.1 — 2026-10-04

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.5.1)

### Changed

- **Log output is now structured** — `log/slog` with a text handler, on stderr,
  one record per line. Anything parsing the previous free-form output needs
  adjusting. The timestamp is omitted deliberately: `daemon(8)` and syslog add
  their own. (#31)

### Added

- `JAILDECK_LOG_LEVEL` — `debug`, `info`, `warn`, `error`; default `info`. An
  unrecognised value falls back to `info` and says so rather than refusing to
  start. (#31)
- A failed audit write is logged at `ERROR` with the operation, target and
  underlying error. It previously went to stderr through the standard `log`
  package, which under `rc.d` means wherever the service script sends it —
  usually nowhere, so a lost audit record was silent. (#31)
- DEBUG diagnostics in `internal/system`: every command's exit code, duration
  and stderr; the raw `jls` output when its JSON will not parse; any `jail.conf`
  line the parser could not understand; any value still containing `$` after
  substitution. (#31)
- `component=` on every record, naming the part of Jail Deck that produced it.
  (#31)

### Fixed

- `jail.conf` paths using the bare `$name` form are resolved. Only `${name}` and
  `$(name)` were handled, so `GET /jails` reported a literal `"/jails/$name"` —
  a wrong value rather than a missing one. (#43)
- A `#` inside a quoted `jail.conf` value no longer truncates the line.
  `host.hostname = "a#b";` was cut at the `#` and discarded, after which the
  hostname silently fell back to the jail's own name. Single-quoted values and
  escaped quotes are handled too. (#43)

Both of those were found by the DEBUG diagnostics added above, not by reading
the code.

## v0.5.0 — 2026-10-01

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.5.0)

Mandatory single-operator authentication. The Access Control milestone.

### Breaking

- **Jail Deck will not start without a credential.** Run `jaildeck passwd`
  before upgrading a running install. There is no unauthenticated mode, on any
  bind address, including loopback — a jail with `ip4=inherit` shares the host's
  loopback, so localhost was never a private channel here. (#13, JD-003)
- **Every route requires a credential.** Existing API scripts receive `401`
  until they authenticate with a session cookie or a bearer token. (#15)
- **The audit log moved to `/var/log/jaildeck/audit.log`**, from a
  working-directory-relative path that an `rc.d`-started service wrote to `/` or
  lost entirely. The old file is not read. (#28)

### Added

- Session authentication: argon2id credential, opaque server-side session
  tokens, `HttpOnly`/`Secure`/`SameSite=Strict` cookie with an explicit `Path`
  and a 12-hour `MaxAge`. Not JWT — immediate revocation matters more here than
  stateless verification. (#14, JD-003)
- `jaildeck passwd` and `jaildeck passwd --stdin`, the only writer of the
  credential. Never accepts the password as an argument, so it cannot reach
  shell history. There is no recovery flow: root runs it again. (#12)
- `jaildeck token add|list|revoke` — long-lived API tokens for scripting, as a
  credential distinct from the browser session. Hashed with SHA-256, not
  argon2id: stretching compensates for low entropy, and a 256-bit random value
  has none to buy back. Revocable by name. (#16)
- `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`. (#14)
- An application state file at `/var/db/jaildeck/state.json`, mode `0600`,
  written atomically — temp file, `fsync`, `rename`, `fsync` of the directory.
  (#11, JD-011)
- A startup warning when bound non-loopback, naming both supported paths: a
  reverse proxy terminating TLS, or an SSH `LocalForward`. It warns rather than
  refusing — a proxy cannot be detected from inside the process. (#17, JD-002)
- `GET /audit?timed_out=` filtering. (#20)
- `JAILDECK_STATE_DIR` and `JAILDECK_AUDIT_LOG`.

### Fixed

- Explicit HTTP server timeouts. `WriteTimeout` stays unset deliberately: it
  would cover handler execution, and a jail operation may legitimately run to
  its 150-second backstop. (#18, JD-014)
- Audit entries record the command that actually ran, rather than a
  reconstruction of it. (#21)
- State-file decode errors name the file.

### Notes

- Changing the password invalidates browser sessions but **not** API tokens.
  Sessions are bound to the credential they were issued against; a token is an
  independent credential, revoked explicitly. (JD-003)
- Sessions are held in memory and do not survive a restart.

## v0.4.0 — 2026-09-16

[Release notes](https://codeberg.org/Otter-Labs/jaildeck/releases/tag/v0.4.0)

The API-only release. The server-rendered HTML was removed in favour of a JSON
API; the Svelte replacement has not been built.

### Breaking

- Server-rendered HTML removed. The backend is JSON-only. (JD-009)
- Module path is now `codeberg.org/Otter-Labs/jaildeck`, following the move from
  GitHub.
- An empty `JAILDECK_HOST` no longer binds all interfaces; the default is
  `127.0.0.1`. (JD-002)
- `GET /audit?operation=` matching is case-insensitive, and any value is
  accepted — an operation never logged returns an empty result rather than the
  unfiltered list.
- Jail operations no longer abort when the client disconnects. (JD-014)
- Timed-out operations return `504`, not `500`.

### Added

- `jaildeck version`, and the version in the startup log.
- Command timeouts on every command, with a jail-operation backstop sized above
  FreeBSD's own `exec.timeout` and `stop.timeout`. Commands are terminated as a
  process group. (JD-014)
- `timed_out` on audit entries, distinguishing "we stopped waiting" from "the
  command failed" — both previously reported exit code `-1`.
- `JAILDECK_ADAPTER=freebsd|fake`, selecting the in-memory adapter by
  configuration instead of by editing source.
- `collections/insomnium.json`, an API collection for driving the JSON API.

## v0.3.1 — 2026-07-08

Tag only; no release.

### Fixed

- Logging and error handling in jail operations.

## v0.3.0 — 2026-07-08

Tag only; no release.

### Added

- Operation result handling and UI feedback for jail actions.

## v0.2.0 — 2026-07-07

Tag only; no release.

### Added

- Operation logging.

## v0.1.0 — 2026-07-06

Tag only; no release.

### Added

- Jail listing and lifecycle management.
