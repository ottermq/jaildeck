# Jail Deck

A lightweight operations dashboard and host agent for FreeBSD jails.

Jail Deck is a thin layer over the tools FreeBSD already ships — `jls`, `service`,
`jail.conf`, `zfs` — exposed through a JSON API and an embedded web interface. One
binary, no database, no Node.js at runtime, and no abstraction that pretends a jail
is a Linux container.

**Current release: v0.24.0.** Jails can be created from release templates, started,
stopped, restarted and destroyed from the browser or the API, and their limits changed in place; their ZFS datasets,
snapshots and resource usage are visible, and a dataset kept past its jail can be restored or destroyed; host setup is checked; PF can be seen,
read-only; bhyve VMs are created from cloud images with cloud-init, with a console password if wanted, pinned to host CPUs, started,
stopped, labelled, snapshotted and destroyed, on a default network Jail Deck sets up with NAT through PF, or on networks of their own kept apart by PF, with addresses Jail Deck picks and checks, and every network and what is on it can be seen; a jail has a shell in the
browser, and a guest its serial console; jails and guests are found in one list by name, state or label, and started, stopped, restarted or snapshotted several at once; every operation is audited, a console with who opened it and why when the client says, and nothing is reachable without a password or an
API token. Releases install and upgrade with `pkg` from a signed repository. See
[the roadmap](docs/ROADMAP.md) for what comes next.

Documentation is published at **[otter-labs.com/jaildeck](https://otter-labs.com/jaildeck/)**.

> Jail Deck is a personal project, written to learn Go and FreeBSD properly rather than
> to be a product. It is built for a single operator on hosts they administer directly,
> and it is also the host agent for Otter Labs Cloud. See [Scope](#scope).

## What works today

- **Jails** — every jail the host knows about, running or not, merging `jls` with
  `/etc/jail.conf` and `/etc/jail.conf.d/`, including the shared defaults a
  handbook-style layout puts outside any jail block.
- **Lifecycle** — start, stop and restart through `service jail`, idempotent: starting
  an already-running jail succeeds without touching it.
- **Operation history** — an append-only log of everything Jail Deck has run, recording
  the command that actually executed, its exit code, and whether it timed out. Filterable,
  and pages backwards with a cursor.
- **Authentication** — mandatory. argon2id for the operator password, named API tokens
  for scripts. There is no unauthenticated mode and no flag to disable it.
- **Firewall** — what PF is doing, read-only: whether it is loaded and enabled, the rules
  with their counters, NAT, every anchor's rules, tables and their addresses, and recent
  pflog lines. Nothing in Jail Deck can change PF (JD-020).
- **Virtual machines** — vm-bhyve's guests: each one's state, CPU, memory and whether it
  starts at boot, and for one guest its console, network interfaces and disks (JD-035).
  Start, stop and restart, from the VMs page or the API, confirmed in vm-bhyve's listing; a
  guest that ignores the power button for 60 seconds is powered off, and the answer says so
  (JD-036). Image templates for creating them: Debian 13's cloud image fetched, checked
  against Debian's checksum and written into a zvol, from the VMs page or the API (JD-037).
  Creating a guest from one — CPU, memory, a disk cloned from the image, a NIC on the
  operator's switch, SSH keys and a static address through cloud-init — and destroying it,
  from the VMs pages or the API (JD-038). A default VM network for every node — a private
  switch with the host as gateway, and outbound NAT through a PF anchor file Jail Deck owns —
  set up by `jaildeck init vm-network` or from the VMs page (JD-040).
- **Web interface** — an embedded Svelte application for all of the above, light and dark.

## Requirements

| | |
| --- | --- |
| Run | FreeBSD with jails. ZFS for the planned storage work |
| Build, backend | Go 1.26 or newer |
| Build, frontend | Node.js — at build time only, never at runtime |

Development works on any OS through the in-memory adapter — see [Development](#development).

## Build

```sh
make all      # frontend, then the binary -> bin/jaildeck
make build    # the binary alone, with Go only
```

`make build` deliberately does not depend on the frontend, so a clone without Node still
compiles and runs. It then serves a page saying the frontend has not been built.

### Native FreeBSD package

On FreeBSD 15 amd64, with Go, Node.js/npm and `pkg` installed:

```sh
make package VERSION=0.10.0   # -> bin/packages/jaildeck-0.10.0.pkg
```

The package contains the versioned executable with its built frontend, the existing
service script and the sample configuration. It does not configure or start Jail Deck,
and does not own live configuration, credentials, logs or jail datasets. The destination
host needs neither Go nor Node.js. See [native packaging](docs/PACKAGING.md) for building,
inspection, installation, upgrades, removal and migration from `make install`.

## First run

Jail Deck refuses to start without an operator credential, so setting one comes first:

```sh
jaildeck passwd            # prompts on the tty, with echo disabled
jaildeck passwd --stdin    # reads it from stdin, for unattended installs
```

It writes to `/var/db/jaildeck`, so it wants root — or point `JAILDECK_STATE_DIR` at a
writable path. The password is never an argument, so it cannot reach shell history.

Then start the server:

```sh
jaildeck
```

It binds `127.0.0.1:8888` by default. Point a browser there and sign in.

**To reach it from another machine**, forward the port rather than widening the bind:

```sh
ssh -L 8888:127.0.0.1:8888 operator@jailhost
```

Forwarding is not only safer: on a non-loopback bind over plain HTTP **no browser can
stay signed in**. The session cookie is `Secure`, so the browser discards it and every
request after a successful login comes back `401`, which looks exactly like a rejected
password. Browsers treat `localhost` as a secure context, so a forwarded port is exempt.

Jail Deck warns at startup when it is bound to a non-loopback address, because the
operator password crosses the network in plaintext unless something is terminating TLS
in front of it. It warns rather than refuses: a reverse proxy cannot be detected from
inside the process.

## Configuration

Environment variables, read once at startup. A `.env` file in the working directory is
loaded if present — see [`.env.sample`](.env.sample).

| Variable | Default | |
| --- | --- | --- |
| `JAILDECK_HOST` | `127.0.0.1` | Bind address. Anything non-loopback warns |
| `JAILDECK_PORT` | `8888` | |
| `JAILDECK_STATE_DIR` | `/var/db/jaildeck` | Password hash and API tokens, written `0600` |
| `JAILDECK_AUDIT_LOG` | `/var/log/jaildeck/audit.log` | Append-only JSON lines. Rotate it with `newsyslog` |
| `JAILDECK_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `JAILDECK_ADAPTER` | `freebsd` | `fake` runs against an in-memory host, for development |
| `JAILDECK_ZFS_POOL` | empty | Pool holding `<pool>/jails`. Empty adopts the only pool; with more than one, set it |
| `JAILDECK_SOCKET` | empty | Unix socket to listen on as well as TCP. Empty means TCP only |
| `JAILDECK_SOCKET_GROUP` | empty | Group owning the socket. Empty leaves it in the process's own group |
| `JAILDECK_SOCKET_ONLY` | `false` | `true` drops the TCP listener, leaving only the socket |

`debug` logs every command Jail Deck runs, which is the fastest way to see what it is
doing to the host. No credential material is logged at any level.

## Running as a service

`make install` puts the binary in `$PREFIX/bin`, the `rc.d` script in
`$PREFIX/etc/rc.d`, and a sample config in `$PREFIX/etc/jaildeck` (`PREFIX` defaults to
`/usr/local`; `DESTDIR` stages the same layout for a package build):

```sh
make install                 # on the FreeBSD host
make install DESTDIR=/stage  # or staged elsewhere
```

**Configuration lives in one file**, not in the script and not in `rc.conf`:

```sh
cp /usr/local/etc/jaildeck/jaildeck.env.sample /usr/local/etc/jaildeck/jaildeck.env
$EDITOR /usr/local/etc/jaildeck/jaildeck.env
```

It takes the same `KEY=value` lines as [`.env.sample`](.env.sample) and the same variables
as the table above, so one file configures Jail Deck whether it is started by hand or by
`rc.d`. The script sources it with `set -a`, so keep it to plain `KEY=value` — it is read
by `sh(1)`, not by a dotenv parser.

Set a credential first, since the service will not start without one, then enable it:

```sh
jaildeck passwd
sysrc jaildeck_enable=YES
service jaildeck start
```

The script itself has only four knobs, all overridable in `rc.conf` and none of them
Jail Deck's own configuration: `jaildeck_enable`, `jaildeck_env_file`,
`jaildeck_pidfile` and `jaildeck_daemon_args`.

Jail Deck runs in the foreground, so the script runs it under `daemon(8)` with `-f -S -T
jaildeck`: `-S` sends its output to syslog, which is why Jail Deck's own log lines carry
no timestamp, and `-f` detaches the descriptors so starting the service over `ssh` does
not hang the session.

```
Oct 22 22:23:51 isengard jaildeck[81634]: level=INFO msg=listening version=v0.7.0 addr=127.0.0.1:8888
```

`daemon(8)` is **not** given `-r`, so a stopped service stays stopped. Add it through
`jaildeck_daemon_args` for restart-on-exit. Replacing `-S` with `-o /var/log/jaildeck.log`
would write to a file instead — but Jail Deck emits no timestamps of its own, so those
lines would have none at all.

## Command line

```
jaildeck                      run the server
jaildeck version
jaildeck passwd [--stdin]     set the operator password
jaildeck token add <name> [--scope console]
                              generate a token and print it once
jaildeck token list
jaildeck token revoke <name>
```

`token add` prints the token on stdout and everything else on stderr, so the secret can
be redirected on its own:

```sh
jaildeck token add ci > /root/ci.token
```

**Scopes** (#178): a token can use the plain API, and only what its scopes add beyond it.
The one scope is `console`, a shell in a jail or a guest's serial console, which is root.
A token made without `--scope` can't open one, and is refused with 403. A token made
before scopes existed keeps every scope, and `token list` shows it as
`all (made before scopes)`. Scopes can't be edited: revoke the token and make it again.

On a host a platform manages, `JAILDECK_CONSOLE_UI=off` keeps the browser session from
opening a console, so the platform's own token, made with `--scope console`, is the only
way in through Jail Deck (#155).

## The API

Every endpoint lives under `/api`. Any other path serves the web interface, so a request
to an old unprefixed path answers `200` with HTML rather than failing loudly.

| | |
| --- | --- |
| `POST /api/auth/login` | password in, session cookie out |
| `POST /api/auth/logout` | |
| `GET /api/auth/me` | the current principal, when the session expires, and its `scopes` |
| `GET /api/about` | which Jail Deck is running: version, commit, when it started, its state directory |
| `GET /api/` | redirects to `/api/jails`, for a client that guessed the base path |
| `GET /api/host/readiness` | what the host has and lacks for jails, VNET, limits and VMs, each with its remedy |
| `GET /api/host/layout` | whether the jails dataset layout exists |
| `PUT /api/host/layout` | create the missing layout datasets, adopting what is there |
| `GET /api/jails` | every jail, running or merely configured, with its ZFS datasets |
| `GET /api/jails/{name}` | one of them, same shape |
| `GET /api/jails/{name}/configuration` | its own stanza as it is on disk, and the rctl rules keyed to its name |
| `POST /api/jails` | create a jail from a ready template: network (on a Jail Deck network by name, its address picked), limits, parameters, child datasets |
| `PUT /api/jails/{name}` | the same, idempotently: `201` made, `200` already matching, `409` with a diff |
| `DELETE /api/jails/{name}` | stop and destroy it, with `confirm=` |
| `POST /api/jails/{name}/exec` | run a command inside it, with a capped timeout and capped output |
| `GET /api/jails/{name}/console` | a shell in it, over a WebSocket; audited, nothing typed recorded; the `console` scope; optional `actor` and `reason`, recorded |
| `PUT /api/jails/{name}/datasets/{child}` | create a child dataset, or converge an existing one |
| `DELETE /api/jails/{name}/datasets/{child}?confirm={child}` | destroy one child and its snapshots, leaving the jail; the jail must be stopped |
| `PATCH /api/jails/{name}/properties` | tag the jail's own dataset |
| `PATCH /api/jails/{name}/limits` | change a jail's resource limits: live when it runs, and in its stanza; `null` removes one |
| `PATCH /api/jails/{name}/datasets/{child}/properties` | tag one of its children |
| `GET /api/jails/{name}/snapshots` | every snapshot of its datasets, oldest first |
| `PUT /api/jails/{name}/snapshots/{snapshot}` | snapshot the jail and its children, atomically |
| `DELETE /api/jails/{name}/snapshots/{snapshot}` | destroy it, with `confirm=` |
| `POST /api/jails/{name}/snapshots/{snapshot}/rollback` | roll the jail's own dataset back |
| `PUT /api/jails/{name}/datasets/{child}/snapshots/{snapshot}` | one dataset only |
| `DELETE /api/jails/{name}/datasets/{child}/snapshots/{snapshot}` | |
| `POST /api/jails/{name}/datasets/{child}/snapshots/{snapshot}/rollback` | |
| `POST /api/jails/{name}/datasets/{child}/retain` | move a dataset out of the jail |
| `POST /api/jails/{name}/datasets/{child}/restore` | move a retained one back in |
| `GET /api/retained` | datasets being kept past their jails, with their snapshots |
| `DELETE /api/retained/{name}?confirm={name}` | destroy a retained dataset and its snapshots |
| `GET /api/templates` | templates, their readiness and their lineage |
| `GET /api/templates/{name}` | one, with every jail and template cloned from it |
| `PUT /api/templates/{name}` | derive one from another, plus packages |
| `DELETE /api/templates/{name}` | destroy one, refusing while jails are cloned from it |
| `POST /api/jails/{name}/template` | clone a configured jail as a template |
| `POST /api/jails/{name}/reclone` | replace its root from a newer template, keeping its children |
| `POST /api/jails/{name}/start` | |
| `POST /api/jails/{name}/stop` | |
| `POST /api/jails/{name}/restart` | |
| `GET /api/audit` | operation history, newest first |
| `GET /api/metrics` | host and per-jail resource usage, and disk health |
| `GET /api/firewall` | PF's state, rules with counters, NAT, anchors and tables, read-only |
| `GET /api/firewall/tables/{name}` | one table's addresses |
| `GET /api/firewall/log` | the most recent pflog lines |
| `GET /api/vms` | vm-bhyve's guests and their state, and whether vm-bhyve is ready |
| `GET /api/vms/{name}` | one guest's console, network interfaces, disks and CPU pin |
| `GET /api/vms/{name}/configuration` | its vm-bhyve configuration and cloud-init seed as they are on disk, a password's value masked |
| `POST /api/vms/{name}/start` | start it and confirm it is running |
| `POST /api/vms/{name}/stop` | the power button, then a poweroff after 60 seconds, reported as `forced` |
| `POST /api/vms/{name}/restart` | stop, then start |
| `PATCH /api/vms/{name}/properties` | label a guest: ZFS user properties on its dataset, as a jail is tagged; `null` removes one |
| `PUT /api/vms/{name}/snapshots/{snapshot}` | snapshot a guest and its disks; a running one is taken as it is and the answer says `crash-consistent` |
| `POST /api/vms` | create a guest from an image: CPU and a pin to host CPUs, memory, disk, a NIC on a switch, SSH keys, a static address, picked by Jail Deck on its own networks |
| `PUT /api/vms/{name}` | the same, idempotent: create it, or say it already matches, or how it differs |
| `GET /api/vms/{name}/console` | its serial console, over a WebSocket; one at a time, audited; the `console` scope; optional `actor` and `reason`, recorded |
| `GET /api/networks` | tenant networks: a bridge each, its subnet, gateway, members and the addresses in use |
| `POST /api/networks` | create one: a bridge on a free /24 of the range, a vm-bhyve switch on it, rules keeping it apart |
| `GET /api/networks/{name}` | one tenant network, with the addresses in use and whose they are |
| `DELETE /api/networks/{name}?confirm={name}` | destroy an empty one |
| `GET /api/names/suggest` | a free name for a new jail or guest, adjective and animal: `jolly-otter` |
| `DELETE /api/vms/{name}?confirm={name}` | destroy a stopped guest and its disk |
| `GET /api/vm-images` | VM image templates, and the sources they can be prepared from |
| `GET /api/vm-images/{name}` | one image: its state, the build it was written from, the guests cloned from it |
| `PUT /api/vm-images/{name}` | prepare one from a source — `202`, then watch it |
| `DELETE /api/vm-images/{name}?confirm={name}` | remove one no guest uses |
| `GET /api/topology` | every bridge and vm-bhyve switch, its addresses and NAT, and the jails and guests on it |
| `GET /api/networks/default` | the default VM network, part by part, with whose each part is and the remedy |
| `PUT /api/networks/default` | set up Jail Deck's parts of it: switch, address, private ports, forwarding, NAT rules |

A jail carries its storage: `datasets` is the dataset mounted at the jail's `path` plus
everything under it, and `properties` is the ZFS *user* properties of the jail's own
dataset — tag a jail with `zfs set otter:instance=web-01 <pool>/jails/containers/<name>`
and find it again from the listing alone, with nothing stored in Jail Deck.

```json
{ "name": "ottermq", "path": "/usr/local/jails/containers/ottermq",
  "datasets": [ { "name": "tank/jails/containers/ottermq",
                  "mountpoint": "/usr/local/jails/containers/ottermq",
                  "used": 31850496, "referenced": 416907264, "available": 963201556480 } ],
  "properties": { "otter:instance": "mq-01" } }
```

The sizes are **bytes**, not the `1.41G` that `zfs list` prints, so they can be compared
and totalled. `referenced` can exceed `used` on a jail cloned from a template, because a
clone still refers to the template's blocks. Both keys are **absent** for a jail with no
dataset of its own — and also when the pool could not be read, which is logged at `WARN`
rather than failing the request, so that an unreadable pool never costs you the list of
jails you were about to start (JD-022).

### Writing storage

```sh
curl -X PUT .../api/jails/ottermq/datasets/data \
  -d '{"properties":{"refquota":"2G","recordsize":"16K","compression":"lz4"}}'
# 201 created, 200 it was already there, 409 it exists somewhere else
```

Five native properties may be set — `mountpoint`, `refquota`, `recordsize`, `compression`,
`atime` — and `mountpoint` only when creating. Anything else is `400` naming the list, because
`sharenfs` would export the jail's data and `canmount` would stop it mounting, and both are
editable as far as `zfs` is concerned. `quota` is not offered: JD-007 puts the limit on
`refquota` because a clone's `referenced` space includes the template it was cloned from.

The quota lands where it belongs. On a jail with a 2G `refquota` on its `data` dataset, `df`
**inside the jail** reports 2.0G, not the pool's free space.

Values are not validated here. `zfs` answers `recordsize=7K` with "must be power of 2 from
512B to 16M", and that sentence is what comes back — as a `400`, not a `500`, so a client does
not retry a value that can never work.

Tagging is separate, because tags merge where a dataset spec converges:

```sh
curl -X PATCH .../api/jails/ottermq/properties \
  -d '{"otter:instance":"mq-01","otter:retired":null}'
```

`null` removes a property (`zfs inherit`), which is not the same as setting it empty. Only user
properties — the name must contain a colon — and the value is passed through untouched (JD-011).
`PATCH` rather than `PUT` so that a replacing write cannot strip a tag another tool set; on a
real host `zrepl:placeholder` and `com.sun:auto-snapshot` both live in this namespace.

### Snapshots

A jail-level snapshot is `zfs snapshot -r`: the jail **and every child dataset at one point in
time**, which is what makes a root re-clonable while its data stays consistent with it.

```sh
curl -X PUT .../api/jails/ottermq/snapshots/before-upgrade
# 201 {"created":["tank/.../ottermq@before-upgrade","tank/.../ottermq/data@before-upgrade"]}
```

**A rollback is never recursive**, because ZFS offers no way to make it so — `zfs rollback` has
no flag that descends into children, and its own `-r` means "destroy the snapshots newer than
this one". So a jail-level rollback returns the jail's own dataset and leaves its children where
they are. Roll a child back through its own route.

Destroy and rollback both take `confirm=`, and it is the snapshot's **full** name:

```sh
curl -X POST '.../snapshots/before-upgrade/rollback?confirm=tank/jails/containers/ottermq@before-upgrade'
# 409 {"blocked":{"newer":["tank/jails/containers/ottermq@after-upgrade"], ...}}
# add &destroy_newer=true to accept losing exactly those
```

A refusal names what is in the way, and comes **before** anything destructive runs — what blocks
a destroy is asked for with `zfs get clones` and `zfs holds`, not read out of the error `zfs
destroy` would have raised. A clone or a hold cannot be overridden here at all: getting past a
clone needs `zfs destroy -R`, which takes the clone with it, and a hold exists to say "not this
one".

Snapshots are ordered by `create_txg`, ZFS's transaction group, not by `created`: a timestamp has
second granularity and two snapshots taken in the same second tie on it, which is precisely when
getting the order wrong destroys something it should not have.

### Keeping data past the jail that held it

`zfs destroy -r` on a jail takes its children and their snapshots with it, so a dataset worth
keeping has to stop being a child first.

```sh
curl -X POST .../api/jails/ottermq/datasets/data/retain -d '{"name":"ottermq-data"}'
# {"to":"tank/jails/retained/ottermq-data","mountpoint":"/usr/local/jails/retained/...","remounted":true}
# ... destroy and recreate the jail ...
curl -X POST .../api/jails/ottermq/datasets/data/restore -d '{"from":"ottermq-data"}'
```

Three commands under the hood — `zfs rename -u`, then `unmount`, then `mount` — and the `-u`
matters. A plain `zfs rename` unmounts and remounts inside the same command, which on a running
jail is the filesystem vanishing from under whatever is using it: `ls /data` answers
`No such file or directory`, mid-rename. With `-u` the rename is invisible and the unmount is a
separate, deliberate step.

**It still unmounts.** `remounted: true` means the filesystem moved, and a running jail loses
that path at that moment — stop it first. Leaving the move half done is worse: a dataset still
mounted inside a jail makes the jail undestroyable, with `zfs destroy` answering
`pool or dataset is busy`.

**When it is no longer wanted,** it is destroyed with its snapshots, and so can a child the jail
no longer needs, without the jail:

```sh
curl -X DELETE '.../api/retained/ottermq-data?confirm=ottermq-data'
curl -X DELETE '.../api/jails/ottermq/datasets/cache?confirm=cache'
```

Both refuse with `409`, before anything runs, while a snapshot has a hold or a dependent clone,
or while the dataset has datasets of its own, which `zfs destroy -r` would take without a word.
A child is refused while its jail runs: zfs would unmount it from under the jail without
complaint. The dataset is unmounted on its own first, because `zfs destroy -r` refused as busy
has already destroyed the snapshots (JD-025).

`<pool>/jails/retained` is created the first time one is needed and is not part of the layout
readiness check — a host that has never retained anything is not missing something.

Authenticate with a token, as a script would:

```sh
curl -sS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8888/api/jails
```

…or with the session cookie, as the browser does. Sessions live in memory and last 12
hours, so restarting the service signs every browser out; tokens are stored and survive.

`GET /api/audit` takes `operation`, `targets` (comma-separated), `success`, `timed_out`,
`request_id`, `limit` (50 by default, 200 at most) and `before` — an RFC 3339 timestamp
for paging backwards. Pass the timestamp of the oldest entry already received:

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  'http://127.0.0.1:8888/api/audit?operation=start&success=false&limit=50'
```

**A unix socket instead of a port.** `JAILDECK_SOCKET=/var/run/jaildeck.sock` adds a
second listener serving the same router, and `JAILDECK_SOCKET_ONLY=true` drops the TCP one
so there is no port at all — no bind address to widen and no TLS to terminate (JD-002):

```sh
curl -sS --unix-socket /var/run/jaildeck.sock \
  -H "Authorization: Bearer $TOKEN" http://localhost/api/jails
```

The socket is created mode `0660`, so its group is a first access check — but **not the
only one**: `RequireAuth` runs on this listener exactly as on TCP, so the token is still
required. `JAILDECK_SOCKET_GROUP` names the group; left empty the socket stays in the
process's own group, which for a root service on FreeBSD is `wheel`.

Jail Deck installs no signal handler, so `service jaildeck stop` leaves the socket file
behind. The next start probes it: if something answers, it refuses to start rather than
steal another instance's clients; if nothing does, it removes the leftover and binds. A
path holding something that is not a socket is a configuration error and is never deleted.

**Correlating your own job with the audit trail.** Send `X-Request-Id` on a request and
every audit entry it produces carries it back as `request_id`, which `GET
/api/audit?request_id=…` then filters on (JD-016). Nothing is stored that you did not
send: a request without the header produces entries with no `request_id` field at all.

The id may be up to 64 characters of ASCII letters, digits and `-._:` — enough for a UUID,
a ULID or a `trace:span` pair. An id outside that is **dropped, not rejected**: the
operation still succeeds and its entries simply carry none, with the reason logged. A
correlation id is a convenience, and failing a jail restart over a space in a header would
be the worse outcome.

A jail operation can legitimately run to its 150-second backstop, so an API client wants
a generous timeout. A `504` means Jail Deck stopped waiting — not that the jail failed to
start, and not that it will not finish.

### Prometheus

`GET /metrics` serves the same report as `GET /api/metrics` in the Prometheus text
exposition format — at the bare path a scraper expects, and **behind the same
authentication as everything else**:

```sh
jaildeck token add prometheus > /usr/local/etc/jaildeck.token
chmod 600 /usr/local/etc/jaildeck.token
```

```yaml
scrape_configs:
  - job_name: jaildeck
    static_configs:
      - targets: ['isengard:8888']
    authorization:
      credentials_file: /usr/local/etc/jaildeck.token
```

**There is no anonymous mode, and binding to loopback is not a substitute for one.** On
FreeBSD a jail configured with `ip4=inherit` — which is what a jail without an address of
its own gets — shares the host's network stack *including its loopback*. Measured on a
host with three jails: the one with its own address was refused, and the two with
`ip4=inherit` reached the host's `127.0.0.1:8888` and were served. An unauthenticated
metrics port would publish the host's memory, every jail's resource usage and every
disk's serial number to any jail on the box (JD-033).

The socket listener works too, and costs nothing extra — it serves the same router, so
`/metrics` is already there. Two things to know, both found by actually scraping it:

- **Prometheus can scrape a Unix socket only from v3.15.0** (2026-09-25). On FreeBSD that
  is the `prometheus3` package; plain `pkg install prometheus` gets **2.55.1**, which
  cannot, and needs a TCP target.
- **The token is still required.** The socket's `0660` group is a first access check and
  not the only one — `RequireAuth` runs on this listener exactly as on TCP (JD-002).

The socket path goes in the label, not in `targets`: an address that is not a hostname is
rejected by `promtool check config`.

```yaml
      - targets: ['localhost:80']
        labels:
          __unix_socket__: /var/run/jaildeck.sock
```

Names follow Prometheus conventions rather than the JSON field names, because the reader
here writes `rate()`: bytes, seconds and ratios. Two that are worth knowing:

- **`jaildeck_jail_cpu_cores`** is cores, not `pcpu`'s percent-of-one-core — a jail on two
  whole cores reads `2` here and `200` in the JSON.
- **`jaildeck_host_cpu_seconds_total`** is converted with `kern.clockrate`'s **`stathz`**,
  not its `hz`. On this hardware those are 127 and 1000, so dividing by the wrong one is
  an eightfold error. The raw `jaildeck_host_cpu_ticks_total` is published alongside it,
  and a ratio of two of those is correct whatever a tick is worth.

A disk is published both ways on purpose: `jaildeck_disk_passed` is the drive's own
verdict and `jaildeck_disk_failing_attributes` is what smartctl flags, and they routinely
disagree. A rule that catches the disagreement is the point:

```yaml
- alert: DiskAttributeFailing
  expr: jaildeck_disk_failing_attributes > 0
  annotations:
    summary: '{{ $labels.device }} has a SMART attribute below threshold'
```

Jail Deck does no alerting of its own and holds no history. The scraper keeps the series;
something that already knows how to wake people decides when to (JD-011, JD-033).

This endpoint is not in the Swagger spec, deliberately: the spec describes the JSON API,
and the exposition format documents itself in its own `HELP` lines.

### The API reference

[`docs/api/swagger.json`](docs/api/swagger.json) describes every endpoint, generated from
annotations on the handlers themselves:

```sh
make doc     # regenerate it
```

Import it into Insomnia, Bruno, Postman or anything else that reads Swagger 2.0. Jail Deck
serves no Swagger UI — the spec is a file, and the tool you already use browses it better
than a page embedded in a jail host would.

`make check` fails if the committed spec is not what the annotations produce, and the test
suite fails in both directions besides — a route with no annotation, or an annotation whose
route no longer exists. swag silently skips an un-annotated handler, so without that test an
undocumented endpoint would produce a spec that is merely shorter, with nothing saying so.

[`docs/api/index.md`](docs/api/index.md) is the guide: the property allowlist, the worked
flows, and the behaviour that was measured on a real host rather than assumed.

## Development

The in-memory adapter needs neither FreeBSD nor jails, so the API and the interface can be
worked on anywhere:

```sh
export JAILDECK_STATE_DIR=/tmp/jaildeck-state
export JAILDECK_AUDIT_LOG=/tmp/jaildeck-audit.log
make build && ./bin/jaildeck passwd
make run-fake
```

Both paths are overridden because the defaults live under `/var`, which a non-root user
cannot create.

For frontend work:

```sh
make web-dev    # Vite on :5173, proxying /api to :8888
```

The dev server proxies `/api` so that development and production are same-origin, and the
authentication model never differs between them.

Before pushing:

```sh
make check      # gofmt, go vet, build, tests — the full gate
make test-race
make cover
make web-check  # svelte-check, tsc and the frontend tests
make help       # every target
```

## Documentation

| | |
| --- | --- |
| [`docs/SPEC.md`](docs/SPEC.md) | what Jail Deck is, what it is not, and why |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | milestones in dependency order, and the open questions |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | package layout and the domain/adapter boundary |
| [`docs/DECISIONS.md`](docs/DECISIONS.md) | every decision and its reasoning, JD-001 onwards |
| [`docs/UI.md`](docs/UI.md) | interface conventions |
| [`CHANGELOG.md`](CHANGELOG.md) | every release |

Decisions are cited by number — JD-003, JD-015 — in the code and throughout the docs, so
a comment explaining *why* can point at the argument instead of repeating it.

## Scope

Jail Deck is a **single-host** operations dashboard and host agent for FreeBSD jails and
bhyve VMs, covering the native lifecycle: template preparation, creation, lifecycle
control, destruction and observability. It is not multi-host, not an OCI runtime, and
never the author of `pf.conf`.

Automation is a first-class consumer rather than an afterthought: Otter Labs Cloud drives
Jail Deck through the same API the web interface uses. The boundary is deliberate — Jail
Deck exposes generic *host* primitives, and anything that only makes sense to a cloud
platform, such as customers, quotas or billing, stays in that platform. The test for any
feature is whether a single operator running Jail Deck on their own box would ever want it.

[Sylve](https://github.com/AlchemillaHQ/Sylve) is a more complete FreeBSD jail and VM
manager, and an inspiration. Jail Deck is not chasing feature parity with it; it grows by
need.

## License

MIT — see [LICENSE](LICENSE).
