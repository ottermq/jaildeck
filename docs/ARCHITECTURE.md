# Jail Deck — Architecture

## Status

Draft. This document describes the intended architecture for the first implementation phase.

## Architectural summary

Jail Deck is a Go web application that renders HTML on the server, enhances selected interactions with HTMX, and interacts with FreeBSD through small service-layer adapters around native system commands and files.

```text
Browser
  -> HTML pages and HTMX requests
  -> Chi router
  -> HTTP handlers
  -> application services
  -> FreeBSD adapters
  -> native tools and system files
```

The application should remain easy to reason about. The web layer should not directly shell out to system commands. System access should pass through explicit services and adapters.

## Runtime shape

The default runtime shape should be:

```text
jaildeck binary
  - embedded templates
  - embedded static assets
  - HTTP server
  - FreeBSD command adapters
  - optional local persistence later
```

In production, the goal is to avoid a separate frontend runtime or build pipeline.

## Baseline stack

| Concern | Choice |
| --- | --- |
| Language | Go |
| HTTP router | Chi |
| HTTP foundation | `net/http` |
| Templates | `html/template` |
| UI enhancement | HTMX |
| Styling | Plain CSS initially |
| Static assets | Embedded with Go `embed` |
| Persistence | None initially; SQLite only if needed |
| Logging | Structured logs preferred |
| Tests | Go unit tests with fakes around system adapters |

## Request lifecycle

A typical full-page request:

```text
GET /jails
  -> handler loads jail list through JailService
  -> service asks JailRepository or JailInspector
  -> adapter reads system state
  -> handler renders pages/jails.html inside layout/base.html
  -> browser receives full HTML page
```

A typical HTMX action:

```text
POST /jails/{name}/start
  -> handler validates jail name
  -> service starts jail through controlled adapter
  -> service reloads updated jail state
  -> handler renders components/jail_row.html
  -> HTMX swaps the updated row into the page
```

## Proposed directory layout

```text
cmd/
  jaildeck/
    main.go

internal/
  app/
    server.go
    routes.go

  config/
    config.go

  domain/
    jail.go
    service.go
    dataset.go
    snapshot.go
    task.go

  handlers/
    dashboard.go
    jails.go
    services.go
    storage.go
    logs.go
    settings.go

  services/
    jail_service.go
    service_service.go
    storage_service.go
    log_service.go
    task_service.go

  freebsd/
    command_runner.go
    jail_adapter.go
    service_adapter.go
    zfs_adapter.go
    log_adapter.go

  web/
    renderer.go
    htmx.go
    responses.go
    middleware.go

web/
  templates/
    layout/
      base.html
      sidebar.html
      topbar.html

    pages/
      dashboard.html
      jails.html
      jail_detail.html
      storage.html
      logs.html
      settings.html

    components/
      jail_row.html
      jail_status_badge.html
      jail_action_buttons.html
      dataset_row.html
      snapshot_row.html
      alert.html
      empty_state.html
      operation_result.html

  static/
    css/
      app.css
    js/
      htmx.min.js
```

This layout can change as the code reveals better boundaries, but the principle should remain: handlers handle HTTP, services handle application behavior, adapters handle FreeBSD integration.

## Layer responsibilities

### `cmd/jaildeck`

Owns process startup:

- parse config
- initialize logger
- create services
- create router
- start HTTP server
- handle shutdown

It should contain very little business logic.

### `internal/app`

Owns application assembly:

- dependency wiring
- route registration
- middleware setup
- server construction

### `internal/domain`

Contains simple domain types, such as:

- `Jail`
- `JailStatus`
- `JailService`
- `Dataset`
- `Snapshot`
- `Task`
- `OperationResult`

These should avoid HTTP-specific and command-specific details.

### `internal/handlers`

Contains HTTP handlers.

Handlers should:

- parse route parameters and form values
- call services
- decide whether to render a page or component
- return appropriate HTTP status codes
- avoid direct shell execution

### `internal/services`

Contains application use cases.

Services should:

- coordinate operations
- enforce safety rules
- normalize errors
- reload state after mutations
- produce data suitable for rendering

### `internal/freebsd`

Contains adapters around system tools and files.

Adapters should:

- execute allowlisted commands
- parse command output
- read system files safely
- return typed results
- preserve useful stderr/stdout for diagnostics
- avoid UI-specific concerns

### `internal/web`

Contains rendering and HTTP helpers.

Likely responsibilities:

- template loading
- template execution
- common response helpers
- HTMX detection helpers
- error rendering helpers
- shared middleware

## Command execution model

System commands should go through a dedicated runner, not through ad hoc `exec.Command` calls scattered across handlers.

A possible interface:

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

The runner should support:

- context cancellation
- timeouts
- structured logging
- argument separation, not shell string interpolation
- allowlisted commands
- captured stdout/stderr

Avoid using a shell unless absolutely necessary.

## FreeBSD adapters

### Jail adapter

Likely responsibilities:

- list running jails
- inspect jail IDs, names, paths, hostnames, IPs, and status
- start jail
- stop jail
- restart jail
- optionally inspect configured but stopped jails later

Possible native tools:

- `jls`
- `jail`
- `service jail onestart <name>`
- `service jail onestop <name>`
- `service jail onerestart <name>`

Exact commands should be validated during FreeBSD testing.

### Service adapter

Likely responsibilities:

- list available services inside a jail, if feasible
- check service status
- start, stop, restart selected services inside a jail

Possible native tools:

- `jexec`
- `service`

This area needs careful safety boundaries.

### ZFS adapter

Likely responsibilities:

- detect whether ZFS is available
- list relevant datasets
- show mountpoints, used space, available space, and origin
- list snapshots
- create snapshots later
- rollback snapshots later, with strong confirmation

Possible native tools:

- `zfs list`
- `zfs list -t snapshot`
- `zfs get`
- `zfs snapshot`
- `zfs rollback`

ZFS should be optional but first-class when present.

### Log adapter

Likely responsibilities:

- show relevant recent logs
- filter by jail name when possible
- expose command output for operations

Initial implementation can be simple and conservative.

## Template architecture

Templates should be organized around pages and reusable components.

### Pages

Pages are full views rendered inside the base layout.

Examples:

- dashboard
- jail list
- jail detail
- storage
- logs
- settings

### Components

Components are reusable fragments. HTMX endpoints should usually return components rather than full pages.

Examples:

- jail row
- jail action buttons
- status badge
- alert
- operation result
- dataset row
- snapshot row

## Rendering conventions

Suggested conventions:

- full-page handlers render templates under `pages/`
- HTMX handlers render templates under `components/`
- every mutation should return either an updated component or an operation result component
- errors should be renderable as HTML fragments
- components should avoid hidden dependencies on global page state

## Routing conventions

Possible initial routes:

```text
GET  /
GET  /jails
GET  /jails/{name}
POST /jails/{name}/start
POST /jails/{name}/stop
POST /jails/{name}/restart
GET  /jails/{name}/logs
GET  /storage
GET  /logs
GET  /settings
```

Later routes can be added for snapshots, services, package inspection, and configuration editing.

## HTMX conventions

HTMX should be used when it reduces complexity.

Good uses:

- refreshing a jail row after start/stop
- loading a detail panel
- polling an operation status
- replacing an alert area
- submitting small forms without a full page reload

Avoid using HTMX to recreate a complex client-side application.

## Error handling

Errors should be categorized before rendering.

Suggested categories:

- validation error
- permission error
- command not found
- command failed
- parse error
- unsupported system state
- timeout
- internal error

The UI should show enough information to help the operator diagnose the issue, especially when native command output is relevant.

## Security model

The security design is not finalized.

Important constraints:

- assume operations may be privileged
- never build shell commands from unsanitized user input
- validate jail names and route parameters
- use CSRF protection for mutating requests
- default to safe binding, probably localhost during early development
- make destructive actions explicit
- log privileged operations

## Authentication

Authentication remains open.

Possible first steps:

- localhost-only without login during early development
- single admin password
- reverse proxy authentication
- built-in sessions

This should be decided after the privilege model is clearer.

## Persistence

The first version should not require a database.

System state should be read from FreeBSD directly.

Persistence may be introduced later for:

- user settings
- task history
- operation logs
- UI preferences
- authentication sessions
- cached metadata

SQLite is the preferred first persistence option if needed.

## Testing strategy

The code should be testable without requiring dangerous system operations.

Suggested approach:

- unit test services with fake adapters
- unit test command output parsers with sample outputs
- unit test handlers with fake services
- integration test FreeBSD adapters separately
- keep command runner behavior small and well covered

## Development constraints

Some behavior can be developed on non-FreeBSD systems using fakes, but real adapter testing must happen on FreeBSD.

The architecture should make this split natural.

## Open architecture questions

1. What privilege model should be used?
2. Should there be a privileged helper process?
3. Should the first version bind only to localhost?
4. How should CSRF protection be implemented with HTMX?
5. Should task history exist before long-running operations are introduced?
6. How much jail configuration parsing belongs in the MVP?
7. Which commands are safe enough for initial operation support?
8. Should logs be read directly, through system tools, or both?
