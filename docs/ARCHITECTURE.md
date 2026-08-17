# Jail Deck — Architecture

## Architectural summary

Jail Deck is a Go backend exposing a JSON API, paired with a Svelte single-page frontend, interacting with FreeBSD through domain-owned adapters around native system commands and files.

```text
Browser (Svelte SPA)
  -> JSON API requests
  -> Chi router
  -> HTTP handlers
  -> domain application services (internal/jails, internal/storage)
  -> FreeBSD adapters (internal/system/freebsd)
  -> native tools and system files (jls, service, zfs, jexec, fetch, freebsd-update, jail.conf*)
```

The web layer never shells out directly. System access always passes through a domain's repository port, implemented by an adapter.

## Runtime shape

```text
jaildeck binary
  - embedded Svelte build output (static JS/CSS/HTML)
  - HTTP server serving the JSON API and the embedded SPA
  - FreeBSD command adapters
  - append-only operation audit log file
```

Node.js is required to *build* the frontend; it is never required to *run* jaildeck in production.

## Baseline stack

| Concern | Choice |
| --- | --- |
| Backend language | Go |
| HTTP router | Chi |
| API style | JSON over `net/http` |
| Frontend | Svelte, built with Vite, embedded via Go `embed` |
| Storage | ZFS (required) |
| Audit log | Append-only JSON-lines file (`internal/audit`) |
| Tests | Go unit tests with fakes around adapters; FreeBSD-only tests for real adapter behavior |

## Directory layout

```text
cmd/
  jaildeck/
    main.go                    # loads config, builds App, starts http.ListenAndServe

internal/
  app/
    app.go                      # composition root: wires adapters -> services -> handlers,
                                 # registers all routes

  config/
    config.go                   # env var loading (JAILDECK_HOST, JAILDECK_PORT)

  jails/                        # bounded context: Jails
    model.go                      # Jail entity, JailStatus
    service.go                     # JailName value object + validation, JailService
    repository.go                  # JailSystem port (List/Start/Stop/Restart)
    handler.go                     # JailHandler

  storage/                      # bounded context: Storage (ZFS + release templates) — not yet built
    dataset.go                    # Dataset, Snapshot entities/value objects
    template.go                   # Template entity: release, patch level, lifecycle state
    repository.go                  # StorageSystem port (create/clone/snapshot/list, fetch/extract/patch/update)
    service.go                     # application service
    handler.go

  audit/                        # shared infra, not a domain
    logger.go                     # Entry, Filter, Logger/Reader interfaces
    file_logger.go                 # FileLogger — append-only JSON-lines file, mutex-guarded
    service.go, handler.go

  common/
    errors.go                     # AppError + HandlerError — shared HTTP error translation used by every domain's handler

  system/
    command.go                    # Command, CommandResult, CommandRunner interface, CommandError
    exec_runner.go                  # ExecCommandRunner — the only place os/exec is called
    freebsd/
      adapter.go, jail.go             # implements jails.JailSystem
      jail_helper.go                  # jail.conf / jail.conf.d parsing + merge (JD-004)
      storage_adapter.go              # (not yet built) implements storage.StorageSystem — zfs, fetch, freebsd-update, jexec
    fake/
      jail.go                          # in-memory JailSystem, for non-FreeBSD dev (see commented-out line in app.go)

web/                             # Svelte frontend (separate module/build) — not yet built, see JD-009 / ROADMAP.md Phase 1b
  src/
    ...
  dist/                          # build output, embedded into the Go binary
```

This can change as the code reveals better boundaries — the principle should remain: **a domain package owns its entity, its port, its use cases, and its handler together; adapters only translate domain calls <-> native tools.**

## Layer responsibilities

### `cmd/jaildeck`

Process startup only: parse config, build the app, start the HTTP server, handle shutdown. No business logic.

### `internal/app`

Composition root. The only place that knows the full dependency graph: wires each domain's repository implementation into its service, wires services into handlers, registers routes.

### `internal/jails`, `internal/storage` (domain packages)

Each owns:

- **Entities and value objects** with real behavior and invariants — not anemic structs. Example: `JailName` (`internal/jails/service.go`) enforces the naming rule at construction via `NewJailName`; `Dataset`/`Template` will similarly enforce things like "cloning requires a `@base` snapshot to exist."
- **A repository interface (port)** describing what the domain needs from the system, owned by the domain — not by the infrastructure package that implements it. `jails.JailSystem` is defined in `internal/jails/repository.go` and speaks in `jails.Jail`; `freebsd.Adapter` implements it, not the other way around.
- **An application service** (`JailService`) that orchestrates the repository, enforces cross-cutting use-case rules (e.g. logging every mutation to the audit log via `internal/audit`), and returns data shaped for the handler. Repeated per-operation logic (validate name, call the system, log the result) is centralized in a single dispatch helper rather than duplicated per action.
- **An HTTP handler** (`JailHandler`) in the same package — there is no separate presentation-layer package.

### `internal/audit`

Shared infrastructure, not a bounded context — no business rules of its own, just an append-only log of what happened. Used by domain services (currently `JailService`; `storage`'s service will use it too once ZFS mutations exist).

### `internal/common`

Shared HTTP error plumbing: `AppError` (a status code + message) and `HandlerError(w, err)`, used by every domain's handler to turn a domain/service error into a consistent HTTP response instead of each handler reimplementing that translation.

### `internal/system`

Shared execution primitives (`CommandRunner`, `Command`, `CommandResult`, `CommandError`) plus the concrete adapters (`freebsd`, `fake`) that implement domain-owned repository ports. Everything that shells out goes through `CommandRunner.Run(ctx, Command{Name, Args})` — argument arrays, never string interpolation. Adapters implement the domain-owned repository interfaces; they don't define their own contracts.

## Request lifecycle

A typical read:

```text
GET /api/jails
  -> handler calls JailService.List
  -> service calls jails.JailSystem.List
  -> freebsd adapter shells out to jls, reads jail.conf*, merges
  -> handler serializes []Jail as JSON
  -> Svelte renders the list
```

A typical mutation:

```text
POST /api/jails/{name}/start
  -> handler validates the name is present, calls JailService.Start
  -> service constructs/validates a JailName, calls JailSystem.Start
  -> adapter runs `service jail start <name>`, re-reads state to confirm
  -> service logs the operation to internal/audit
  -> handler serializes the updated Jail (or an error) as JSON
  -> Svelte updates its local state and re-renders
```

A jail-creation flow:

```text
POST /api/jails
  -> handler parses {name, template} from the request body
  -> JailService (or a coordinating service) calls
       storage.Service.CloneTemplate(template, name)     -> zfs clone
       JailService.WriteConfig(name, ...)                 -> jail.conf.d/<name>.conf
       JailService.Start(name)                            -> service jail start
  -> each step's failure is reported distinctly; a partial failure should
     leave the system in a diagnosable state, not a silent half-created jail
```

The exact shape of the creation flow (which domain owns orchestration across the jail/storage boundary) is not finalized — worth deciding when jail creation is actually implemented, not guessed here.

## Command execution model

```go
type CommandRunner interface {
    Run(ctx context.Context, command Command) (CommandResult, error)
}

type Command struct {
    Name string
    Args []string
}

type CommandResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
}
```

Argument arrays, not shell string interpolation. No allowlist enforcement exists yet — an open item. Errors from adapters are wrapped in `system.CommandError` (command, args, raw result, underlying cause) — handlers and domain services use `errors.As` to pull it out for user-facing summaries.

## FreeBSD adapters

### Jail adapter (`internal/system/freebsd/adapter.go`, `jail.go`)

Implements `jails.JailSystem`: list (merge `jls` + `jail.conf*`), start/stop/restart via `service jail <action> <name>`.

### Storage adapter (`internal/system/freebsd/storage_adapter.go`)

Implements `storage.StorageSystem`. Responsibilities, mapped to the template/jail-creation workflow described in `docs/SPEC.md`:

- `zfs create`, `zfs clone`, `zfs snapshot`, `zfs list` / `zfs list -t snapshot`
- fetch a release's `base.txz` (`fetch`)
- extract it into a template dataset
- patch `resolv.conf`/`localtime` into a template
- bring a template to the current patch level (`freebsd-update --currently-running <ver> -b <path> fetch install`)
- run commands inside a jail (`jexec <name> <cmd>`) for provisioning

ZFS is required (JD-006), so this adapter can assume ZFS is present.

## API conventions

Being settled starting with `jails` and `audit` (`docs/ROADMAP.md` Phase 1); every domain added afterward follows the same shape from the start. Expected shape:

```text
GET    /api/jails
GET    /api/jails/{name}
POST   /api/jails/{name}/start
POST   /api/jails/{name}/stop
POST   /api/jails/{name}/restart
POST   /api/jails                    # create (clone template + configure + start)
GET    /api/templates
POST   /api/templates                # prepare a new release template
GET    /api/storage/datasets
GET    /api/storage/snapshots
GET    /api/operations
```

## Error handling

Categories: validation error, permission error, command not found, command failed, parse error, unsupported system state, timeout, internal error. For a JSON API these map to HTTP status + a structured error body (code, message, and — when safe — the relevant command output).

## Security model

Open items: no command allowlist yet, no CSRF protection (less relevant for a JSON API consumed by a same-origin SPA, but worth revisiting once auth exists), jail names and route parameters must be validated before reaching a command. Destructive storage operations (snapshot rollback, dataset removal) need explicit confirmation semantics once they exist — likely a confirmation flag/step in the API, not just a UI-level dialog, since the API can be called by more than the UI.

## Testing strategy

Unit test domain services with fake repositories, unit test command-output parsers with fixtures, integration-test the real FreeBSD adapters separately (only fully verifiable on FreeBSD). Each domain gets its own fake repository implementation for tests.

## Open architecture questions

1. Which domain (or a new coordinating layer) owns orchestration for multi-step flows that cross the jail/storage boundary, like jail creation?
2. What does partial-failure recovery look like for jail creation (clone succeeded, config write failed — now what)?
3. Should there be a command allowlist, and at what layer?
4. How should long-running operations (template fetch/extract/update) be represented in the API, once JD-008 is resolved?
5. Exact REST/JSON API shape — not finalized, see "API conventions" above.
6. Should `internal/audit` gain a real database backing, and if so, does that pull `storage`/`jails` metadata along with it, or stay scoped to operation history only?
