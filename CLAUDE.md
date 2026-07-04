# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Jail Deck is pre-implementation. The repository currently contains only planning documents — there is no `go.mod`, no source code, and no Makefile yet. Before writing code, read the five planning docs in the repo root; they are the source of truth for scope and design decisions:

- **`DECISIONS.md`** — locked architectural decisions (JD-001..JD-012). **This is the highest-priority doc**: where it conflicts with `SPEC.md`, `ARCHITECTURE.md`, or `UI.md` (which describe earlier/broader thinking), DECISIONS.md wins. Do not revisit a locked decision without clear justification, and flag it explicitly if you think one needs revisiting.
- **`SPEC.md`** — product philosophy, what Jail Deck is/is not, MVP scope, baseline technical choices, open product questions
- **`ARCHITECTURE.md`** — layered architecture, proposed directory layout, command execution model, adapter responsibilities, open architecture questions
- **`ROADMAP.md`** — phased build order (Phase 0 research → Phase 8 packaging); work should generally follow this sequence rather than jumping ahead
- **`UI.md`** — page structure, component strategy, HTMX interaction patterns, copywriting guidelines

When these docs conflict with an instruction or assumption, treat the docs as authoritative and flag the conflict rather than silently picking one.

## What Jail Deck is

A lightweight, FreeBSD-first web dashboard for inspecting, operating, and gradually managing FreeBSD jails through native system tools (`jls`, `jail`, `jexec`, `service`, `sysrc`, `pkg`, `zfs`). It is a thin, honest layer over FreeBSD — not a container runtime, not an orchestrator, not a SPA. See `SPEC.md` for the full "is / is not" list.

## Baseline stack (once implementation starts)

| Concern | Choice |
| --- | --- |
| Language | Go |
| HTTP router | Chi |
| HTTP foundation | `net/http` |
| Templates | `html/template` (server-rendered) |
| UI enhancement | HTMX (partial page updates via HTML fragments) |
| Styling | Plain CSS, no build step |
| Static assets | Embedded with Go `embed` |
| Persistence | None initially; SQLite only if a clear need emerges |

Production must not require Node.js/npm or any frontend build pipeline — the deployable artifact is a single Go binary with embedded templates and assets.

## Architectural rules to preserve as code is added

- **Layering is one-directional and strict**: `handlers` (HTTP) → `services` (use cases, safety rules) → `freebsd` adapters (native command/file access). Handlers must never shell out directly; all system access goes through `internal/freebsd` adapters.
- **No shell string interpolation.** System commands run through a dedicated `CommandRunner` using argument arrays, not shell interpolation, with an allowlist of permitted commands, context cancellation/timeouts, and captured stdout/stderr. See "Command execution model" in `ARCHITECTURE.md`.
- **HTMX responses render `components/`, full-page responses render `pages/`.** Mutating endpoints should return an updated component or an operation-result fragment, not a redirect or JSON, per the HTML-over-JSON philosophy in `SPEC.md`.
- **Errors are categorized, not generic.** Validation, permission, command-not-found, command-failed, parse, unsupported-state, timeout, internal — surfaced with enough detail (including relevant native command output) for an operator to diagnose the failure.
- **Destructive/disruptive actions require confirmation** (stop, restart, snapshot rollback/delete, dataset removal) and should be logged as privileged operations.
- **The MVP does not edit critical system configuration files.** Read/operate first; safe config editing is an explicit later phase (`ROADMAP.md` Phase 7).

## Proposed directory layout

The intended structure (subject to change as real code reveals better boundaries — see `ARCHITECTURE.md` for full layer responsibilities):

```text
cmd/jaildeck/main.go            # process startup only, minimal logic

internal/
  app/            # dependency wiring, route registration, server construction
  config/
  domain/         # Jail, JailStatus, Dataset, Snapshot, Task, OperationResult
  handlers/       # parse HTTP, call services, choose page vs component render
  services/       # use cases: coordinate ops, enforce safety, reload state
  freebsd/        # command_runner, jail_adapter, service_adapter, zfs_adapter, log_adapter
  web/            # renderer, htmx detection, response helpers, middleware

web/
  templates/{layout,pages,components}/
  static/{css,js}/
```

## Development constraints

- Adapter logic (`internal/freebsd/*`) can only be fully verified on real FreeBSD; other layers should be developed and unit-tested against fakes so non-FreeBSD development remains possible.
- Testing strategy per `ARCHITECTURE.md`: unit test services against fake adapters, unit test command-output parsers against sample/fixture output, unit test handlers against fake services, and integration-test the real `freebsd` adapters separately on FreeBSD.
- ZFS support must be optional — the app should degrade gracefully (not crash or hide the whole storage section) on hosts without ZFS.
- Default to binding on localhost only during early development; the privilege model (run as root vs. `doas` vs. privileged helper) is an open, unresolved decision — don't assume one when writing privileged-operation code without flagging it.

## Key open decisions (do not assume answers)

These are explicitly unresolved in the docs; if a task depends on one, surface the ambiguity instead of guessing:

1. Privilege model for operations requiring root (see `SPEC.md` "Privilege model", `ARCHITECTURE.md` "Open architecture questions")
2. Authentication/bind strategy (localhost-only vs. LAN vs. login)
3. Whether operation/task history is persisted or memory-only
4. Exact native commands for jail start/stop/restart (needs FreeBSD validation)
5. How configured-but-stopped jails are discovered (vs. only running jails)
