---
title: "Interface"
description: "The web interface: what it shows and the conventions it follows."
---

# Jail Deck — UI Specification

## UI goal

Jail Deck should feel like a practical FreeBSD administration panel: clear, fast, restrained, and focused on operational confidence. The interface should help the user answer:

- What jails exist?
- What is running?
- What changed?
- What can I safely do next?
- What failed, and why?
- What templates are ready to create a jail from?
- What's using my ZFS storage?

## UI personality

Calm, utilitarian, readable, predictable, fast, explicit about risk, helpful to someone learning FreeBSD, efficient for repeated daily use. Should not feel like a marketing dashboard or generic cloud console.

## Visual direction

Priorities: readable tables, clear status badges, obvious action buttons, compact detail panels, good spacing, useful empty states, visible errors. Specific styling approach (plain CSS, a utility framework, component library) is an open question — see "Frontend architecture" below.

## Layout

A classic sidebar + top bar admin layout is the starting point:

```text
+--------------------------------------------------+
| Top bar: Jail Deck / host summary / user actions |
+----------------------+---------------------------+
| Sidebar              | Main content              |
|                      |                           |
| Dashboard            | Page title                |
| Jails                | Page actions              |
| Storage              | Content                   |
| Templates            |                           |
| Logs                 |                           |
| Settings             |                           |
+----------------------+---------------------------+
```

Keep the sidebar simple — avoid too many sections before the features justify their own pages.

## Navigation

Built: **Jails**, **Templates**, **Operations**, **Host**. A section stays highlighted on its detail pages, so `/jails/ottermq` keeps Jails lit.

Still on the sketch above: Dashboard, Storage, Logs, Settings. Possible later: Snapshots (if it earns a page separate from a jail's), Tasks (once long-running operations exist), Services, Packages, System.

## Page specifications

### Dashboard

Purpose: quick operational overview. Possible content: host name, FreeBSD version, running/stopped jail counts, storage summary, recent operations/errors, quick links. Can stay minimal — Jails and jail creation are the more important flows.

### Jails page

Core purpose and columns:

```text
Status | Name | JID | Hostname | IP | Path | Actions
```

Actions: View, Start, Stop, Restart, Create (opens the jail-creation flow). Action availability reflects current state (running: Stop/Restart/View; stopped: Start/View; unknown: View/Refresh).

### Jail creation flow

Purpose: turn the clone-configure-start sequence (`docs/SPEC.md`) into a guided flow.

Likely steps:

1. choose a ready template (only templates with a `@base` snapshot are selectable)
2. name the jail
3. review/adjust generated config (network interface, any service-specific stanza)
4. confirm and create — show per-step progress/result (clone, config write, start) since this is a multi-step operation that can partially fail

Exact interaction shape (single form vs. wizard steps, how failures mid-flow are presented) is not designed yet.

### Jail detail page

At `/jails/{name}`, reached from the jail's name on the list (JD-028). Built:

- header — name, status badge, JID, hostname, IP
- **Datasets** — every dataset of the jail, the one mounted at the jail's own path marked as such, with mountpoint and used/referenced/available (JD-022)
- **Tags** — the jail's `user:property` ZFS tags, its own and never a child's
- **Snapshots** — newest first, ordered by transaction group rather than by time (JD-024); fetched separately and allowed to fail on its own

Still to come: services, recent logs, recent operations, the actions, and which template the jail was cloned from.

### Storage page

Purpose: inspect ZFS storage relevant to jails. Since ZFS is required (JD-006), this page doesn't need a "no ZFS" empty state — but should still handle "no datasets under management yet" gracefully. Content: datasets, mountpoints, used/available space, snapshots.

### Templates page

Purpose: the userlands a jail is cloned from — the thing jail creation depends on. Built, at `/templates`:

- the templates as an **indented lineage**, parent before child, each one ready or not ready, with what it was derived from and what it costs (JD-028)
- **Retained datasets** below: the datasets that outlived their jail (JD-025), on the same page because they answer the same question
- a page per template at `/templates/{name}`: where it lives, and **what depends on it** — the jails cloned from it, the templates derived from it, anything else, and holds — which is what a removal would have to refuse over

Still to come: preparing a release template (its own long-running-operation problem, JD-008), deriving one from the interface, removing one, and a release picker sourced from `download.freebsd.org` or manual version entry, depending on how "which releases are supported" resolves (`docs/SPEC.md`).

### Logs page

Purpose: recent Jail Deck operation results, command failures, relevant jail log snippets where feasible.

### Settings page

Purpose: expose safe configuration visibility — HTTP bind address, detected FreeBSD paths, detected ZFS pool/dataset root, command paths, application version. Avoid dangerous editable settings.

## Frontend architecture

Not designed yet:

1. **Framework mode** — SvelteKit (file-based routing, more structure/conventions) or a plain Svelte + Vite SPA (fewer conventions, hand-rolled routing)? This decides several of the questions below.
2. **State management** — Svelte's built-in stores (`svelte/store`), Svelte 5 runes, or something else?
3. **Routing** — SvelteKit's built-in routing, or a lightweight router (`svelte-routing`, `page.js`) for a plain Svelte + Vite app; whether the route structure mirrors the API path structure from `docs/ARCHITECTURE.md`
4. **Component library / styling approach** — plain CSS, a utility framework, or a component library?
5. **API client pattern** — hand-rolled `fetch` wrappers, a generated client, or something like TanStack Query (`@tanstack/svelte-query`) for caching/refetch?
6. **Refresh pattern** — how the UI reflects state after a mutation: polling, optimistic updates, or plain refetch-after-mutation?
7. **Long-running operation UI** — once JD-008 resolves, how does the frontend represent a multi-minute template-preparation operation? (progress bar, polling a status endpoint, toast-on-completion?)
8. **Build tooling** — Vite, either standalone or via SvelteKit (which is itself Vite-based) — the specific setup depends on the framework-mode decision above.

These should be settled together when Phase 1b (`docs/ROADMAP.md`) actually starts, not decided piecemeal ahead of time.

## States

Every major section should handle loading, empty, success, warning, error, unsupported. This applies to multi-step flows (jail creation, template preparation) at the step level, not just the page level.

## Empty states

Should explain what is happening:

- "No running jails were found."
- "No recent operation history is available yet."
- "This jail is stopped, so service status cannot be inspected."
- "No templates are ready yet — prepare one before creating a jail."
- "No datasets found under the jails storage root."

## Error states

Should show what operation failed, a short explanation, relevant command output when safe, and a next suggested action when obvious. Avoid generic messages like "Something went wrong." For multi-step flows, show *which step* failed, not just that the overall flow failed.

## Confirmation behavior

Potentially destructive or disruptive operations require confirmation: stop jail, restart jail, rollback snapshot, delete snapshot, remove dataset.

## Accessibility and usability

Real buttons, real links, visible focus states, readable contrast, form labels, meaningful table headers, status text in addition to color.

## Copywriting guidelines

Use FreeBSD-native terminology, explain risky operations in plain language. Good: "Start jail", "Create jail from template", "ZFS is not available on this host." Avoid vague wording: "Launch container", "Destroy environment", "Unknown error."

## Open UI questions

1. ~~Table-first or card-first for the Jails page?~~ **Table-first**, with a page per jail behind the name (JD-028).
2. Browser `confirm()` dialogs or custom confirmation components?
3. Should logs appear inline on jail detail or only on a dedicated logs page?
4. Should the dashboard be meaningful from the start, or should `/` redirect to `/jails`?
5. How much explanatory text for FreeBSD learners?
6. Dark mode: early or postponed?
7. Collapsible or fixed sidebar?
8. Wizard-style or single-form jail creation?
9. How is template preparation progress shown, given it can take real time?
10. ~~Does the Templates page need its own space, or does it live as a section of Storage?~~ **Its own page**, which also carries the retained datasets (JD-028). Whether a Storage page is still wanted once a jail's own page shows its datasets is now the open question.
