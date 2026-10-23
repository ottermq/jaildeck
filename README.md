# Jail Deck

A lightweight operations dashboard and host agent for FreeBSD jails.

Jail Deck is a thin layer over the tools FreeBSD already ships — `jls`, `service`,
`jail.conf`, `zfs` — exposed through a JSON API and an embedded web interface. One
binary, no database, no Node.js at runtime, and no abstraction that pretends a jail
is a Linux container.

**Current release: v0.6.0.** Jails can be listed, started, stopped and restarted from
the browser or the API, every operation is audited, and nothing is reachable without a
password or an API token. Host setup checks, ZFS storage, release templates, jail
creation and bhyve VMs are planned — see [the roadmap](docs/ROADMAP.md).

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
jaildeck token add <name>     generate a token and print it once
jaildeck token list
jaildeck token revoke <name>
```

`token add` prints the token on stdout and everything else on stderr, so the secret can
be redirected on its own:

```sh
jaildeck token add ci > /root/ci.token
```

## The API

Every endpoint lives under `/api`. Any other path serves the web interface, so a request
to an old unprefixed path answers `200` with HTML rather than failing loudly.

| | |
| --- | --- |
| `POST /api/auth/login` | password in, session cookie out |
| `POST /api/auth/logout` | |
| `GET /api/auth/me` | the current principal and when the session expires |
| `GET /api/jails` | every jail, running or merely configured |
| `POST /api/jails/{name}/start` | |
| `POST /api/jails/{name}/stop` | |
| `POST /api/jails/{name}/restart` | |
| `GET /api/audit` | operation history, newest first |

Authenticate with a token, as a script would:

```sh
curl -sS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8888/api/jails
```

…or with the session cookie, as the browser does. Sessions live in memory and last 12
hours, so restarting the service signs every browser out; tokens are stored and survive.

`GET /api/audit` takes `operation`, `targets` (comma-separated), `success`, `timed_out`,
`limit` (50 by default, 200 at most) and `before` — an RFC 3339 timestamp for paging
backwards. Pass the timestamp of the oldest entry already received:

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  'http://127.0.0.1:8888/api/audit?operation=start&success=false&limit=50'
```

A jail operation can legitimately run to its 150-second backstop, so an API client wants
a generous timeout. A `504` means Jail Deck stopped waiting — not that the jail failed to
start, and not that it will not finish.

`collections/insomnium.json` is an Insomnium collection covering every endpoint. `make
check` fails if it stops doing so, in either direction — a route with no request, or a
request whose route no longer exists — so it cannot quietly fall behind the API.

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
make web-check  # svelte-check and tsc
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
