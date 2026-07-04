# DECISIONS.md

# Jail Deck — Architecture Decisions

This document records architectural decisions that have been intentionally locked for the project.

Its purpose is to avoid repeatedly revisiting the same topics and to provide a stable foundation for future development.

Each decision is identified by a unique ID.

---

## JD-001 — Runtime Privileges

**Status:** Locked

Jail Deck runs as **root**.

### Rationale

Jail Deck is a system administration tool whose primary responsibilities require elevated privileges. Running the application as root keeps the architecture simple and avoids introducing privileged helper processes, `doas` integration, or privilege escalation mechanisms during the MVP.

---

## JD-002 — Binding Model

**Status:** Locked

The MVP binds exclusively to:

```
127.0.0.1
```

Remote access is outside the scope of the MVP.

### Rationale

Local-only access significantly reduces complexity by eliminating authentication, authorization, TLS, and network security concerns during the initial development phase.

---

## JD-003 — Authentication

**Status:** Locked

The MVP implements **no authentication**.

### Rationale

Because the application is accessible only through localhost, authentication would provide little practical benefit while increasing implementation complexity.

Authentication will become mandatory if remote access is introduced in future versions.

---

## JD-004 — Jail Discovery

**Status:** Locked

Jail Deck discovers jails exclusively through:

```
jls
```

The MVP does **not** parse:

* `/etc/jail.conf`
* `/etc/jail.conf.d`

Configured-but-stopped jails are outside the scope of the MVP.

### Rationale

The objective of the MVP is operational management rather than configuration management.

---

## JD-005 — Jail Operations

**Status:** Locked

Mutating operations use FreeBSD's standard service interface.

Examples:

```
service jail start <name>
service jail stop <name>
service jail restart <name>
```

### Rationale

The `service` command is the standard administrative interface used by FreeBSD operators. The MVP intentionally builds upon this interface instead of invoking lower-level jail commands directly.

---

## JD-006 — Storage Model

**Status:** Locked

Jail Deck requires **ZFS**.

Jail root directories are expected to reside inside ZFS datasets.

Support for UFS or plain-directory jail roots is outside the scope of the MVP.

Future versions may support non-ZFS systems with reduced functionality.

### Rationale

ZFS is considered a foundational capability of Jail Deck rather than an optional enhancement.

---

## JD-007 — Operation History

**Status:** Locked

Every mutating operation is recorded in an append-only log file.

Each entry records, at minimum:

* timestamp
* operation
* target jail
* executed command
* exit code
* success or failure
* error summary (when applicable)

SQLite is **not** used for operation history.

The UI may keep a short in-memory list of recent events for convenience, but the append-only log file is the durable source of operation history.

### Rationale

Plain log files are simple, transparent, easy to inspect, and align well with traditional Unix administration.

---

## JD-008 — Long-running Operations

**Status:** Locked

The MVP executes operations synchronously.

Task queues, asynchronous execution, HTMX polling, and progress reporting are deferred to future versions.

### Rationale

The initial implementation should prioritize correctness and simplicity over advanced execution workflows.

---

## JD-009 — Initial User Interface

**Status:** Locked

The primary application page is:

```
/jails
```

The root URL redirects to:

```
/
    ↓
/jails
```

The interface uses:

* table-based layout
* simple confirmation dialogs
* server-rendered HTML
* HTMX partial updates

A dashboard is intentionally postponed.

### Rationale

The jail list is the primary workflow of the application and should be immediately accessible.

---

## JD-010 — Native Integration

**Status:** Locked

Jail Deck integrates existing FreeBSD facilities rather than replacing them.

Whenever practical, it relies on native tools such as:

* `service`
* `jls`
* `zfs`
* `pkg`
* `sysctl`
* `rc.conf`

### Rationale

Jail Deck is an orchestration and visualization layer built on top of FreeBSD, not an alternative implementation of its administration tools.

---

## JD-011 — Readability Over Cleverness

**Status:** Locked

The project favors explicit, predictable, and maintainable implementations over clever abstractions.

Examples include:

* standard library before external dependencies
* small focused libraries
* server-rendered HTML
* component-oriented templates
* straightforward project organization

### Rationale

Long-term maintainability is valued over short-term convenience.

---

## JD-012 — Technology Stack

**Status:** Locked

The MVP technology stack is:

| Layer                     | Technology                      |
| ------------------------- | ------------------------------- |
| Language                  | Go                              |
| HTTP Router               | Chi                             |
| Templates                 | `html/template`                 |
| Interactivity             | HTMX                            |
| Optional JavaScript       | Alpine.js (only when necessary) |
| Styling                   | Plain CSS                       |
| Production Frontend Build | None                            |

### Rationale

The chosen stack minimizes dependencies, embraces the Go standard library, and produces a single deployable binary without requiring Node.js in production.

---

## Decision Policy

Once a decision is marked as **Locked**, it should not be revisited without a clear technical or product justification.

Architectural evolution is expected, but changes should be deliberate and documented rather than occurring through incremental drift.
