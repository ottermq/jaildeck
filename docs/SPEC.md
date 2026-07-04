# Jail Deck — Project Specification

## Status

Draft. This document captures the current agreed direction for Jail Deck before implementation starts.

## One-line description

Jail Deck is a lightweight, FreeBSD-first web dashboard for inspecting, operating, and gradually managing FreeBSD jails through native system tools.

## Purpose

Jail Deck exists to make day-to-day jail administration more practical and pleasant without hiding FreeBSD behind an alien abstraction. It should help an operator see what is running, understand how each jail is configured, perform common actions safely, and inspect related system resources such as networking, storage, services, logs, and snapshots.

The project should feel like a thin, helpful layer over FreeBSD rather than a separate platform.

## Product philosophy

### 1. FreeBSD first

Jail Deck is designed specifically for FreeBSD jails. It should embrace FreeBSD terminology, file locations, service management, ZFS integration, and operational conventions.

It should not pretend that jails are Linux containers, virtual machines, or cloud workloads.

### 2. Integrate, do not replace

Jail Deck should orchestrate and inspect existing tools instead of reimplementing them.

Likely integrations include:

- `jls`
- `jail`
- `jexec`
- `service`
- `sysrc`
- `pkg`
- `zfs`
- log files under `/var/log`
- `rc.conf` and jail configuration files

The UI should make native operations clearer, safer, and easier to repeat.

### 3. Minimal dependencies

Prefer the Go standard library and small, focused dependencies.

Avoid runtime dependencies that complicate FreeBSD deployment unless they provide clear value.

### 4. Single binary as the default deployment model

The ideal distribution model is a single Go binary plus static assets and templates embedded into that binary.

Installation should eventually feel natural on FreeBSD, such as:

```sh
pkg install jaildeck
```

or during early development:

```sh
make install
```

### 5. Server-rendered UI

Jail Deck should use server-rendered HTML as the primary interface.

The browser should receive complete pages or HTML fragments. JSON APIs may exist later, but they are not the foundation of the application.

### 6. HTML over JSON

User actions should usually follow this flow:

```text
Browser action
  -> HTTP request
  -> Go handler
  -> system/service operation
  -> render HTML fragment
  -> HTMX swaps part of the page
```

This keeps frontend state minimal and avoids a full SPA architecture.

### 7. Boring architecture

The application should be easy to understand, build, run, and debug.

A new contributor should be able to open the repository and understand the structure quickly.

### 8. Observable and explainable

Jail Deck should show what happened, what command or operation was attempted, whether it succeeded, and what the user can do next when something fails.

Errors should be understandable, not cryptic wrappers around command output.

### 9. Recoverable operations

Operations that modify the system should be designed with safety in mind.

When possible, Jail Deck should detect risk, confirm destructive actions, preserve logs, and leave the system in a known state after failures.

## Initial target user

The first target user is a technical FreeBSD learner or operator who is comfortable with servers but wants a clearer dashboard for jail-related administration.

The product should serve someone who is learning FreeBSD without becoming a toy. It should also remain useful to a more experienced operator who wants a practical local admin interface.

## What Jail Deck is

Jail Deck is:

- a web dashboard for FreeBSD jail operations
- a local or LAN-facing admin tool
- a thin interface over native FreeBSD tools
- a server-rendered Go web application
- an operational UI for jails, storage, logs, services, and system state
- a project that values clarity over automation magic

## What Jail Deck is not

Jail Deck is not:

- a replacement for FreeBSD jails
- a new container runtime
- a virtualization platform
- a Kubernetes-like orchestrator
- a cloud management platform
- a full configuration management system
- a mandatory abstraction over FreeBSD concepts
- a JavaScript-heavy SPA
- a tool that requires Node.js in production

## Baseline technical choices

These decisions are considered accepted unless implementation reveals a strong reason to revisit them.

| Layer | Choice | Notes |
| --- | --- | --- |
| Language | Go | Good fit for system tooling and static binaries. |
| HTTP router | Chi | Small, idiomatic, compatible with `net/http`. |
| Templates | `html/template` | Standard library, safe by default. |
| Interactivity | HTMX | Partial updates through HTML fragments. |
| Small client-side behavior | Alpine.js, only if needed | Optional, not a foundation. |
| Styling | Plain CSS initially | Keep the first version simple. |
| Frontend build step | None initially | Avoid Node-based production requirements. |
| Persistence | None initially; SQLite later if justified | Prefer reading system state directly first. |

## Core domains

The current domain map is:

- Dashboard
- Jails
- Services inside jails
- Storage and ZFS datasets
- Snapshots
- Networking
- Logs
- Tasks and operation history
- Settings

These domains should guide code organization, navigation, and future planning.

## MVP scope

The first useful version should focus on visibility and safe operations.

### MVP should include

- list existing jails
- show jail status
- show basic jail metadata
- start a jail
- stop a jail
- restart a jail
- open a jail detail page
- show services for a selected jail, where feasible
- show recent logs relevant to a selected jail
- show basic ZFS dataset information, where applicable
- show clear success and error messages
- use HTMX for partial updates where it simplifies the interaction

### MVP should avoid

- complex provisioning flows
- advanced template systems
- multi-host management
- role-based multi-user administration
- heavy frontend tooling
- automatic edits to critical configuration files before the behavior is well understood

## Later capabilities

Potential future capabilities include:

- jail creation wizard
- safer configuration editing
- dataset creation and mounting helpers
- snapshot creation and rollback
- package inspection inside jails
- service management inside jails
- terminal-like command execution through controlled operations
- richer task history
- backup/export helpers
- multi-user authentication
- API for external automation
- plugin-like integrations with existing FreeBSD jail managers, if useful

## Design constraints

### No production Node.js requirement

The production application should not require Node.js, npm, Vite, Quasar, Vue, React, or a frontend asset pipeline.

### No hiding native concepts

The UI may explain FreeBSD concepts, but it should not rename them into misleading generic terms.

For example, a jail is a jail. A dataset is a dataset. A service is a service.

### No unsafe magic

Jail Deck should not silently modify system files, destroy datasets, remove snapshots, or execute broad commands without clear user intent.

### No database-first model

Jail Deck should avoid inventing an internal desired state model too early.

The system state comes from FreeBSD first. Persistence can be added for settings, history, users, and cached metadata when there is a clear reason.

## Open questions

These decisions are intentionally left open.

### Privilege model

How should Jail Deck perform privileged operations?

Options to investigate:

- run the service as root
- run as a dedicated user with specific `doas` permissions
- split into unprivileged web process plus privileged helper
- use a local socket with a controlled command executor

This is one of the most important architecture decisions.

### Authentication model

Should the first version assume localhost-only access, LAN access with a password, or full login support from the beginning?

The safest default may be localhost-only until the privilege model is settled.

### Supported jail configuration styles

Which jail sources should Jail Deck inspect first?

Possibilities:

- `/etc/jail.conf`
- `/etc/jail.conf.d/*.conf`
- service-managed jails
- jails created by other FreeBSD jail tools

The first version should probably inspect running jails before trying to fully parse every possible configuration style.

### ZFS assumptions

Should ZFS be required, optional, or treated as a first-class feature when present?

The likely answer is: optional but strongly supported.

### Long-running operations

How should the UI represent operations that may take time?

Options:

- simple request/response for fast actions
- HTMX polling
- Server-Sent Events
- task table with refresh

### Configuration editing

Should Jail Deck edit system configuration files in the first version?

The current leaning is no. Initial versions should inspect and operate before editing critical files.

### Packaging

How soon should FreeBSD packaging be considered?

The likely approach is to keep the repository package-friendly from the beginning, but not block early development on creating a formal port.
