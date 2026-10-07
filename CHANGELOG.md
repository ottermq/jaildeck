# Changelog

Every released version is recorded here. A version additionally gets a
[release](https://codeberg.org/Otter-Labs/jaildeck/releases) with full notes when
it carries something an upgrader has to act on — a changed output format, a new
mandatory step, a migration. Versions that only fix internal behaviour live here
alone.

Dates are the tag date. Issue numbers link to
[the tracker](https://codeberg.org/Otter-Labs/jaildeck/issues).

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
