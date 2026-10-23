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

- **A release template is brought to its patch level and sealed.** The last two stages,
  which carry a prepare from `extracted` through `updating` to **`ready`** — a template a
  jail can be cloned from, prepared end to end from the interface with no shell steps.

  `freebsd-update -b <root> --currently-running <release> --not-running-from-cron fetch
  install`, every flag measured against the real tool on the deployment host. The **plain
  release** is what `--currently-running` wants, not a patch level: a freshly unpacked
  `base.txz` is exactly the release as published. `--not-running-from-cron` is what makes
  it non-interactive. `fetch install` in one invocation exits 0 even when there is nothing
  new, which is the ordinary case for a recently published release.

  **The `@base` snapshot is taken last, and that ordering is the point.** `base.txz` is
  the release as published, so a template sealed before `freebsd-update` would carry every
  advisory since into every jail cloned from it. An update that fails leaves the template
  `extracted` and unsealed — honestly not ready — with `freebsd-update`'s own sentence in
  the operations log.

  Nothing records which patch level it reached. `freebsd-update` says so in its own output,
  which the audit log keeps, and `freebsd-version -u` inside a jail cloned from the
  template is the live answer; a number stored here could disagree with the userland it
  describes (JD-011).

- **A fetched release is unpacked into a template dataset.** The second stage, which
  carries a prepare from `fetched` to `extracted`: create an empty dataset under
  `<pool>/jails/templates/`, `tar -xpf` the archive into it, and copy the host's
  `resolv.conf` and `localtime` in — without the first a jail cloned from this template
  cannot reach a package mirror, and without the second it logs in UTC while the host does
  not. The tar keeps permissions and flags; a userland extracted without them has a
  world-writable `/etc`.

  **Nothing is ever unpacked over an existing dataset.** A dataset holding half a userland
  looks exactly like one holding a whole one, so a template that already has a dataset and
  no `@base` is a `409` naming itself — the same refusal a derive already made, now the
  rule for every path (JD-027). A failed extract is left exactly where it stopped: the
  dataset is the evidence, and the audit log carries `tar`'s own sentence.

  Repeating a call **resumes**: an archive already in `media/` is not downloaded again.

- **A release template's archive can be fetched from the FreeBSD mirror.** The first stage
  of preparing one, and the first operation to use JD-008's answer:
  `PUT /api/templates/{name}` with `{"release": "15.1-RELEASE"}` **returns 202 and does not
  wait**. An empty release means the host's own, which is also the ceiling — a jail's
  userland must be the same as or older than the kernel it runs under.

  It fetches that release's `base.txz` into `<pool>/jails/media/{release}/` and **verifies
  it against the release's own MANIFEST** before reporting success. That is not politeness:
  an archive that arrived corrupt would be extracted into a template every later jail is
  cloned from.

  **The release, never a patch level.** `kern.osrelease` says `15.1-RELEASE-p5` and the
  mirror publishes no `-pN` directory at all, so the patch level is dropped — it is what
  `freebsd-update` brings the extracted userland to afterwards, not what is downloaded.

  **Templates now report `state`.** Read off the filesystem — the archive is in `media/` or
  it is not, the dataset exists or it does not, the `@base` snapshot exists or it does not
  — so a reload, or a restart of Jail Deck, shows where the work actually got to. A
  template that has only been fetched has no dataset and still appears, because the
  resource a client was told to watch has to exist from the moment the work starts.

  **`running`** is beside it when a step is executing, with the step and when it began. In
  memory only; a second request for a template already in flight is a 409 rather than a
  queued one.

  Naming both `from` and `release` is a 400: they are two ways to build a template, and
  picking one for the caller would be guessing.

### Added

- **Preparing a release from the Templates page.** A form for the release, a `State`
  column in place of the ready badge, and the step with its elapsed time under it while
  something is running. The page polls only while anything is in flight and stops when
  nothing is; a template being prepared cannot be removed. What each stage actually ran is
  on the Operations page, which is where a failure is explained.

### Changed

- **JD-008 is decided: a long-running operation returns `202` and you watch the
  resource.** Preparing a release template will not hold the request open for the minutes
  it takes. There is **no job and no job id** — the thing being watched is the template,
  at the URL it keeps for the rest of its life.

  `state` is read off the filesystem, not recorded: `media/<release>/base.txz` exists, the
  dataset exists, the `@base` snapshot exists. JD-011 had already named those checks as
  the template's state machine. So a restart or a client that never comes back costs
  nothing — the next `GET` reports where the work actually got to, and repeating the call
  resumes from whatever stage is already a fact on disk.

  The one thing that cannot be derived — whether a step is executing *now* rather than
  having stopped half way — is held in memory and reported as `running`. Deliberately not
  persisted: a restart kills the work, so a marker that survived it would be claiming
  something false.

  Deriving a template with `pkg -c` **stays synchronous**. The line is not "slow" in the
  abstract but whether the operator needs to be told what is happening to keep using the
  tool: ~25 s measured on the deployment target is a spinner; a 372 MB fetch plus
  `freebsd-update` is not.

  No code yet — this is the decision and the records that pointed at it. The
  implementation is the rest of the Templates milestone.

### API

- **A dataset reports the native ZFS properties it sets.** `GET /api/jails` and
  `GET /api/jails/{name}` carry a `properties` map beside each dataset: the four an
  existing dataset may be given (JD-023), as the strings `zfs` prints — so `refquota`
  reads `2G` and a client can send the same value back. (#55)

  **The value the dataset sets, not the value in effect.** A property inherited from the
  dataset above is absent. Showing it would be friendlier right up to the moment someone
  edited it: the write would create a local override on the child rather than change the
  setting they were looking at.

  Only the allowlisted four. `canmount` is locally set on real datasets and stays out for
  the same reason it is out of the write list — it would stop a jail's dataset mounting.
  `mountpoint` keeps its own column.

  It costs no extra command: the `zfs get -s local all` that reads the tags carried these
  all along and was discarding them. The jail's user properties stay at the jail level
  (JD-004); nothing moved.

### Added

- **A jail's datasets show their properties, and a child's can be changed.** The chips
  under each dataset name are what it sets itself; a child gets a **Properties** form
  prefilled with them. An empty box leaves the property alone — there is no route that
  makes a dataset inherit one again. The jail's own dataset shows its properties and has
  no form, because the API writes to children and not to the dataset the jail runs from.

## v0.8.0 — 2026-10-23

[Release notes](https://github.com/ottermq/jaildeck/releases/tag/v0.8.0)

### API

- **Templates.** `GET /api/templates` lists them with their readiness and lineage, both
  read from ZFS. Two ways to build one. (#7, JD-027)

  **`POST /api/jails/{name}/template`** clones a jail you configured — the FreeBSD
  Handbook's "Cloning a Jail as a Template" (17.4.3). It captures whatever is in the jail,
  not only packages, and is the general path. Snapshot the jail as `@base`, clone it into
  `templates/<name>`, then **`zfs promote`** the clone.

  The promote is the step that is easy to omit and expensive to omit: without it the
  template is a clone *of* the jail, and `zfs destroy` on that jail answers `filesystem has
  dependent clones`. After it the jail is a clone of the template, which is also the right
  way round — a template should outlive the jail it came from.

  The jail must be **stopped**, and this refuses rather than stopping it: a snapshot of a
  running jail is an image caught mid-stride, and taking a service down is the operator's
  call. A jail already carrying `@base` is refused too, since reusing it would capture
  whenever that snapshot was taken rather than the jail now.

  **`PUT /api/templates/{name}`** is the shortcut for the package-only case: clone a
  parent's `@base`, `pkg -c <root> install -y`, snapshot as `@base`.

  **`GET /api/templates/{name}`** is one template with its `dependents` — every clone and
  every hold — which is what a removal would refuse over, answerable before asking for one.
  Both lists are present and empty rather than absent.

  **`DELETE /api/templates/{name}?confirm={name}`** destroys one, and **refuses while
  anything is cloned from it**, naming every jail and derived template in the way. ZFS
  would refuse too, but only after being asked and without saying which; this asks
  `zfs get clones` first. Never `zfs destroy -R`, which would take them with it. It is also
  what cleans up the half-built template a failed derive leaves behind.

  `pkg -c` chroots, so nothing is started: no jail, no stanza, no networking for a
  temporary jail. It is the same shape as the `freebsd-update -b <root>` JD-007 already runs
  against an unbooted template.

  **It installs packages and nothing else.** Anything further belongs to the other path —
  clone a template, create a jail on it, exec into it, snapshot the dataset — which is more
  capable and is the general answer. The two are the same mechanism at different times:
  provision at jail creation, or provision once into a template and clone it.

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

- **Templates and jail roots can be moved around from the interface.** The last of the
  write actions, which completes the pass: derive a template, remove one, restore a
  retained dataset, clone a jail as a template, and re-clone a jail's root. (JD-029)

  **A template snapshot is chosen, not typed.** Re-cloning takes the full
  `dataset@snapshot` with no default — every jail is already a clone of some template's
  `@base`, so defaulting to one would be a no-op dressed as an upgrade (JD-026) — but a
  string nobody can remember is not a choice either. The form offers the ready templates
  and sends the snapshot each one names.

  **A failed re-clone is rendered as the recovery procedure it is**: which step stopped,
  every dataset that is no longer under the jail and where it now sits, and that a restore
  brings it back. Nothing is retried and nothing is undone, so that message is the only
  record of where the data went.

  **Removing a template from its own page navigates back to the list**, because the thing
  the page was about is gone. A refusal stays, rendered against the dependents the page
  already lists.

  A restore puts a retained dataset under any jail under any name, which is what the API
  has always allowed and what makes "destroy the jail, keep the data" finish (JD-025).

- **Snapshots can be taken, rolled back to and destroyed from the interface.** (JD-029)

  **Taking one** covers the whole jail by default — one `zfs snapshot -r`, so every dataset
  is caught at the same instant (JD-024) — or one dataset alone. Taking one that already
  exists changes nothing and says so, which is what makes repeating a timed-out call safe.

  **Destroying one** asks for `confirm=<dataset>@<snapshot>` typed in full, because that is
  what the API asks for: `nightly` names a snapshot on every dataset that has one. At the
  jail's own dataset the destroy is recursive, and the confirmation **names every snapshot
  it would take** before anything runs.

  **A refusal is an answer, not a failure.** Nothing ran, so it is rendered under the row
  in the warning colour, naming the newer snapshots, the clones or the holds that stood in
  the way. A rollback refused over newer snapshots offers them back once — *destroy them
  and roll back* — because `destroy_newer=true` is the one refusal this API lets a client
  accept. Clones and holds get no such button: a hold exists to say "not this one", and
  taking a clone with its origin needs `zfs destroy -R`, which Jail Deck never passes.

- **A jail's tags and datasets can be changed from the interface.** The first of the write
  actions: add, edit and remove a ZFS tag; create a child dataset with its native
  properties; retain one past the jail. (JD-029)

  **A form opens where the thing is, and the modal is kept for what cannot be undone.**
  None of these destroys anything, so none of them gets a dialog. The dialog that does
  exist replaces the browser's `confirm()`, which could not carry the one thing the
  destructive operations need: the API will not destroy or roll back a snapshot without
  `confirm=<dataset>@<snapshot>` spelled out in full (JD-024), so the dialog asks for
  exactly that string and keeps its button disabled until it matches.

  **Nothing is validated twice.** A property name without a colon, a `recordsize` of 7K —
  both go to the server, and the server's own sentence comes back. The forms carry the
  rules as hints, because an operator should know before typing, but a hint that could
  disagree with ZFS is not a check.

  **Every write is followed by a read.** The server reads ZFS rather than remembering what
  it was told (JD-011), so a refetch is the only answer that cannot disagree with the host
  — and a command that failed can still have changed something.

  A 409 from the dataset endpoint is rendered from its body rather than as the word
  "Conflict": it carries the property, the value asked for and the value the dataset has,
  which is why the API answers that way instead of with a message.

- **The web interface reads the storage model.** Six releases of storage endpoints had
  nothing on screen; this is the reading half of catching up. (JD-028)

  **A page per jail** at `/jails/{name}`, behind the jail's name on the list: its datasets
  with the jail's own marked as such, its ZFS tags, and its snapshots newest first.

  **A Templates page** at `/templates` showing the lineage as an indented table — parent
  before child, ready or not — with the retained datasets below it, and a page per template
  at `/templates/{name}` listing what depends on it: the jails cloned from it, the templates
  derived from it, anything else, and holds.

  **What a clone is, is read from the host, not from the dataset's name.** Sorting
  dependents by looking for `/containers/` or `/templates/` in the path was tried and was
  wrong the first time it ran — `<pool>/jails/containers` is the layout Jail Deck creates,
  not where the jails it lists have to be. A clone is a jail when a jail reports that
  dataset as its own, and otherwise it is shown as the dataset it is.

  Nothing destructive is on these pages yet. The actions come next, each with the
  confirmation its refusal already defines.

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
