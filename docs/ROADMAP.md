# Jail Deck — Roadmap

## Status

Draft. This roadmap is intentionally capability-based rather than date-based.

## Roadmap philosophy

Jail Deck should grow from visibility to safe operation, then to controlled management.

The project should not start by editing critical system configuration or inventing complex abstractions. It should first become a trustworthy dashboard over the system as it already exists.

## Phase 0 — Research and validation

Goal: understand the FreeBSD surface area before committing to implementation details.

### Outcomes

- confirm how to list running jails reliably
- confirm how to identify configured but stopped jails
- confirm common jail configuration locations
- confirm safe start/stop/restart commands
- confirm how service management behaves inside jails
- confirm useful ZFS commands and output formats
- confirm log locations and patterns
- decide initial privilege model
- decide initial authentication/bind strategy

### Deliverables

- command notes
- sample command outputs
- parser fixtures
- privilege model decision
- MVP route list

### Open decisions for this phase

- run as root, use `doas`, or use a privileged helper?
- bind to localhost only or support LAN access from the start?
- inspect only running jails first or include configured stopped jails?
- require ZFS for early testing or support non-ZFS immediately?

## Phase 1 — Skeleton application

Goal: create the basic Go application structure.

### Capabilities

- start HTTP server
- use Chi router
- render base layout
- serve embedded static assets
- load embedded templates
- show a basic dashboard page
- show application version/build info
- establish initial CSS
- establish component-oriented template layout

### Technical tasks

- create repository structure
- add `cmd/jaildeck/main.go`
- add route registration
- add renderer
- add base layout
- add app CSS
- add Makefile
- add basic tests

### Completion criteria

The application can run locally and render a simple Jail Deck page without external frontend tooling.

## Phase 2 — Read-only jail visibility

Goal: make Jail Deck useful without modifying the host.

### Capabilities

- list running jails
- parse jail metadata
- display status badges
- show jail list page
- show jail detail page
- display raw diagnostic information when parsing is incomplete
- handle empty state when no jails are running

### Technical tasks

- implement command runner
- implement jail adapter
- implement parser tests with sample outputs
- implement `JailService`
- implement jails handlers
- implement `jail_row` component
- implement jail detail page

### Completion criteria

A user can open Jail Deck and understand what jails are currently running.

## Phase 3 — Safe jail operations

Goal: support basic jail lifecycle actions.

### Capabilities

- start jail
- stop jail
- restart jail
- refresh jail row through HTMX
- show operation success/failure
- capture relevant command output
- prevent invalid actions when state is known

### Technical tasks

- add mutation routes
- add CSRF protection
- add operation result component
- add confirmation for stop/restart
- add command timeout handling
- add structured operation logging

### Completion criteria

A user can start, stop, and restart known jails from the UI and receive clear feedback.

## Phase 4 — Logs and operation history

Goal: make actions explainable.

### Capabilities

- show recent Jail Deck operations
- show command result history for current session
- show relevant system log snippets where feasible
- show failures in a readable format
- add logs page
- add recent operations section to jail detail

### Technical tasks

- define operation result model
- decide whether history is memory-only or persisted
- implement log adapter
- implement logs page
- implement operation result components

### Completion criteria

After an action fails, the user can inspect what was attempted and why it failed.

## Phase 5 — Storage visibility

Goal: expose ZFS information without performing destructive operations.

### Capabilities

- detect ZFS availability
- list relevant datasets
- show mountpoints
- show used and available space
- show snapshots, if straightforward
- handle systems without ZFS gracefully

### Technical tasks

- implement ZFS adapter
- add parser tests for ZFS output
- implement storage service
- implement storage page
- implement dataset row component
- implement snapshot row component

### Completion criteria

A user can understand the storage layout related to jails, especially on ZFS-based hosts.

## Phase 6 — Services inside jails

Goal: inspect and control selected services inside a jail.

### Capabilities

- inspect service status inside a running jail
- start selected service
- stop selected service
- restart selected service
- show service operation output

### Technical tasks

- validate safe `jexec` usage
- implement service adapter
- add service section to jail detail
- add service action components
- add confirmation where needed

### Completion criteria

A user can manage common rc.d services inside a running jail through controlled UI actions.

## Phase 7 — Safer management features

Goal: move from operation to controlled management.

Possible capabilities:

- create ZFS snapshot
- rollback ZFS snapshot with strong confirmation
- inspect installed packages inside a jail
- show jail configuration source
- edit limited safe settings
- reload jail configuration
- create a jail through a guided flow

These features should wait until the privilege model, command runner, logging, and confirmation patterns are mature.

## Phase 8 — Packaging and distribution

Goal: make Jail Deck feel native to install and operate on FreeBSD.

### Capabilities

- install binary
- install rc.d service script
- install default config
- document safe deployment modes
- provide upgrade path
- prepare FreeBSD package or port work

### Technical tasks

- refine Makefile with `PREFIX` and `BINDIR`
- add rc.d script
- add sample config file
- add installation docs
- add release build workflow

### Completion criteria

A user can install Jail Deck on FreeBSD in a repeatable way and run it as a service.

## Not planned for early versions

The following should not distract the first implementation phases:

- multi-host management
- cluster orchestration
- complex user/team permissions
- full terminal emulator
- broad configuration management
- plugin system
- heavy frontend framework
- production Node.js dependency
- remote cloud control plane

## Cross-cutting work

These concerns apply across phases.

### Documentation

Maintain:

- project specification
- architecture notes
- command behavior notes
- installation notes
- security notes
- troubleshooting notes

### Testing

Maintain:

- parser fixtures
- service unit tests
- handler tests
- adapter tests on FreeBSD
- regression tests for dangerous command construction

### Security

Continuously review:

- privilege boundaries
- command allowlist
- route validation
- CSRF protection
- default bind address
- authentication model
- destructive action confirmation

### UX

Continuously improve:

- status clarity
- operation feedback
- empty states
- error messages
- FreeBSD terminology explanations

## Current highest-priority open questions

1. What is the safest and simplest privilege model?
2. Should the first runnable version bind only to localhost?
3. How should configured but stopped jails be discovered?
4. What exact native commands should be used for start/stop/restart?
5. Should operation history be persisted or memory-only at first?
6. How should Jail Deck behave on systems without ZFS?
7. Should the first version include authentication, or rely on deployment constraints?
